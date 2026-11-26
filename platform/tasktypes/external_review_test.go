// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package tasktypes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/OpenNSW/core/remote"
	"github.com/OpenNSW/core/taskflow/callbacktoken"
	"github.com/OpenNSW/core/taskflow/plugins"
	"github.com/OpenNSW/core/taskflow/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	reviewTaskID = "0b6f3f6e-3c1a-4c55-9a1e-6d1f2f3a4b5c"
	reviewStepID = "5a1d7c2e-8f4b-4e3a-b6c9-0d2e1f3a4b5c"
)

// receivedCall is one request the fake review service saw.
type receivedCall struct {
	path string
	body map[string]any
}

// reviewService records the calls a fake review service received. The handler runs
// on the server's goroutines, so received is guarded by mu.
type reviewService struct {
	mu       sync.Mutex
	received []receivedCall
}

// calls returns a copy of the calls received so far.
func (s *reviewService) calls() []receivedCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]receivedCall(nil), s.received...)
}

// newReviewService starts a fake review service answering every call with status
// and returns a remote.Manager that knows it as "agency", plus the service.
func newReviewService(t *testing.T, status int) (*remote.Manager, *reviewService) {
	t.Helper()
	svc := &reviewService{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// assert, not require: FailNow must not be called off the test's goroutine,
		// and the client still needs a response.
		var body map[string]any
		raw, err := io.ReadAll(r.Body)
		if !assert.NoError(t, err) || !assert.NoError(t, json.Unmarshal(raw, &body)) {
			http.Error(w, "bad request body", http.StatusBadRequest)
			return
		}
		svc.mu.Lock()
		svc.received = append(svc.received, receivedCall{path: r.URL.EscapedPath(), body: body})
		svc.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	mgr := remote.NewManager()
	require.NoError(t, mgr.SetServices(remote.ServiceConfig{ID: "agency", URL: srv.URL, Timeout: "5s"}))
	return mgr, svc
}

func reviewContext(inputs map[string]any) plugins.PluginContext {
	return plugins.PluginContext{
		Context: context.Background(),
		Record:  &store.TaskRecord{TaskID: reviewTaskID, ActiveStepID: reviewStepID, CaseID: "case-1"},
		Inputs:  inputs,
	}
}

func ownToken(t *testing.T) string {
	t.Helper()
	token, err := callbacktoken.Encode(reviewTaskID, reviewStepID)
	require.NoError(t, err)
	return token
}

func TestExternalReview_DispatchesWithoutReplyToken(t *testing.T) {
	mgr, svc := newReviewService(t, http.StatusOK)
	p := NewExternalReviewPlugin(mgr, tokenFor, noCaseAttributes{})
	ctx := reviewContext(map[string]any{"submission": map[string]any{"field": "v1"}})

	err := p.Execute(ctx, json.RawMessage(`{"service_id": "agency", "path": "/api/v1/internal/cases/{caseId}/flows", "service_workflow_id": "review"}`))

	require.ErrorIs(t, err, ErrSuspended)
	assert.Equal(t, "QUEUED_EXTERNALLY", ctx.Record.State)
	calls := svc.calls()
	require.Len(t, calls, 1)
	call := calls[0]
	// The other side starts its review flow on the same case.
	assert.Equal(t, "/api/v1/internal/cases/case-1/flows", call.path)
	assert.Equal(t, map[string]any{
		"workflowId":    "review",
		"flowId":        reviewTaskID,
		"callbackToken": ownToken(t),
		"input":         map[string]any{"field": "v1"},
	}, call.body)
}

// A dispatch needs its path, the workflow to start and the task's case; without one it
// fails before calling out.
func TestExternalReview_DispatchNeeds(t *testing.T) {
	for name, tc := range map[string]struct {
		config string
		caseID string
	}{
		"path":                {`{"service_id": "agency", "service_workflow_id": "review"}`, "case-1"},
		"service_workflow_id": {`{"service_id": "agency", "path": "/api/v1/internal/cases/{caseId}/flows"}`, "case-1"},
		"a case":              {`{"service_id": "agency", "path": "/api/v1/internal/cases/{caseId}/flows", "service_workflow_id": "review"}`, ""},
	} {
		t.Run(name, func(t *testing.T) {
			mgr, svc := newReviewService(t, http.StatusOK)
			p := NewExternalReviewPlugin(mgr, tokenFor, noCaseAttributes{})
			ctx := reviewContext(map[string]any{"submission": map[string]any{}})
			ctx.Record.CaseID = tc.caseID

			err := p.Execute(ctx, json.RawMessage(tc.config))

			require.Error(t, err)
			assert.NotErrorIs(t, err, ErrSuspended)
			assert.Empty(t, svc.calls())
		})
	}
}

// The case ID is escaped into its path segment.
func TestExternalReview_EscapesTheCaseID(t *testing.T) {
	mgr, svc := newReviewService(t, http.StatusOK)
	p := NewExternalReviewPlugin(mgr, tokenFor, noCaseAttributes{})
	ctx := reviewContext(map[string]any{"submission": map[string]any{}})
	ctx.Record.CaseID = "a/b"

	err := p.Execute(ctx, json.RawMessage(`{"service_id": "agency", "path": "/api/v1/internal/cases/{caseId}/flows", "service_workflow_id": "review"}`))

	require.ErrorIs(t, err, ErrSuspended)
	calls := svc.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "/api/v1/internal/cases/a%2Fb/flows", calls[0].path)
}

// caseAttributesStub answers CaseAttributes with attrs or err, and records the
// case IDs it reads.
type caseAttributesStub struct {
	attrs map[string]any
	err   error
	reads []string
}

func (s *caseAttributesStub) CaseAttributes(_ context.Context, caseID string) (map[string]any, error) {
	s.reads = append(s.reads, caseID)
	return s.attrs, s.err
}

// noCaseAttributes is the reader for tests that don't look at case attributes: no case
// has any.
type noCaseAttributes struct{}

func (noCaseAttributes) CaseAttributes(context.Context, string) (map[string]any, error) {
	return nil, nil
}

const dispatchConfig = `{"service_id": "agency", "path": "/api/v1/internal/cases/{caseId}/flows", "service_workflow_id": "review", "reply_command": "submit"}`

func TestExternalReview_DispatchSendsTheCaseAttributes(t *testing.T) {
	mgr, svc := newReviewService(t, http.StatusOK)
	cases := &caseAttributesStub{attrs: map[string]any{"company": map[string]any{"name": "Adam PVT LTD"}}}
	p := NewExternalReviewPlugin(mgr, tokenFor, cases)

	err := p.Execute(reviewContext(map[string]any{"submission": map[string]any{"field": "v1"}}), json.RawMessage(dispatchConfig))

	require.ErrorIs(t, err, ErrSuspended)
	assert.Equal(t, []string{"case-1"}, cases.reads)
	calls := svc.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, map[string]any{"company": map[string]any{"name": "Adam PVT LTD"}}, calls[0].body["attributes"])
	// The input is the node's, unchanged.
	assert.Equal(t, map[string]any{"field": "v1"}, calls[0].body["input"])
}

func TestExternalReview_DispatchWithoutCaseAttributes(t *testing.T) {
	mgr, svc := newReviewService(t, http.StatusOK)
	p := NewExternalReviewPlugin(mgr, tokenFor, &caseAttributesStub{attrs: map[string]any{}})

	err := p.Execute(reviewContext(map[string]any{"submission": map[string]any{}}), json.RawMessage(dispatchConfig))

	require.ErrorIs(t, err, ErrSuspended)
	calls := svc.calls()
	require.Len(t, calls, 1)
	assert.NotContains(t, calls[0].body, "attributes")
}

// A failed read fails the step before it calls out, and the step is retried as for a
// failed POST: it is not a permanent error.
func TestExternalReview_FailedCaseAttributesReadIsRetried(t *testing.T) {
	mgr, svc := newReviewService(t, http.StatusOK)
	p := NewExternalReviewPlugin(mgr, tokenFor, &caseAttributesStub{err: fmt.Errorf("db down")})

	err := p.Execute(reviewContext(map[string]any{"submission": map[string]any{}}), json.RawMessage(dispatchConfig))

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrSuspended)
	assert.False(t, plugins.IsPermanent(err))
	assert.Empty(t, svc.calls())
}

func TestExternalReview_ReplySendsNoCaseAttributes(t *testing.T) {
	mgr, svc := newReviewService(t, http.StatusOK)
	cases := &caseAttributesStub{attrs: map[string]any{"company": map[string]any{"name": "Adam PVT LTD"}}}
	p := NewExternalReviewPlugin(mgr, tokenFor, cases)

	err := p.Execute(reviewContext(map[string]any{"replyToken": "tok_x", "submission": map[string]any{}}), json.RawMessage(dispatchConfig))

	require.ErrorIs(t, err, ErrSuspended)
	assert.Empty(t, cases.reads)
	calls := svc.calls()
	require.Len(t, calls, 1)
	assert.NotContains(t, calls[0].body, "attributes")
}

func TestExternalReview_EmptyReplyTokenDispatches(t *testing.T) {
	mgr, svc := newReviewService(t, http.StatusOK)
	p := NewExternalReviewPlugin(mgr, tokenFor, noCaseAttributes{})
	ctx := reviewContext(map[string]any{"replyToken": "", "submission": map[string]any{}})

	err := p.Execute(ctx, json.RawMessage(`{"service_id": "agency", "path": "/api/v1/internal/cases/{caseId}/flows", "service_workflow_id": "review", "reply_command": "submit"}`))

	require.ErrorIs(t, err, ErrSuspended)
	calls := svc.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "/api/v1/internal/cases/case-1/flows", calls[0].path)
}

func TestExternalReview_RepliesOnReplyToken(t *testing.T) {
	mgr, svc := newReviewService(t, http.StatusOK)
	p := NewExternalReviewPlugin(mgr, tokenFor, noCaseAttributes{})
	ctx := reviewContext(map[string]any{
		"replyToken": "tok_x",
		"submission": map[string]any{"field": "v2"},
	})

	// No path: a reply always goes to the callbacks route.
	err := p.Execute(ctx, json.RawMessage(`{"service_id": "agency", "reply_command": "submit"}`))

	require.ErrorIs(t, err, ErrSuspended)
	assert.Equal(t, "QUEUED_EXTERNALLY", ctx.Record.State)
	calls := svc.calls()
	require.Len(t, calls, 1)
	call := calls[0]
	assert.Equal(t, "/api/v1/callbacks/tok_x", call.path)
	assert.Equal(t, map[string]any{
		"command": "submit",
		"payload": map[string]any{
			"submission":    map[string]any{"field": "v2"},
			"callbackToken": ownToken(t),
		},
	}, call.body)
}

func TestExternalReview_ReplySendsOnlyPayloadInput(t *testing.T) {
	mgr, svc := newReviewService(t, http.StatusOK)
	p := NewExternalReviewPlugin(mgr, tokenFor, noCaseAttributes{})
	ctx := reviewContext(map[string]any{
		"replyToken": "tok_x",
		"payload":    map[string]any{"feedback": "attach the invoice"},
		"submission": map[string]any{"field": "v1"},
	})

	err := p.Execute(ctx, json.RawMessage(`{"service_id": "agency", "reply_command": "needs_more_info"}`))

	require.ErrorIs(t, err, ErrSuspended)
	calls := svc.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, map[string]any{
		"command": "needs_more_info",
		"payload": map[string]any{
			"feedback":      "attach the invoice",
			"callbackToken": ownToken(t),
		},
	}, calls[0].body)
	// The step keeps its inputs, for its page to show.
	assert.Equal(t, map[string]any{"feedback": "attach the invoice"}, ctx.Inputs["payload"])
	assert.Contains(t, ctx.Inputs, "submission")
}

func TestExternalReview_ReplyWithEmptyPayloadSendsOnlyCallbackToken(t *testing.T) {
	mgr, svc := newReviewService(t, http.StatusOK)
	p := NewExternalReviewPlugin(mgr, tokenFor, noCaseAttributes{})
	ctx := reviewContext(map[string]any{
		"replyToken": "tok_x",
		"payload":    map[string]any{},
		"submission": map[string]any{"field": "v1"},
	})

	err := p.Execute(ctx, json.RawMessage(`{"service_id": "agency", "reply_command": "needs_more_info"}`))

	require.ErrorIs(t, err, ErrSuspended)
	calls := svc.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, map[string]any{"callbackToken": ownToken(t)}, calls[0].body["payload"])
	assert.Equal(t, map[string]any{}, ctx.Inputs["payload"], "callbackToken is not written into the step's own input")
}

func TestExternalReview_ReplyTokenizesOnlyPayloadInput(t *testing.T) {
	mgr, svc := newReviewService(t, http.StatusOK)
	// tokenFor has no client for "agency": tokenizing the submission's invoice
	// would fail the reply, though the reply does not send it.
	p := NewExternalReviewPlugin(mgr, withRenderConfig(tokenFor, reviewRenderConfig), noCaseAttributes{})
	ctx := reviewContext(map[string]any{
		"replyToken": "tok_x",
		"payload":    map[string]any{"feedback": "attach the invoice"},
		"submission": map[string]any{"invoice": "k-invoice"},
	})

	err := p.Execute(ctx, json.RawMessage(`{"service_id": "agency", "reply_command": "needs_more_info"}`))

	require.ErrorIs(t, err, ErrSuspended)
	calls := svc.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, map[string]any{
		"feedback":      "attach the invoice",
		"callbackToken": ownToken(t),
	}, calls[0].body["payload"])
}

func TestExternalReview_ReplyToStaleStepIsNotRetried(t *testing.T) {
	mgr, _ := newReviewService(t, http.StatusConflict)
	p := NewExternalReviewPlugin(mgr, tokenFor, noCaseAttributes{})

	err := p.Execute(reviewContext(map[string]any{"replyToken": "tok_x"}),
		json.RawMessage(`{"service_id": "agency", "reply_command": "submit"}`))

	// Wrapped as core wraps a plugin error, so the orchestrator still finds it.
	err = fmt.Errorf("plugin execution failed: %w", err)
	errType, permanent := plugins.PermanentType(err)
	assert.True(t, permanent)
	assert.Equal(t, "StaleReplyToken", errType)
}

func TestExternalReview_ReplyPayloadNotAnObjectIsNotRetried(t *testing.T) {
	mgr, _ := newReviewService(t, http.StatusOK)
	p := NewExternalReviewPlugin(mgr, tokenFor, noCaseAttributes{})

	err := p.Execute(reviewContext(map[string]any{"replyToken": "tok_x", "payload": "x"}),
		json.RawMessage(`{"service_id": "agency", "reply_command": "submit"}`))

	err = fmt.Errorf("plugin execution failed: %w", err)
	errType, permanent := plugins.PermanentType(err)
	assert.True(t, permanent)
	assert.Equal(t, "InvalidReplyPayload", errType)
}

func TestExternalReview_FailedReplyIsRetried(t *testing.T) {
	mgr, _ := newReviewService(t, http.StatusServiceUnavailable)
	p := NewExternalReviewPlugin(mgr, tokenFor, noCaseAttributes{})

	err := p.Execute(reviewContext(map[string]any{"replyToken": "tok_x"}),
		json.RawMessage(`{"service_id": "agency", "reply_command": "submit"}`))

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrSuspended)
	assert.False(t, plugins.IsPermanent(err), "a 503 may clear, so the step is retried")
}

// A dispatch the agency refuses with a 4xx can't succeed on a retry, so the step
// parks at once.
func TestExternalReview_RefusedDispatchIsNotRetried(t *testing.T) {
	mgr, _ := newReviewService(t, http.StatusBadRequest)
	p := NewExternalReviewPlugin(mgr, tokenFor, noCaseAttributes{})

	err := p.Execute(reviewContext(map[string]any{}),
		json.RawMessage(`{"service_id": "agency", "path": "/api/v1/internal/cases/{caseId}/flows", "service_workflow_id": "review"}`))

	errType, permanent := plugins.PermanentType(err)
	assert.True(t, permanent, "err = %v", err)
	assert.Equal(t, plugins.ErrorTypeRejected, errType)
}

func TestExternalReview_ConfigErrors(t *testing.T) {
	tests := []struct {
		name   string
		inputs map[string]any
		config string
		want   string
	}{
		{"dispatch without path", map[string]any{}, `{"service_id": "agency"}`, "path is required"},
		{"reply without command", map[string]any{"replyToken": "tok_x"}, `{"service_id": "agency"}`, "reply_command is required"},
		{"reply token not a string", map[string]any{"replyToken": 42}, `{"service_id": "agency", "reply_command": "submit"}`, "replyToken must be a string"},
		{"reply payload not an object", map[string]any{"replyToken": "tok_x", "payload": "x"}, `{"service_id": "agency", "reply_command": "submit"}`, "payload must be an object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr, svc := newReviewService(t, http.StatusOK)
			p := NewExternalReviewPlugin(mgr, tokenFor, noCaseAttributes{})

			err := p.Execute(reviewContext(tt.inputs), json.RawMessage(tt.config))

			require.ErrorContains(t, err, tt.want)
			assert.Empty(t, svc.calls())
		})
	}
}

func TestNewExternalReviewPlugin_PanicsOnMissingDependencies(t *testing.T) {
	issue := func(string, string) (string, error) { return "", nil }
	clientFor := func(string) (string, bool) { return "", false }
	renderConfig := func(context.Context, store.TaskRecord) (json.RawMessage, error) { return nil, nil }

	tests := map[string]func(){
		"nil manager": func() { NewExternalReviewPlugin(nil, FileTokens{}, noCaseAttributes{}) },
		"nil cases":   func() { NewExternalReviewPlugin(remote.NewManager(), FileTokens{}, nil) },
		"issuer without client lookup": func() {
			NewExternalReviewPlugin(remote.NewManager(), FileTokens{IssueForClient: issue, RenderConfigOf: renderConfig}, noCaseAttributes{})
		},
		"client lookup without issuer": func() {
			NewExternalReviewPlugin(remote.NewManager(), FileTokens{ClientFor: clientFor}, noCaseAttributes{})
		},
		"issuer without render config": func() {
			NewExternalReviewPlugin(remote.NewManager(), FileTokens{ClientFor: clientFor, IssueForClient: issue}, noCaseAttributes{})
		},
	}
	for name, build := range tests {
		t.Run(name, func(t *testing.T) { assert.Panics(t, build) })
	}
}

func TestNewExternalReviewPlugin_AcceptsZeroOrCompleteFiles(t *testing.T) {
	assert.NotPanics(t, func() { NewExternalReviewPlugin(remote.NewManager(), FileTokens{}, noCaseAttributes{}) })
	assert.NotPanics(t, func() {
		NewExternalReviewPlugin(remote.NewManager(), withRenderConfig(tokenFor, reviewRenderConfig), noCaseAttributes{})
	})
}
