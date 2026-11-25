// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package tasktypes

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OpenNSW/core/remote"
	"github.com/OpenNSW/core/taskflow/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OpenNSW/nsw-srilanka/internal/tasks/filefields"
)

// tokenFor stands in for file tokens: "<client>/<value>".
var tokenFor = FileTokens{
	ClientFor: func(serviceID string) (string, bool) {
		clientID, ok := map[string]string{"reviewer": "REVIEWER_CLIENT"}[serviceID]
		return clientID, ok
	},
	IssueForClient: func(clientID, value string) (string, error) {
		return clientID + "/" + value, nil
	},
	// A task that declares no files; withRenderConfig gives it some.
	RenderConfigOf: fixedRenderConfig(`{}`),
}

// fixedRenderConfig resolves every task's render config to renderConfig.
func fixedRenderConfig(renderConfig string) func(context.Context, store.TaskRecord) (json.RawMessage, error) {
	return func(context.Context, store.TaskRecord) (json.RawMessage, error) {
		return json.RawMessage(renderConfig), nil
	}
}

// withRenderConfig is f resolving every task's render config to renderConfig.
func withRenderConfig(f FileTokens, renderConfig string) FileTokens {
	f.RenderConfigOf = fixedRenderConfig(renderConfig)
	return f
}

const reviewRenderConfig = `{
  "id": "x:render",
  "files": ["submission.invoice", "submission.documents[*].file", "review.certificate"]
}`

func reviewInputs(t *testing.T) map[string]any {
	t.Helper()
	var in map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{
		"submission": {
			"invoice": "k-invoice",
			"documents": [{"file": "k-doc", "name": "packing list"}],
			"exporter": "ACME"
		}
	}`), &in))
	return in
}

func TestFileTokens_TokenizeSendsTokens(t *testing.T) {
	in := reviewInputs(t)

	sent, err := withRenderConfig(tokenFor, reviewRenderConfig).tokenize(context.Background(), store.TaskRecord{}, "submission", in["submission"], "reviewer")
	require.NoError(t, err)

	assert.Equal(t, map[string]any{
		"invoice":   "REVIEWER_CLIENT/k-invoice",
		"documents": []any{map[string]any{"file": "REVIEWER_CLIENT/k-doc", "name": "packing list"}},
		"exporter":  "ACME",
	}, sent)
	assert.Equal(t, reviewInputs(t), in, "what is sent must be a copy: the task keeps its stored values")
}

func TestFileTokens_TokenizeWholeInputs(t *testing.T) {
	in := reviewInputs(t)

	sent, err := withRenderConfig(tokenFor, reviewRenderConfig).tokenize(context.Background(), store.TaskRecord{}, "", in, "reviewer")
	require.NoError(t, err)
	assert.Equal(t, "REVIEWER_CLIENT/k-invoice", sent.(map[string]any)["submission"].(map[string]any)["invoice"])
}

// With no client to issue a token to, a file is not sent at all.
func TestFileTokens_TokenizeFailsClosedWithoutAClient(t *testing.T) {
	_, err := withRenderConfig(tokenFor, reviewRenderConfig).tokenize(context.Background(), store.TaskRecord{}, "submission", reviewInputs(t)["submission"], "efc")
	assert.ErrorContains(t, err, `"efc"`)
}

func TestFileTokens_TokenizeWithoutFilesToSend(t *testing.T) {
	in := reviewInputs(t)
	for name, renderConfig := range map[string]string{
		"no files declared":        `{"id":"x:render"}`,
		"none in what is sent":     `{"files":["review.certificate"]}`,
		"declared but not present": `{"files":["submission.missing"]}`,
	} {
		// "efc" has no client: nothing to issue means no client needed.
		sent, err := withRenderConfig(tokenFor, renderConfig).tokenize(context.Background(), store.TaskRecord{}, "submission", in["submission"], "efc")
		require.NoError(t, err, name)
		assert.Equal(t, in["submission"], sent, name)
	}
}

func TestFileTokens_TokenizeErrors(t *testing.T) {
	_, err := withRenderConfig(tokenFor, `{"files":["submission.docs[0]"]}`).tokenize(context.Background(), store.TaskRecord{}, "submission", map[string]any{}, "reviewer")
	assert.Error(t, err, "a bad declaration")

	failing := tokenFor
	failing.IssueForClient = func(string, string) (string, error) { return "", errors.New("no keyset") }
	_, err = withRenderConfig(failing, reviewRenderConfig).tokenize(context.Background(), store.TaskRecord{}, "submission", reviewInputs(t)["submission"], "reviewer")
	assert.ErrorContains(t, err, "no keyset")
}

// A declared file field of another shape than declared is not pushed as it
// is: the push fails, and nothing reaches the reviewer.
func TestExternalReview_DoesNotPushMismatchedFileFields(t *testing.T) {
	m, pushed := newReviewer(t)
	p := NewExternalReviewPlugin(m, withRenderConfig(tokenFor, reviewRenderConfig), noCaseAttributes{})
	in := reviewInputs(t)
	in["submission"].(map[string]any)["invoice"] = []any{"k-invoice", "k-other"}
	record := &store.TaskRecord{TaskID: uuid.NewString(), ActiveStepID: uuid.NewString(), CaseID: "case-1"}

	err := p.Execute(pluginContext{Context: context.Background(), Record: record, Inputs: in}, json.RawMessage(`{"service_id":"reviewer","path":"/review","service_workflow_id":"review"}`))

	require.ErrorIs(t, err, filefields.ErrShape)
	assert.Empty(t, *pushed)
}

// newReviewer stands up a reviewing service as the outbound service "reviewer",
// recording the body of each push.
func newReviewer(t *testing.T) (*remote.Manager, *[]map[string]any) {
	t.Helper()
	var pushed []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		pushed = append(pushed, body)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	m := remote.NewManager()
	require.NoError(t, m.SetServices(remote.ServiceConfig{ID: "reviewer", URL: srv.URL}))
	return m, &pushed
}

func TestExternalReview_PushesFileTokens(t *testing.T) {
	m, pushed := newReviewer(t)
	p := NewExternalReviewPlugin(m, withRenderConfig(tokenFor, reviewRenderConfig), noCaseAttributes{})
	in := reviewInputs(t)
	record := &store.TaskRecord{
		TaskID:       uuid.NewString(),
		ActiveStepID: uuid.NewString(),
		CaseID:       "case-1",
		Data:         reviewInputs(t),
	}

	err := p.Execute(pluginContext{Context: context.Background(), Record: record, Inputs: in}, json.RawMessage(`{"service_id":"reviewer","path":"/review","service_workflow_id":"review"}`))

	require.ErrorIs(t, err, ErrSuspended)
	require.Len(t, *pushed, 1)
	data := (*pushed)[0]["input"].(map[string]any)
	assert.Equal(t, "REVIEWER_CLIENT/k-invoice", data["invoice"])
	assert.Equal(t, reviewInputs(t), record.Data, "the task keeps its stored values")
	assert.Equal(t, reviewInputs(t), in)
}

func TestExternalReview_DoesNotPushFilesWithoutAClient(t *testing.T) {
	m, pushed := newReviewer(t)
	p := NewExternalReviewPlugin(m, FileTokens{
		RenderConfigOf: fixedRenderConfig(reviewRenderConfig),
		ClientFor:      func(string) (string, bool) { return "", false },
		IssueForClient: tokenFor.IssueForClient,
	}, noCaseAttributes{})
	record := &store.TaskRecord{TaskID: uuid.NewString(), ActiveStepID: uuid.NewString(), CaseID: "case-1"}

	err := p.Execute(pluginContext{Context: context.Background(), Record: record, Inputs: reviewInputs(t)}, json.RawMessage(`{"service_id":"reviewer","path":"/review","service_workflow_id":"review"}`))

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrSuspended)
	assert.Empty(t, *pushed)
}

// A reply sends the step's inputs back to the reviewer, so its declared files go as
// tokens for the reviewer's client too, and the task keeps its stored values.
func TestExternalReview_RepliesWithFileTokens(t *testing.T) {
	m, pushed := newReviewer(t)
	p := NewExternalReviewPlugin(m, withRenderConfig(tokenFor, reviewRenderConfig), noCaseAttributes{})
	in := reviewInputs(t)
	in["replyToken"] = "tok_x"
	record := &store.TaskRecord{TaskID: uuid.NewString(), ActiveStepID: uuid.NewString(), CaseID: "case-1"}

	err := p.Execute(pluginContext{Context: context.Background(), Record: record, Inputs: in}, json.RawMessage(`{"service_id":"reviewer","reply_command":"submit"}`))

	require.ErrorIs(t, err, ErrSuspended)
	require.Len(t, *pushed, 1)
	payload := (*pushed)[0]["payload"].(map[string]any)
	submission := payload["submission"].(map[string]any)
	assert.Equal(t, "REVIEWER_CLIENT/k-invoice", submission["invoice"])
	assert.Equal(t, "REVIEWER_CLIENT/k-doc", submission["documents"].([]any)[0].(map[string]any)["file"])
	assert.NotContains(t, payload, "replyToken")
	assert.Equal(t, "k-invoice", in["submission"].(map[string]any)["invoice"], "the task keeps its stored values")
}

// A reply with a payload input sends only that, so its declared files are the
// ones under "payload", and they go as tokens for the reviewer's client too.
func TestExternalReview_RepliesWithPayloadFileTokens(t *testing.T) {
	m, pushed := newReviewer(t)
	p := NewExternalReviewPlugin(m, withRenderConfig(tokenFor, `{"files": ["payload.invoice"]}`), noCaseAttributes{})
	in := map[string]any{
		"replyToken": "tok_x",
		"payload":    map[string]any{"invoice": "k-invoice", "feedback": "attach the invoice"},
	}
	record := &store.TaskRecord{TaskID: uuid.NewString(), ActiveStepID: uuid.NewString(), CaseID: "case-1"}

	err := p.Execute(pluginContext{Context: context.Background(), Record: record, Inputs: in}, json.RawMessage(`{"service_id":"reviewer","reply_command":"submit"}`))

	require.ErrorIs(t, err, ErrSuspended)
	require.Len(t, *pushed, 1)
	payload := (*pushed)[0]["payload"].(map[string]any)
	assert.Equal(t, "REVIEWER_CLIENT/k-invoice", payload["invoice"])
	assert.Equal(t, "attach the invoice", payload["feedback"])
	assert.Equal(t, "k-invoice", in["payload"].(map[string]any)["invoice"], "the task keeps its stored values")
}

// A reply that would send a file to a service with no client fails rather than
// send it.
func TestExternalReview_DoesNotReplyWithFilesWithoutAClient(t *testing.T) {
	m, pushed := newReviewer(t)
	p := NewExternalReviewPlugin(m, FileTokens{
		RenderConfigOf: fixedRenderConfig(reviewRenderConfig),
		ClientFor:      func(string) (string, bool) { return "", false },
		IssueForClient: tokenFor.IssueForClient,
	}, noCaseAttributes{})
	in := reviewInputs(t)
	in["replyToken"] = "tok_x"
	record := &store.TaskRecord{TaskID: uuid.NewString(), ActiveStepID: uuid.NewString(), CaseID: "case-1"}

	err := p.Execute(pluginContext{Context: context.Background(), Record: record, Inputs: in}, json.RawMessage(`{"service_id":"reviewer","reply_command":"submit"}`))

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrSuspended)
	assert.Empty(t, *pushed)
}

// A deployment that doesn't own its files stores the owner's tokens, which it
// sends back to the owner as they are, with no client of its own to issue for.
func TestFileTokens_ZeroSendsStoredValues(t *testing.T) {
	in := reviewInputs(t)
	sent, err := withRenderConfig(FileTokens{}, reviewRenderConfig).tokenize(context.Background(), store.TaskRecord{}, "submission", in["submission"], "origin")
	require.NoError(t, err)
	assert.Equal(t, reviewInputs(t)["submission"], sent)
}
