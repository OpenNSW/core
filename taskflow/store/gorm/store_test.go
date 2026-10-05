// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package gorm

import (
	"context"
	"reflect"
	"testing"

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
