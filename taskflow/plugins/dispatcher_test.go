// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package plugins

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OpenNSW/core/taskflow/callbacktoken"
	"github.com/OpenNSW/core/taskflow/store"
)

const (
	testTaskID = "5f0c9b1e-3d2a-4c6b-8e7f-0a1b2c3d4e5f"
	testStepID = "9a8b7c6d-5e4f-4a3b-9c2d-1e0f9a8b7c6d"
)

func TestCallbackToken_NamesTheActiveStep(t *testing.T) {
	token, err := CallbackToken(&store.TaskRecord{TaskID: testTaskID, ActiveStepID: testStepID})
	if err != nil {
		t.Fatal(err)
	}
	task, step, err := callbacktoken.Decode(token)
	if err != nil || task != testTaskID || step != testStepID {
		t.Errorf("Decode(%q) = (%s, %s, %v)", token, task, step, err)
	}
}

// A record with no active step, or IDs that are not UUIDs, cannot be addressed by a callback, so
// the dispatch must not go out with a token that names nothing.
func TestCallbackToken_RejectsARecordThatCannotBeAddressed(t *testing.T) {
	for name, rec := range map[string]store.TaskRecord{
		"no active step": {TaskID: testTaskID},
		"task not UUID":  {TaskID: "task-1", ActiveStepID: testStepID},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := CallbackToken(&rec); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestDefaultHTTPDispatcher_SendsTheCallbackTokenAsXTaskID(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Task-ID")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	token, _ := callbacktoken.Encode(testTaskID, testStepID)
	if err := DefaultHTTPDispatcher(context.Background(), srv.URL, token, map[string]any{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if got != token {
		t.Errorf("X-Task-ID = %q, want the callback token %q", got, token)
	}
}
