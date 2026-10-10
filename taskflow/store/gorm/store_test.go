// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package gorm

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/OpenNSW/core/taskflow/store"
)

const (
	stepA = "0a0a0a0a-0000-4000-8000-00000000000a"
	stepB = "0b0b0b0b-0000-4000-8000-00000000000b"
	task1 = "task-1"
)

// newTestStore returns a store backed by an in-memory SQLite database holding one task row
// that has not started any step yet (seq 0).
func newTestStore(t *testing.T) (*TaskStore, context.Context) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	// Each connection to :memory: is its own database, so keep exactly one.
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	// The production table is Postgres (see the host's migrations). This mirrors it with column
	// types SQLite's driver understands: it only parses times for TIMESTAMP columns, so the model's
	// timestamptz cannot be used here. The guards under test are portable SQL.
	if err := db.Exec(`CREATE TABLE task_records_v2 (
		task_id TEXT PRIMARY KEY, task_type TEXT, state TEXT, render_config TEXT,
		parent_workflow_id TEXT, parent_step_id TEXT,
		task_workflow_id TEXT,
		active_task_template_id TEXT, root_workflow_id TEXT NOT NULL DEFAULT '',
		active_step_id TEXT NULL, seq INTEGER NOT NULL DEFAULT 0, data TEXT,
		claimed_by TEXT NULL, claimed_at TIMESTAMP NULL,
		created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL)`).Error; err != nil {
		t.Fatalf("create table: %v", err)
	}

	s := New(db)
	ctx := context.Background()
	s.InitTask(ctx, store.TaskRecord{TaskID: task1, TaskType: "TEST", State: "STARTING", Data: map[string]any{}})
	return s, ctx
}

func mustGet(t *testing.T, s *TaskStore, ctx context.Context) store.TaskRecord {
	t.Helper()
	got, ok := s.GetTask(ctx, task1)
	if !ok {
		t.Fatal("task not found")
	}
	return got
}

func wantRows(t *testing.T, got int64, err error, want int64) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Fatalf("rows affected = %d, want %d", got, want)
	}
}

func wantRow(t *testing.T, got store.TaskRecord, step string, seq int64, state string) {
	t.Helper()
	if got.ActiveStepID != step || got.Seq != seq || got.State != state {
		t.Fatalf("row = (%q, %d, %q), want (%q, %d, %q)", got.ActiveStepID, got.Seq, got.State, step, seq, state)
	}
}

func claim(step string, seq int64, data map[string]any) store.StepClaim {
	return store.StepClaim{StepID: step, Seq: seq, ActiveTaskTemplateID: "tmpl-" + step[:2], State: "STARTING_STEP", Data: data}
}

// InitTask is a no-op on conflict: TaskID is the parent's ActivationID, unique per invocation, so a
// conflict can only be a retry of the same StartTask call, never a different task. It must not
// overwrite anything — including columns it would otherwise own — or a retry that lands after the
// row has already progressed could silently rewind it.
func TestInitTask_IsANoOpOnConflict(t *testing.T) {
	s, ctx := newTestStore(t)

	n, err := s.ClaimStep(ctx, task1, claim(stepA, 4, map[string]any{"in": 1}))
	wantRows(t, n, err, 1)

	// A retry of StartTask for the same TaskID, as if the row had never progressed.
	s.InitTask(ctx, store.TaskRecord{TaskID: task1, TaskType: "TEST", State: "OLD", ActiveStepID: stepB, Seq: 99, Data: map[string]any{}})

	got := mustGet(t, s, ctx)
	wantRow(t, got, stepA, 4, "STARTING_STEP") // untouched: the retry changed nothing
}

func TestClaimStep(t *testing.T) {
	s, ctx := newTestStore(t)

	n, err := s.ClaimStep(ctx, task1, claim(stepA, 4, map[string]any{"in": "a"}))
	wantRows(t, n, err, 1)
	got := mustGet(t, s, ctx)
	wantRow(t, got, stepA, 4, "STARTING_STEP")
	if got.ActiveTaskTemplateID != "tmpl-0a" {
		t.Errorf("ActiveTaskTemplateID = %q", got.ActiveTaskTemplateID)
	}

	// A retry of the same step matches again, and sets data rather than merging it.
	n, err = s.ClaimStep(ctx, task1, claim(stepA, 4, map[string]any{"other": "x"}))
	wantRows(t, n, err, 1)
	if want := map[string]any{"other": "x"}; !reflect.DeepEqual(mustGet(t, s, ctx).Data, want) {
		t.Errorf("Data = %v, want %v (set, not merged)", mustGet(t, s, ctx).Data, want)
	}

	// The next step may share its seq with the previous one's persisted value.
	n, err = s.ClaimStep(ctx, task1, claim(stepB, 5, nil))
	wantRows(t, n, err, 1)
	wantRow(t, mustGet(t, s, ctx), stepB, 5, "STARTING_STEP")

	// A late attempt of A after B claimed changes nothing.
	n, err = s.ClaimStep(ctx, task1, claim(stepA, 4, map[string]any{"late": true}))
	wantRows(t, n, err, 0)
	wantRow(t, mustGet(t, s, ctx), stepB, 5, "STARTING_STEP")
}

func TestWriteRenderState(t *testing.T) {
	s, ctx := newTestStore(t)
	n, err := s.ClaimStep(ctx, task1, claim(stepA, 4, nil))
	wantRows(t, n, err, 1)

	n, err = s.WriteRenderState(ctx, task1, stepA, 4, "PENDING_USER", map[string]any{"form": "v1"})
	wantRows(t, n, err, 1)
	got := mustGet(t, s, ctx)
	wantRow(t, got, stepA, 4, "PENDING_USER")
	if want := map[string]any{"form": "v1"}; !reflect.DeepEqual(got.Data, want) {
		t.Errorf("Data = %v, want %v", got.Data, want)
	}

	// Wrong step, or wrong seq for the right step: dropped.
	for name, c := range map[string]struct {
		step string
		seq  int64
	}{"other step": {stepB, 4}, "other seq": {stepA, 3}} {
		t.Run(name, func(t *testing.T) {
			n, err := s.WriteRenderState(ctx, task1, c.step, c.seq, "WRONG", map[string]any{})
			wantRows(t, n, err, 0)
			wantRow(t, mustGet(t, s, ctx), stepA, 4, "PENDING_USER")
		})
	}
}

func TestPersistSubmission(t *testing.T) {
	s, ctx := newTestStore(t)
	_, _ = s.ClaimStep(ctx, task1, claim(stepA, 4, nil))
	_, _ = s.WriteRenderState(ctx, task1, stepA, 4, "PENDING_USER", map[string]any{})

	n, err := s.PersistSubmission(ctx, task1, stepA, 4, map[string]any{"answer": 42.0})
	wantRows(t, n, err, 1)
	got := mustGet(t, s, ctx)
	wantRow(t, got, stepA, 5, store.StateAdvancing)
	if want := map[string]any{"answer": 42.0}; !reflect.DeepEqual(got.Data, want) {
		t.Errorf("Data = %v, want %v", got.Data, want)
	}

	// Persisting again for the same step and seq no longer matches: seq has moved on.
	n, err = s.PersistSubmission(ctx, task1, stepA, 4, map[string]any{"answer": 0.0})
	wantRows(t, n, err, 0)
	wantRow(t, mustGet(t, s, ctx), stepA, 5, store.StateAdvancing)
}

func TestCompleteTask(t *testing.T) {
	s, ctx := newTestStore(t)
	_, _ = s.ClaimStep(ctx, task1, claim(stepB, 5, nil))

	n, err := s.CompleteTask(ctx, task1, 6)
	wantRows(t, n, err, 1)
	wantRow(t, mustGet(t, s, ctx), stepB, 6, store.StateCompleted)

	// A retry matches again; an older seq does not.
	n, err = s.CompleteTask(ctx, task1, 6)
	wantRows(t, n, err, 1)
	n, err = s.CompleteTask(ctx, task1, 5)
	wantRows(t, n, err, 0)
	wantRow(t, mustGet(t, s, ctx), stepB, 6, store.StateCompleted)
}

func TestGuardedWrites_UnknownTaskChangesNothing(t *testing.T) {
	s, ctx := newTestStore(t)

	n, err := s.ClaimStep(ctx, "nope", claim(stepA, 1, nil))
	wantRows(t, n, err, 0)
	n, err = s.WriteRenderState(ctx, "nope", stepA, 1, "S", nil)
	wantRows(t, n, err, 0)
	n, err = s.PersistSubmission(ctx, "nope", stepA, 1, nil)
	wantRows(t, n, err, 0)
	n, err = s.CompleteTask(ctx, "nope", 1)
	wantRows(t, n, err, 0)
	n, err = s.ClaimTask(ctx, "nope", userA, t1)
	wantRows(t, n, err, 0)
	n, err = s.ReleaseTask(ctx, "nope", userA)
	wantRows(t, n, err, 0)
}

var (
	t1 = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	t2 = t1.Add(time.Hour)
)

const (
	userA = "user-a"
	userB = "user-b"
)

func wantClaim(t *testing.T, got store.TaskRecord, holder string, at *time.Time) {
	t.Helper()
	if got.ClaimedBy != holder {
		t.Fatalf("ClaimedBy = %q, want %q", got.ClaimedBy, holder)
	}
	switch {
	case at == nil && got.ClaimedAt != nil:
		t.Fatalf("ClaimedAt = %v, want nil", got.ClaimedAt)
	case at != nil && (got.ClaimedAt == nil || !got.ClaimedAt.Equal(*at)):
		t.Fatalf("ClaimedAt = %v, want %v", got.ClaimedAt, *at)
	}
}

func TestClaimTask(t *testing.T) {
	s, ctx := newTestStore(t)
	wantClaim(t, mustGet(t, s, ctx), "", nil) // a new task is unclaimed

	n, err := s.ClaimTask(ctx, task1, userA, t1)
	wantRows(t, n, err, 1)
	wantClaim(t, mustGet(t, s, ctx), userA, &t1)

	// A repeat claim by the holder succeeds and keeps the original claimed_at.
	n, err = s.ClaimTask(ctx, task1, userA, t2)
	wantRows(t, n, err, 1)
	wantClaim(t, mustGet(t, s, ctx), userA, &t1)

	// Another user cannot take a held claim.
	n, err = s.ClaimTask(ctx, task1, userB, t2)
	wantRows(t, n, err, 0)
	wantClaim(t, mustGet(t, s, ctx), userA, &t1)
}

func TestReleaseTask(t *testing.T) {
	s, ctx := newTestStore(t)
	_, _ = s.ClaimTask(ctx, task1, userA, t1)

	// Only the holder can release.
	n, err := s.ReleaseTask(ctx, task1, userB)
	wantRows(t, n, err, 0)
	wantClaim(t, mustGet(t, s, ctx), userA, &t1)

	n, err = s.ReleaseTask(ctx, task1, userA)
	wantRows(t, n, err, 1)
	wantClaim(t, mustGet(t, s, ctx), "", nil)

	// Releasing an unclaimed task changes nothing; the store leaves it to the caller to decide
	// whether that is an error.
	n, err = s.ReleaseTask(ctx, task1, userA)
	wantRows(t, n, err, 0)

	// Once released, anyone can claim, and claimed_at is the new claim's time.
	n, err = s.ClaimTask(ctx, task1, userB, t2)
	wantRows(t, n, err, 1)
	wantClaim(t, mustGet(t, s, ctx), userB, &t2)
}

// A completed task cannot be claimed, but a claim held when it completed is kept as a record of
// who worked it, and the holder can still release it.
func TestClaimTask_CompletedTask(t *testing.T) {
	s, ctx := newTestStore(t)
	_, _ = s.ClaimTask(ctx, task1, userA, t1)
	_, _ = s.CompleteTask(ctx, task1, 1)
	wantClaim(t, mustGet(t, s, ctx), userA, &t1)

	n, err := s.ClaimTask(ctx, task1, userA, t2)
	wantRows(t, n, err, 0)
	n, err = s.ClaimTask(ctx, task1, userB, t2)
	wantRows(t, n, err, 0)
	wantClaim(t, mustGet(t, s, ctx), userA, &t1)

	n, err = s.ReleaseTask(ctx, task1, userA)
	wantRows(t, n, err, 1)
	n, err = s.ClaimTask(ctx, task1, userB, t2)
	wantRows(t, n, err, 0) // still completed
}

// Claims and step writes own separate columns: a claim must not make an open step stale, and the
// step writes must not drop the claim.
func TestClaimAndStepWrites_DoNotTouchEachOther(t *testing.T) {
	s, ctx := newTestStore(t)
	_, _ = s.ClaimStep(ctx, task1, claim(stepA, 4, nil))
	_, _ = s.WriteRenderState(ctx, task1, stepA, 4, "PENDING_USER", map[string]any{"form": "v1"})

	n, err := s.ClaimTask(ctx, task1, userA, t1)
	wantRows(t, n, err, 1)
	got := mustGet(t, s, ctx)
	wantRow(t, got, stepA, 4, "PENDING_USER")
	if want := map[string]any{"form": "v1"}; !reflect.DeepEqual(got.Data, want) {
		t.Errorf("Data = %v, want %v", got.Data, want)
	}

	// The step is submitted, the next step starts and the task completes, all under the claim.
	_, _ = s.PersistSubmission(ctx, task1, stepA, 4, map[string]any{"a": "submitted"})
	_, _ = s.ClaimStep(ctx, task1, claim(stepB, 5, nil))
	_, _ = s.WriteRenderState(ctx, task1, stepB, 5, "PENDING_USER", nil)
	_, _ = s.CompleteTask(ctx, task1, 6)
	wantRow(t, mustGet(t, s, ctx), stepB, 6, store.StateCompleted)
	wantClaim(t, mustGet(t, s, ctx), userA, &t1)

	// InitTask (a StartTask retry) does not reset the claim either.
	s.InitTask(ctx, store.TaskRecord{TaskID: task1, TaskType: "TEST", State: "OLD", Data: map[string]any{}})
	wantClaim(t, mustGet(t, s, ctx), userA, &t1)
}

// Many users claim at once and exactly one wins. The test database has one connection, so the
// claims are serialised; this checks that the guard admits a single holder, while Postgres's
// row lock does the same for truly concurrent updates.
func TestClaimTask_ConcurrentClaimsHaveOneWinner(t *testing.T) {
	s, ctx := newTestStore(t)

	const n = 20
	var (
		wg   sync.WaitGroup
		wins atomic.Int32
	)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := s.ClaimTask(ctx, task1, fmt.Sprintf("user-%d", i), t1)
			if err != nil {
				t.Errorf("claim %d: %v", i, err)
			}
			if rows == 1 {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := wins.Load(); got != 1 {
		t.Fatalf("%d claims won, want exactly 1", got)
	}
	if mustGet(t, s, ctx).ClaimedBy == "" {
		t.Fatal("task is unclaimed after the race")
	}
}

// The end-to-end example from the design doc: A runs at seq 4 and B is the final step. It
// includes the stale writes that arrive out of order.
func TestStepLifecycle_StaleWritesAreDropped(t *testing.T) {
	s, ctx := newTestStore(t)
	_, _ = s.ClaimStep(ctx, task1, claim(stepA, 4, nil))

	// A callback completes A and the wake-up persists before A's render write lands.
	n, err := s.PersistSubmission(ctx, task1, stepA, 4, map[string]any{"a": "submitted"})
	wantRows(t, n, err, 1)
	n, err = s.WriteRenderState(ctx, task1, stepA, 4, "PENDING_USER", map[string]any{})
	wantRows(t, n, err, 0)
	wantRow(t, mustGet(t, s, ctx), stepA, 5, store.StateAdvancing)

	// B claims, then a late persist for A (seq 4) arrives.
	n, err = s.ClaimStep(ctx, task1, claim(stepB, 5, map[string]any{"b": "inputs"}))
	wantRows(t, n, err, 1)
	n, err = s.PersistSubmission(ctx, task1, stepA, 4, map[string]any{"a": "late"})
	wantRows(t, n, err, 0)
	wantRow(t, mustGet(t, s, ctx), stepB, 5, "STARTING_STEP")

	// B renders, is submitted, and is the final step.
	n, err = s.WriteRenderState(ctx, task1, stepB, 5, "PENDING_USER", map[string]any{"b": "form"})
	wantRows(t, n, err, 1)
	n, err = s.PersistSubmission(ctx, task1, stepB, 5, map[string]any{"b": "submitted"})
	wantRows(t, n, err, 1)
	wantRow(t, mustGet(t, s, ctx), stepB, 6, store.StateAdvancing)

	n, err = s.CompleteTask(ctx, task1, 6)
	wantRows(t, n, err, 1)
	wantRow(t, mustGet(t, s, ctx), stepB, 6, store.StateCompleted)
}
