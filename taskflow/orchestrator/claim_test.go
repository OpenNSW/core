// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package orchestrator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/OpenNSW/core/taskflow/store"
)

const (
	claimTaskID = "claim-task"
	holderA     = "user-a"
	holderB     = "user-b"
)

// claimFixture is an unclaimed task waiting for the user.
func claimFixture(t *testing.T, db store.TaskStore) *TaskManager {
	t.Helper()
	if err := db.InitTask(context.Background(), store.TaskRecord{
		TaskID: claimTaskID, TaskType: "TEST", State: "PENDING_USER", Data: map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	return newTestTaskManager(db, newTestRegistry(), &mockTemporalManager{}, noopCallback)
}

func claimRow(t *testing.T, db store.TaskStore) store.TaskRecord {
	t.Helper()
	row, ok := db.GetTask(context.Background(), claimTaskID)
	if !ok {
		t.Fatal("task row missing")
	}
	return row
}

func TestClaimTask_ClaimsAnUnclaimedTask(t *testing.T) {
	db := newSafeMockTaskStore()
	tm := claimFixture(t, db)
	before := time.Now().UTC()

	if err := tm.ClaimTask(context.Background(), claimTaskID, holderA); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	row := claimRow(t, db)
	if row.ClaimedBy != holderA {
		t.Errorf("ClaimedBy = %q, want %q", row.ClaimedBy, holderA)
	}
	if row.ClaimedAt == nil || row.ClaimedAt.Before(before) {
		t.Errorf("ClaimedAt = %v, want a time at or after %v", row.ClaimedAt, before)
	}
}

func TestClaimTask_RepeatClaimByTheHolderSucceeds(t *testing.T) {
	db := newSafeMockTaskStore()
	tm := claimFixture(t, db)
	ctx := context.Background()
	if err := tm.ClaimTask(ctx, claimTaskID, holderA); err != nil {
		t.Fatal(err)
	}
	first := *claimRow(t, db).ClaimedAt

	if err := tm.ClaimTask(ctx, claimTaskID, holderA); err != nil {
		t.Fatalf("repeat ClaimTask: %v", err)
	}
	if got := claimRow(t, db).ClaimedAt; got == nil || !got.Equal(first) {
		t.Errorf("ClaimedAt = %v, want the original %v", got, first)
	}
}

func TestClaimTask_HeldByAnotherHolder(t *testing.T) {
	db := newSafeMockTaskStore()
	tm := claimFixture(t, db)
	ctx := context.Background()
	if err := tm.ClaimTask(ctx, claimTaskID, holderA); err != nil {
		t.Fatal(err)
	}

	err := tm.ClaimTask(ctx, claimTaskID, holderB)

	if !errors.Is(err, ErrClaimHeld) {
		t.Fatalf("err = %v, want ErrClaimHeld", err)
	}
	var held *ClaimHeldError
	if !errors.As(err, &held) || held.Holder != holderA || held.ClaimedAt == nil {
		t.Fatalf("err = %#v, want a *ClaimHeldError naming %q with its claim time", err, holderA)
	}
	if got := claimRow(t, db).ClaimedBy; got != holderA {
		t.Errorf("ClaimedBy = %q, want %q unchanged", got, holderA)
	}
}

// A completed task cannot be claimed, even by the holder of a claim kept from before it
// completed.
func TestClaimTask_CompletedTask(t *testing.T) {
	db := newSafeMockTaskStore()
	tm := claimFixture(t, db)
	ctx := context.Background()
	if err := tm.ClaimTask(ctx, claimTaskID, holderA); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CompleteTask(ctx, claimTaskID, 1); err != nil {
		t.Fatal(err)
	}

	for _, holder := range []string{holderA, holderB} {
		if err := tm.ClaimTask(ctx, claimTaskID, holder); !errors.Is(err, ErrTaskCompleted) {
			t.Errorf("ClaimTask(%q) err = %v, want ErrTaskCompleted", holder, err)
		}
	}
	if got := claimRow(t, db).ClaimedBy; got != holderA {
		t.Errorf("ClaimedBy = %q, want the kept claim %q", got, holderA)
	}
}

func TestReleaseTask(t *testing.T) {
	db := newSafeMockTaskStore()
	tm := claimFixture(t, db)
	ctx := context.Background()
	if err := tm.ClaimTask(ctx, claimTaskID, holderA); err != nil {
		t.Fatal(err)
	}

	if err := tm.ReleaseTask(ctx, claimTaskID, holderB); !errors.Is(err, ErrNotClaimHolder) {
		t.Fatalf("release by another holder: err = %v, want ErrNotClaimHolder", err)
	}
	if got := claimRow(t, db).ClaimedBy; got != holderA {
		t.Fatalf("ClaimedBy = %q, want %q unchanged", got, holderA)
	}

	if err := tm.ReleaseTask(ctx, claimTaskID, holderA); err != nil {
		t.Fatalf("release by the holder: %v", err)
	}
	if row := claimRow(t, db); row.ClaimedBy != "" || row.ClaimedAt != nil {
		t.Fatalf("claim = (%q, %v), want cleared", row.ClaimedBy, row.ClaimedAt)
	}

	// Releasing an unclaimed task is harmless, so a repeated release succeeds.
	if err := tm.ReleaseTask(ctx, claimTaskID, holderA); err != nil {
		t.Errorf("repeat release: %v", err)
	}
	if err := tm.ReleaseTask(ctx, claimTaskID, holderB); err != nil {
		t.Errorf("release of an unclaimed task: %v", err)
	}
}

func TestReleaseTask_CompletedTask(t *testing.T) {
	db := newSafeMockTaskStore()
	tm := claimFixture(t, db)
	ctx := context.Background()
	if err := tm.ClaimTask(ctx, claimTaskID, holderA); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CompleteTask(ctx, claimTaskID, 1); err != nil {
		t.Fatal(err)
	}

	if err := tm.ReleaseTask(ctx, claimTaskID, holderA); err != nil {
		t.Fatalf("ReleaseTask: %v", err)
	}
	if got := claimRow(t, db).ClaimedBy; got != "" {
		t.Errorf("ClaimedBy = %q, want cleared", got)
	}
}

func TestClaimAndRelease_RequireAHolder(t *testing.T) {
	db := newSafeMockTaskStore()
	tm := claimFixture(t, db)
	ctx := context.Background()

	if err := tm.ClaimTask(ctx, claimTaskID, ""); !errors.Is(err, ErrHolderRequired) {
		t.Errorf("ClaimTask err = %v, want ErrHolderRequired", err)
	}
	if err := tm.ReleaseTask(ctx, claimTaskID, ""); !errors.Is(err, ErrHolderRequired) {
		t.Errorf("ReleaseTask err = %v, want ErrHolderRequired", err)
	}
	if got := claimRow(t, db).ClaimedBy; got != "" {
		t.Errorf("ClaimedBy = %q, want unclaimed", got)
	}
}

func TestClaimAndRelease_UnknownTask(t *testing.T) {
	tm := claimFixture(t, newSafeMockTaskStore())
	ctx := context.Background()

	if err := tm.ClaimTask(ctx, "nope", holderA); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("ClaimTask err = %v, want ErrTaskNotFound", err)
	}
	if err := tm.ReleaseTask(ctx, "nope", holderA); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("ReleaseTask err = %v, want ErrTaskNotFound", err)
	}
}

// staleClaimStore drops the first `misses` claim and release writes as if their guard had not
// matched, while the row is in fact claimable: what the manager sees when the claim changes
// between its write and the read that explains a miss.
type staleClaimStore struct {
	*safeMockTaskStore
	misses int
}

func (s *staleClaimStore) ClaimTask(ctx context.Context, taskID, holder string, at time.Time) (int64, error) {
	if s.misses > 0 {
		s.misses--
		return 0, nil
	}
	return s.safeMockTaskStore.ClaimTask(ctx, taskID, holder, at)
}

func (s *staleClaimStore) ReleaseTask(ctx context.Context, taskID, holder string) (int64, error) {
	if s.misses > 0 {
		s.misses--
		return 0, nil
	}
	return s.safeMockTaskStore.ReleaseTask(ctx, taskID, holder)
}

func TestClaimAndRelease_RetryWhenTheClaimChangesUnderThem(t *testing.T) {
	ctx := context.Background()

	db := &staleClaimStore{safeMockTaskStore: newSafeMockTaskStore()}
	tm := claimFixture(t, db)
	db.misses = claimAttempts - 1
	if err := tm.ClaimTask(ctx, claimTaskID, holderA); err != nil {
		t.Fatalf("ClaimTask after %d misses: %v", claimAttempts-1, err)
	}
	db.misses = claimAttempts - 1
	if err := tm.ReleaseTask(ctx, claimTaskID, holderA); err != nil {
		t.Fatalf("ReleaseTask after %d misses: %v", claimAttempts-1, err)
	}

	// With holderA holding the claim, a miss is unexplained for both: holderA may release it and
	// may claim it again. After claimAttempts misses they give up with an error.
	if err := tm.ClaimTask(ctx, claimTaskID, holderA); err != nil {
		t.Fatal(err)
	}
	db.misses = claimAttempts
	if err := tm.ReleaseTask(ctx, claimTaskID, holderA); err == nil {
		t.Error("ReleaseTask: want an error after every attempt missed")
	}
	db.misses = claimAttempts
	if err := tm.ClaimTask(ctx, claimTaskID, holderA); err == nil {
		t.Error("ClaimTask: want an error after every attempt missed")
	}
}

func TestErrTaskNotFound_FromTaskReads(t *testing.T) {
	tm := claimFixture(t, newSafeMockTaskStore())
	ctx := context.Background()

	if _, err := tm.GetTaskRenderInfo(ctx, "nope"); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("GetTaskRenderInfo err = %v, want ErrTaskNotFound", err)
	}
	if err := tm.CompleteTaskStep(ctx, "nope", stepA, map[string]any{}); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("CompleteTaskStep err = %v, want ErrTaskNotFound", err)
	}
}
