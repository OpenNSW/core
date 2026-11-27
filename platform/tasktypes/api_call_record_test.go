// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package tasktypes

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// flakyRecorder fails its first `failures` writes, then succeeds.
type flakyRecorder struct {
	failures int
	calls    int
}

func (r *flakyRecorder) RecordDispatch(context.Context, string, string, string, map[string]any) error {
	r.calls++
	if r.calls <= r.failures {
		return errors.New("database unavailable")
	}
	return nil
}

var noWait = []time.Duration{0, 0, 0}

func TestRecordDispatch_RetriesAFailedWrite(t *testing.T) {
	rec := &flakyRecorder{failures: 2}

	recordDispatch(context.Background(), rec, "task-1", "case-1", "wf-1", map[string]any{"edgeId": "edge-1"}, noWait)

	assert.Equal(t, 3, rec.calls, "the write is retried until it succeeds")
}

func TestRecordDispatch_StopsAfterTheLastRetry(t *testing.T) {
	rec := &flakyRecorder{failures: 10}

	recordDispatch(context.Background(), rec, "task-1", "case-1", "wf-1", nil, noWait)

	assert.Equal(t, 1+len(noWait), rec.calls, "one attempt, then one retry per delay")
}

func TestRecordDispatch_StopsWhenTheStepIsCancelled(t *testing.T) {
	rec := &flakyRecorder{failures: 10}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	recordDispatch(ctx, rec, "task-1", "case-1", "wf-1", nil, []time.Duration{time.Hour})

	assert.Equal(t, 1, rec.calls, "a cancelled step does not wait out the backoff")
}

func TestNewAPICallPlugin_NilManagerPanics(t *testing.T) {
	assert.Panics(t, func() { NewAPICallPlugin(nil) })
	assert.Panics(t, func() { NewAPICallPluginWithInterpreter(nil, passthroughInterpreter{}) })
}
