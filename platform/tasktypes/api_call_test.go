// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package tasktypes

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/OpenNSW/core/remote"
	"github.com/OpenNSW/core/taskflow/plugins"
	"github.com/OpenNSW/core/taskflow/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// calledService is a fake service that answers every call with one status,
// content type and body, and records what it received.
type calledService struct {
	mu       sync.Mutex
	paths    []string
	methods  []string
	received []any
}

func (s *calledService) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.paths)
}

// newCalledService starts a fake service and returns a remote.Manager that knows
// it as "svc", plus the service.
func newCalledService(t *testing.T, status int, contentType, body string) (*remote.Manager, *calledService) {
	t.Helper()
	svc := &calledService{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var sent any
		_ = json.Unmarshal(raw, &sent)
		svc.mu.Lock()
		svc.paths = append(svc.paths, r.URL.EscapedPath())
		svc.methods = append(svc.methods, r.Method)
		svc.received = append(svc.received, sent)
		svc.mu.Unlock()
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	mgr := remote.NewManager()
	require.NoError(t, mgr.SetServices(remote.ServiceConfig{ID: "svc", URL: srv.URL, Timeout: "5s"}))
	return mgr, svc
}

func apiCallContext(inputs map[string]any) plugins.PluginContext {
	return plugins.PluginContext{
		Context:         context.Background(),
		Record:          &store.TaskRecord{TaskID: "task-1", State: "PENDING_USER", Data: map[string]any{}},
		Inputs:          inputs,
		OutputNamespace: "result",
	}
}

const callbackConfig = `{"service_id": "svc", "path": "/callbacks/{token}"}`

var callbackInputs = map[string]any{
	"token":   "tok-1",
	"payload": map[string]any{"command": "approve", "payload": map[string]any{"note": "ok"}},
}

// A call the service accepts completes the step with its status and response,
// and an empty error, and sends the payload input as the body.
func TestAPICall_AcceptedCallCompletesTheStep(t *testing.T) {
	mgr, svc := newCalledService(t, http.StatusOK, "application/json", `{"id": "r-1"}`)
	ctx := apiCallContext(callbackInputs)

	err := NewAPICallPlugin(mgr).Execute(ctx, json.RawMessage(callbackConfig))

	require.NoError(t, err)
	assert.Equal(t, map[string]any{"status": 200, "response": map[string]any{"id": "r-1"}, "error": ""}, ctx.Record.Data["result"])
	assert.Equal(t, "QUEUED_EXTERNALLY", ctx.Record.State)
	require.Equal(t, 1, svc.calls())
	assert.Equal(t, "/callbacks/tok-1", svc.paths[0])
	assert.Equal(t, http.MethodPost, svc.methods[0])
	assert.Equal(t, callbackInputs["payload"], svc.received[0])
}

// A response with no body records no response, and one that isn't JSON records
// its text.
func TestAPICall_RecordsTheResponseAsItCame(t *testing.T) {
	for name, tc := range map[string]struct {
		status      int
		contentType string
		body        string
		want        map[string]any
	}{
		"no body":  {http.StatusNoContent, "", "", map[string]any{"status": 204, "error": ""}},
		"not JSON": {http.StatusOK, "text/plain", "done", map[string]any{"status": 200, "response": "done", "error": ""}},
	} {
		t.Run(name, func(t *testing.T) {
			mgr, _ := newCalledService(t, tc.status, tc.contentType, tc.body)
			ctx := apiCallContext(callbackInputs)

			require.NoError(t, NewAPICallPlugin(mgr).Execute(ctx, json.RawMessage(callbackConfig)))

			assert.Equal(t, tc.want, ctx.Record.Data["result"])
		})
	}
}

// Without a payload input, all the mapped inputs are sent.
func TestAPICall_SendsAllTheInputsWithoutAPayload(t *testing.T) {
	mgr, svc := newCalledService(t, http.StatusOK, "application/json", `{}`)
	inputs := map[string]any{"token": "tok-1", "note": "ok"}

	require.NoError(t, NewAPICallPlugin(mgr).Execute(apiCallContext(inputs), json.RawMessage(callbackConfig)))

	require.Equal(t, 1, svc.calls())
	assert.Equal(t, map[string]any{"token": "tok-1", "note": "ok"}, svc.received[0])
}

// A 409 means what the call addresses is no longer waiting for it, so the step
// completes as delivered, with no error.
func TestAPICall_ConflictCompletesAsDelivered(t *testing.T) {
	mgr, _ := newCalledService(t, http.StatusConflict, "application/json", `{"error": "the step is no longer waiting"}`)
	ctx := apiCallContext(callbackInputs)

	err := NewAPICallPlugin(mgr).Execute(ctx, json.RawMessage(callbackConfig))

	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"status":   409,
		"response": map[string]any{"error": "the step is no longer waiting"},
		"error":    "",
	}, ctx.Record.Data["result"])
}

// Any other refusal completes the step with an error for the workflow to route:
// the body's error or message field, otherwise the status.
func TestAPICall_RefusalCompletesWithAnError(t *testing.T) {
	for name, tc := range map[string]struct {
		status      int
		contentType string
		body        string
		wantError   string
	}{
		"error field":   {http.StatusBadRequest, "application/json", `{"error": "the submission does not match its form"}`, "the submission does not match its form"},
		"message field": {http.StatusUnprocessableEntity, "application/json", `{"message": "unknown command"}`, "unknown command"},
		"empty error":   {http.StatusBadRequest, "application/json", `{"error": "", "message": "bad command"}`, "bad command"},
		"no body":       {http.StatusNotFound, "", "", "404 Not Found"},
		"text body":     {http.StatusBadRequest, "text/plain", "nope", "400 Bad Request"},
	} {
		t.Run(name, func(t *testing.T) {
			mgr, _ := newCalledService(t, tc.status, tc.contentType, tc.body)
			ctx := apiCallContext(callbackInputs)

			err := NewAPICallPlugin(mgr).Execute(ctx, json.RawMessage(callbackConfig))

			require.NoError(t, err)
			out := ctx.Record.Data["result"].(map[string]any)
			assert.Equal(t, tc.status, out["status"])
			assert.Equal(t, tc.wantError, out["error"])
		})
	}
}

// 401 and 403 park the step: the credentials, or what the caller may do, are
// wrong, and retrying can't fix that.
func TestAPICall_UnauthorizedAndForbiddenPark(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			mgr, _ := newCalledService(t, status, "application/json", `{"error": "not allowed"}`)
			ctx := apiCallContext(callbackInputs)

			err := NewAPICallPlugin(mgr).Execute(ctx, json.RawMessage(callbackConfig))

			require.Error(t, err)
			errType, permanent := plugins.PermanentType(err)
			assert.True(t, permanent, "parks without retrying")
			assert.Equal(t, plugins.ErrorTypeRejected, errType)
			assert.Contains(t, err.Error(), "not allowed")
			assert.NotContains(t, ctx.Record.Data, "result")
		})
	}
}

// 408, 429 and 5xx fail the step so the engine retries it, and record nothing.
func TestAPICall_RetryableStatusFailsTheStep(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			mgr, _ := newCalledService(t, status, "", "")
			ctx := apiCallContext(callbackInputs)

			err := NewAPICallPlugin(mgr).Execute(ctx, json.RawMessage(callbackConfig))

			require.Error(t, err)
			_, permanent := plugins.PermanentType(err)
			assert.False(t, permanent, "the engine retries it")
			assert.NotContains(t, ctx.Record.Data, "result")
		})
	}
}

// A service that can't be reached fails the step so the engine retries it.
func TestAPICall_UnreachableServiceFailsTheStep(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	mgr := remote.NewManager()
	require.NoError(t, mgr.SetServices(remote.ServiceConfig{ID: "svc", URL: srv.URL, Timeout: "5s"}))
	ctx := apiCallContext(callbackInputs)

	err := NewAPICallPlugin(mgr).Execute(ctx, json.RawMessage(callbackConfig))

	require.Error(t, err)
	_, permanent := plugins.PermanentType(err)
	assert.False(t, permanent, "the engine retries it")
	assert.NotContains(t, ctx.Record.Data, "result")
}

// A service the manager doesn't list is a configuration error, so the step
// parks without calling anything.
func TestAPICall_UnknownServiceParks(t *testing.T) {
	mgr, svc := newCalledService(t, http.StatusOK, "application/json", `{}`)

	err := NewAPICallPlugin(mgr).Execute(apiCallContext(callbackInputs), json.RawMessage(`{"service_id": "other", "path": "/callbacks/{token}"}`))

	require.Error(t, err)
	errType, permanent := plugins.PermanentType(err)
	assert.True(t, permanent)
	assert.Equal(t, plugins.ErrorTypeConfig, errType)
	assert.Zero(t, svc.calls())
}

// A path placeholder with no input is an input error, and nothing is sent.
func TestAPICall_MissingPathInputParks(t *testing.T) {
	mgr, svc := newCalledService(t, http.StatusOK, "application/json", `{}`)

	err := NewAPICallPlugin(mgr).Execute(apiCallContext(map[string]any{"payload": map[string]any{}}), json.RawMessage(callbackConfig))

	require.Error(t, err)
	errType, permanent := plugins.PermanentType(err)
	assert.True(t, permanent)
	assert.Equal(t, plugins.ErrorTypeInput, errType)
	assert.Zero(t, svc.calls())
}

// retain_state keeps the step's state, method is honoured, and result_field
// records whether the call went through.
func TestAPICall_HonoursRetainStateMethodAndResultField(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   bool
	}{
		"accepted": {http.StatusOK, `{}`, true},
		"refused":  {http.StatusBadRequest, `{"error": "no"}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			mgr, svc := newCalledService(t, tc.status, "application/json", tc.body)
			ctx := apiCallContext(callbackInputs)

			err := NewAPICallPlugin(mgr).Execute(ctx, json.RawMessage(`{"service_id": "svc", "path": "/callbacks/{token}", "method": "put", "result_field": "sent", "retain_state": true}`))

			require.NoError(t, err)
			assert.Equal(t, "PENDING_USER", ctx.Record.State)
			assert.Equal(t, tc.want, ctx.Record.Data["result"].(map[string]any)["sent"])
			require.Equal(t, 1, svc.calls())
			assert.Equal(t, http.MethodPut, svc.methods[0])
		})
	}
}

// acceptAll is an interpreter that sends an empty body and reports a failed
// call as an error for the applicant.
type acceptAll struct{}

func (acceptAll) BuildRequest(map[string]any) remote.Body {
	return remote.JSONBody{V: map[string]any{}}
}

func (acceptAll) Interpret(callErr error, _ map[string]any) (bool, map[string]any) {
	if callErr != nil {
		return false, map[string]any{"error": "the service is unavailable"}
	}
	return true, map[string]any{}
}

// A plugin with an interpreter reports a failed call through it and completes
// the step, as it always has: only the plugin without one retries.
func TestAPICall_InterpreterStillReportsFailures(t *testing.T) {
	mgr, _ := newCalledService(t, http.StatusServiceUnavailable, "", "")
	ctx := apiCallContext(map[string]any{})

	err := NewAPICallPluginWithInterpreter(mgr, acceptAll{}).Execute(ctx, json.RawMessage(`{"service_id": "svc", "path": "/orders", "result_field": "accepted"}`))

	require.NoError(t, err)
	assert.Equal(t, map[string]any{"error": "the service is unavailable", "accepted": false}, ctx.Record.Data["result"])
}
