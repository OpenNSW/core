// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package tasktypes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"strings"

	"github.com/OpenNSW/core/remote"
	coreplugins "github.com/OpenNSW/core/taskflow/plugins"
	"github.com/OpenNSW/core/taskflow/store"
)

// ExternalReviewPlugin starts a review flow on the reviewing service's side, on
// the same case, and parks until the reviewer calls back.
type ExternalReviewPlugin struct {
	client *dispatchHelper
	files  FileTokens
	cases  CaseAttributes
}

// CaseAttributes reads a case's attributes (cases.attributes), which a dispatch sends
// with the flow it starts.
type CaseAttributes interface {
	CaseAttributes(ctx context.Context, caseID string) (map[string]any, error)
}

// NewExternalReviewPlugin builds a plugin that POSTs the applicant's submitted
// form to the configured service+path, starting the other side's review flow.
// The files in it are sent as tokens for the reviewer, issued through files, and
// the attributes of the dispatch's case, read from cases, are sent with each flow
// it starts.
// manager and cases must be non-nil, and files either zero or complete.
func NewExternalReviewPlugin(manager *remote.Manager, files FileTokens, cases CaseAttributes) *ExternalReviewPlugin {
	if manager == nil {
		panic("manager is nil")
	}
	if cases == nil {
		panic("cases is nil")
	}
	if (files.ClientFor == nil) != (files.IssueForClient == nil) {
		panic("files needs both ClientFor and IssueForClient, or neither")
	}
	if files.IssueForClient != nil && files.RenderConfigOf == nil {
		panic("files needs RenderConfigOf with IssueForClient")
	}
	return &ExternalReviewPlugin{client: newDispatchHelper(manager), files: files, cases: cases}
}

type externalReviewConfig struct {
	ServiceID string `json:"service_id"`
	// Path is where a dispatch is POSTed: the other side's client flows route,
	// /api/v1/internal/cases/{caseId}/flows, with {caseId} filled in from the task's
	// case. A reply ignores it: it always calls back on callbacksPath/{replyToken}.
	Path string `json:"path,omitempty"`
	// ServiceWorkflowID is the workflow the other side starts for the review, as its
	// flows.clients lists it. Required only to dispatch.
	ServiceWorkflowID string `json:"service_workflow_id,omitempty"`
	// ReplyCommand is the command a reply carries, e.g. "submit" for an applicant's
	// resubmission or "needs_more_info" for an officer asking for one. Required
	// only to reply.
	ReplyCommand string `json:"reply_command,omitempty"`
}

// replyTokenInput is the reserved input that makes the step reply instead of
// dispatch: the token the other side parked its own step on.
const replyTokenInput = "replyToken"

// replyPayloadInput is the reserved input that, when present, is all a reply sends.
const replyPayloadInput = "payload"

// casePathParam is the placeholder in a dispatch path that the task's case ID fills.
const casePathParam = "{caseId}"

// Execute marks the step QUEUED_EXTERNALLY, then POSTs the submission to the other
// side's client flows route, which starts its review flow on the same case.
//
// Given a replyToken input, it replies instead: the other side parked a step
// and handed over that token, so the step calls back on it (see replyRequest)
// rather than dispatching a new review the other side would take for a retry.
//
// A failed call fails the step and the engine retries it. A 409 on a reply is the
// exception (see isConflict).
func (p *ExternalReviewPlugin) Execute(ctx pluginContext, configRaw json.RawMessage) error {
	var cfg externalReviewConfig
	if err := json.Unmarshal(configRaw, &cfg); err != nil {
		return configError(fmt.Errorf("external_review: invalid config: %w", err))
	}
	if cfg.ServiceID == "" {
		return configError(fmt.Errorf("external_review: service_id is required"))
	}
	replyToken, err := replyTokenFrom(ctx.Inputs)
	if err != nil {
		return err
	}
	callbackToken, err := coreplugins.CallbackToken(ctx.Record)
	if err != nil {
		return inputError(fmt.Errorf("external_review: %w", err))
	}

	var (
		path string
		body map[string]any
	)
	if replyToken != "" {
		path, body, err = p.reply(ctx, cfg, replyToken, callbackToken)
	} else {
		path, body, err = p.dispatchRequest(ctx, cfg, callbackToken)
	}
	if err != nil {
		return err
	}

	ctx.Record.State = "QUEUED_EXTERNALLY"
	slog.Info("taskv2 external_review: calling out",
		"taskId", ctx.Record.TaskID, "serviceId", cfg.ServiceID, "reply", replyToken != "")

	err = p.client.post(ctx.Context, cfg.ServiceID, path, body)
	if replyToken != "" && isConflict(err) {
		return coreplugins.Permanent("StaleReplyToken",
			fmt.Errorf("external_review: %s rejected the reply: its step is no longer waiting: %w", cfg.ServiceID, err))
	}
	if err != nil {
		return classifyRemote(err)
	}
	return ErrSuspended
}

// dispatchRequest is the first round: the review flow the other side starts, POSTed
// to the configured path with the task's case in it, and the case's attributes when it
// has any. A failed read of them fails the step, which is retried like a failed POST.
func (p *ExternalReviewPlugin) dispatchRequest(ctx pluginContext, cfg externalReviewConfig, callbackToken string) (string, map[string]any, error) {
	if cfg.Path == "" {
		return "", nil, configError(fmt.Errorf("external_review: path is required"))
	}
	if cfg.ServiceWorkflowID == "" {
		return "", nil, fmt.Errorf("external_review: service_workflow_id is required")
	}
	if ctx.Record.CaseID == "" {
		return "", nil, fmt.Errorf("external_review: the task has no case")
	}
	path := strings.ReplaceAll(cfg.Path, casePathParam, url.PathEscape(ctx.Record.CaseID))

	// Convention: if input_mapping placed a value under the reserved key
	// "submission", that value is the wire shape the reviewer sees — write any
	// additional context directly into a nested "submission.<field>" path
	// in the node's own input_mapping (input_mapping's destination side
	// supports dot-paths natively). Otherwise the whole inputs bag is sent
	// (default fallback for simple cases).
	var data any = ctx.Inputs
	root := ""
	if submission, ok := ctx.Inputs["submission"]; ok {
		data, root = submission, "submission"
	}
	// The task's declared file fields are rooted at its data, which holds the
	// inputs; the reviewer gets tokens for the ones it is sent.
	data, err := p.files.tokenize(ctx.Context, *ctx.Record, root, data, cfg.ServiceID)
	if err != nil {
		return "", nil, fmt.Errorf("external_review: %w", err)
	}
	body := startFlowBody(ctx.Record, cfg.ServiceWorkflowID, data, callbackToken)
	attrs, err := p.cases.CaseAttributes(ctx.Context, ctx.Record.CaseID)
	if err != nil {
		return "", nil, fmt.Errorf("external_review: failed to read the attributes of case %s: %w", ctx.Record.CaseID, err)
	}
	if len(attrs) > 0 {
		body["attributes"] = attrs
	}
	return path, body, nil
}

// reply is replyRequest for the step's inputs with their declared files sent as
// tokens for the other side's client, as a dispatch sends them. The inputs are
// the task's data, so the declared paths apply as they are.
func (p *ExternalReviewPlugin) reply(ctx pluginContext, cfg externalReviewConfig, replyToken, callbackToken string) (string, map[string]any, error) {
	// Only what the reply sends gets tokens, as a dispatch tokenizes only its
	// submission: the payload input when there is one, else all the inputs.
	root, data := "", any(ctx.Inputs)
	if payload, ok := ctx.Inputs[replyPayloadInput]; ok {
		if _, ok := payload.(map[string]any); !ok {
			// The inputs are the same on every retry, so none can succeed.
			return "", nil, coreplugins.Permanent("InvalidReplyPayload",
				fmt.Errorf("external_review: %s must be an object, got %T", replyPayloadInput, payload))
		}
		root, data = replyPayloadInput, payload
	}
	sent, err := p.files.tokenize(ctx.Context, *ctx.Record, root, data, cfg.ServiceID)
	if err != nil {
		return "", nil, fmt.Errorf("external_review: %w", err)
	}
	// tokenize can return data itself, which is the step's own, so the reply
	// gets a copy. All the inputs are non-nil here: they hold the replyToken.
	payload := maps.Clone(sent.(map[string]any))
	if root == "" {
		delete(payload, replyTokenInput)
	}
	return replyRequest(cfg, payload, replyToken, callbackToken)
}

// replyRequest is a callback on the other side's parked step: {command, payload} on
// replyToken, the envelope every callback takes, with this step's own token added to
// payload as callbackToken for the other side to answer on. The payload is the
// step's "payload" input when it has one, so that its other inputs are only for its
// own page to show, and all of its inputs but replyToken otherwise. The receiving
// step's output_mapping reads them by the names this node gave them.
func replyRequest(cfg externalReviewConfig, payload map[string]any, replyToken, callbackToken string) (string, map[string]any, error) {
	if cfg.ReplyCommand == "" {
		return "", nil, configError(fmt.Errorf("external_review: reply_command is required to reply"))
	}
	payload["callbackToken"] = callbackToken
	return callbacksPath + "/" + url.PathEscape(replyToken),
		map[string]any{"command": cfg.ReplyCommand, "payload": payload}, nil
}

// isConflict reports whether err is a 409: the token's step is no longer waiting, so
// no retry can succeed and parking here would wait for an answer that can never
// come. The step fails for good, which parks it for an admin.
func isConflict(err error) bool {
	var re *remote.RemoteError
	return errors.As(err, &re) && re.StatusCode == http.StatusConflict
}

// replyTokenFrom returns the replyToken input, or "" when there is none. An empty
// value counts as none, so a workflow can clear the variable it maps from.
func replyTokenFrom(inputs map[string]any) (string, error) {
	v, ok := inputs[replyTokenInput]
	if !ok || v == nil {
		return "", nil
	}
	token, ok := v.(string)
	if !ok {
		return "", inputError(fmt.Errorf("external_review: %s must be a string, got %T", replyTokenInput, v))
	}
	return token, nil
}

// startFlowBody is the body of the other side's client flows route: the workflow to
// start, this task's ID as the flow's, so a retried dispatch is recognised as the same
// flow, the data as its input, and callbackToken. callbackToken is opaque and names the
// step this dispatch is for; the reviewer calls back on it, so a late or repeated
// callback can't complete a later step.
//
// data carries only the values declared by the workflow node's input_mapping
// — not the full record state — so the external reviewer sees the explicit
// contract surface and nothing more.
func startFlowBody(record *store.TaskRecord, workflowID string, data any, callbackToken string) map[string]any {
	return map[string]any{
		"workflowId":    workflowID,
		"flowId":        record.TaskID,
		"input":         data,
		"callbackToken": callbackToken,
	}
}
