// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package tasktypes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/OpenNSW/core/remote"
	flowplugins "github.com/OpenNSW/core/taskflow/plugins"
)

// Interpreter adapts a domain to the generic API-call plugin: it builds the
// request body from the task inputs and interprets the response into an
// acceptance flag plus fields to record into the output namespace.
type Interpreter interface {
	// BuildRequest returns the request body to POST, derived from the task's
	// mapped inputs — e.g. selecting a payload key and injecting identifiers.
	BuildRequest(inputs map[string]any) remote.Body

	// Interpret turns the call outcome into a result: whether the call was
	// accepted, plus the fields to record into the output namespace (e.g.
	// response identifiers, or an error message on rejection). callErr is the
	// transport/HTTP error (nil on success); resp is the decoded response body,
	// which the remote client populates even on a 4xx/5xx JSON error.
	Interpret(callErr error, resp map[string]any) (accepted bool, captured map[string]any)
}

// DispatchRecorder is implemented by interpreters whose service answers an
// accepted call later, by a callback, so the task that made the call has to be
// found again when the answer arrives. RecordDispatch runs once Interpret has
// accepted the call, with the task's ID, case and parent workflow and the
// fields Interpret captured.
//
// RecordDispatch must be safe to repeat, as an upsert on the call's own id is:
// a failed write is retried (see recordDispatch), and an error never fails the
// step, because the service has already accepted the call.
type DispatchRecorder interface {
	RecordDispatch(ctx context.Context, taskID, caseID, workflowID string, captured map[string]any) error
}

// recordDispatchBackoff is how long recordDispatch waits before each retry of
// a failed write.
var recordDispatchBackoff = []time.Duration{200 * time.Millisecond, time.Second, 3 * time.Second}

// recordDispatch records an accepted call, retrying a failed write after each
// of the backoff delays. It does not fail the step even when every attempt
// fails: failing it would retry the activity and send the call again, which
// the service takes as a second submission under a new id, while the first
// id's callbacks still find no record. A dispatch that could not be recorded
// is logged instead; its callbacks are answered 503 until the service stops
// retrying, and the task stays waiting for them.
func recordDispatch(ctx context.Context, rec DispatchRecorder, taskID, caseID, workflowID string, captured map[string]any, backoff []time.Duration) {
	err := rec.RecordDispatch(ctx, taskID, caseID, workflowID, captured)
	for i := 0; err != nil && i < len(backoff); i++ {
		slog.Warn("api_call: failed to record the accepted call; retrying",
			"taskId", taskID, "attempt", i+1, "retryIn", backoff[i], "error", err)
		select {
		case <-ctx.Done():
			slog.Error("api_call: accepted call not recorded: the step was cancelled",
				"taskId", taskID, "caseId", caseID, "workflowId", workflowID, "captured", captured, "error", err)
			return
		case <-time.After(backoff[i]):
		}
		err = rec.RecordDispatch(ctx, taskID, caseID, workflowID, captured)
	}
	if err != nil {
		slog.Error("api_call: accepted call not recorded; its callbacks will not find the task",
			"taskId", taskID, "caseId", caseID, "workflowId", workflowID, "captured", captured, "error", err)
	}
}

// MultipartInterpreter is implemented by interpreters whose service expects a
// multipart/form-data body — a JSON document alongside file uploads — rather
// than a JSON body. The plugin routes such an interpreter through the
// multipart transport and never calls BuildRequest for it.
//
// BuildParts takes a context because assembling the body may need to fetch the
// attachments from storage. An error is passed to Interpret as callErr, so a
// failure to assemble the request is reported through the same path as a
// failure to send it.
type MultipartInterpreter interface {
	Interpreter

	BuildParts(ctx context.Context, inputs map[string]any) ([]remote.Part, error)
}

// HeaderInterpreter is implemented by interpreters whose service needs a header
// resolved per call: a value that belongs to the case being processed rather than
// to the service — the identifier a provider issued for the organisation a
// submission is filed under, say. A header that is the same for every call
// belongs in the service's own configuration instead.
//
// The value reaches the interpreter as a task input, so which workflow variable
// it comes from is an artifact decision. Returning nil sends none: an
// interpreter must not invent an identity, and the service is the one to say
// whether it can be identified.
//
// It composes with either body contract — a JSON body or multipart parts.
type HeaderInterpreter interface {
	Interpreter

	BuildHeaders(inputs map[string]any) map[string]string
}

// headersFor returns the per-call headers the interpreter asks for, if any.
func headersFor(interp Interpreter, inputs map[string]any) map[string]string {
	hi, ok := interp.(HeaderInterpreter)
	if !ok {
		return nil
	}
	return hi.BuildHeaders(inputs)
}

// QueryInterpreter is implemented by interpreters whose service takes its
// parameters in the query string rather than a body — a lookup, typically,
// where the method is GET and there is nothing to send.
//
// Which workflow variable each parameter comes from is an artifact decision, as
// with BuildHeaders: the interpreter only names what the service asks for.
type QueryInterpreter interface {
	Interpreter

	BuildQuery(inputs map[string]any) url.Values
}

// queryFor returns the query parameters the interpreter asks for, if any.
func queryFor(interp Interpreter, inputs map[string]any) url.Values {
	qi, ok := interp.(QueryInterpreter)
	if !ok {
		return nil
	}
	return qi.BuildQuery(inputs)
}

// expandPath fills {name} placeholders in a configured path from the task's
// inputs, so a resource-scoped endpoint — /orders/{slug}/gate-pass — can be
// declared in an artifact and addressed per case.
//
// A placeholder with no input is an error rather than an empty segment: the
// alternative is calling a different URL than the one the artifact declared,
// which a service can only answer with a 404 the applicant cannot act on.
func expandPath(path string, inputs map[string]any) (string, error) {
	var missing []string
	var b strings.Builder
	rest := path
	for {
		open := strings.Index(rest, "{")
		if open == -1 {
			b.WriteString(rest)
			break
		}
		close := strings.Index(rest[open:], "}")
		if close == -1 {
			b.WriteString(rest)
			break
		}
		close += open

		name := rest[open+1 : close]
		b.WriteString(rest[:open])

		value, ok := inputs[name]
		if !ok || value == nil || fmt.Sprint(value) == "" {
			missing = append(missing, name)
		} else {
			b.WriteString(url.PathEscape(fmt.Sprint(value)))
		}
		rest = rest[close+1:]
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("api_call: path %q has no input for %s", path, strings.Join(missing, ", "))
	}
	return b.String(), nil
}

// RawInterpreter is implemented by interpreters that read the whole response —
// its status, headers and body — and decide what happens to the step, rather
// than reporting every outcome as captured fields. The plugin sends the call
// with remote.Manager.CallRaw for such an interpreter, and calls InterpretRaw
// instead of Interpret.
//
// resp is nil when no response arrived; callErr then says why: a connection
// failure, a timeout, or a body that could not be assembled. InterpretRaw
// returns what Interpret does, plus err:
//
//   - nil: the step completes with captured, as after Interpret.
//   - a plain error: the step fails, and the engine retries it.
//   - an error from plugins.Permanent: the step parks for an admin.
//
// statusOutcome holds the rules for a call that is safe to send again; an
// interpreter can use them, or decide for itself, for example to record a 5xx
// on a submission the service would take twice rather than retry it.
//
// It composes with the interfaces that shape the request.
type RawInterpreter interface {
	Interpreter

	InterpretRaw(resp *remote.RawResponse, callErr error) (accepted bool, captured map[string]any, err error)
}

// passthroughInterpreter sends the "payload" input as-is, or all the mapped
// inputs when there is none, and reads the response by its status (see
// statusOutcome).
type passthroughInterpreter struct{}

func (passthroughInterpreter) BuildRequest(inputs map[string]any) remote.Body {
	if v, ok := inputs["payload"]; ok {
		return remote.JSONBody{V: v}
	}
	return remote.JSONBody{V: inputs}
}

// Interpret satisfies Interpreter. The plugin reads this interpreter's
// responses through InterpretRaw, so it never calls Interpret.
func (passthroughInterpreter) Interpret(callErr error, resp map[string]any) (bool, map[string]any) {
	if callErr != nil {
		return false, map[string]any{"error": callErr.Error()}
	}
	out := map[string]any{}
	if resp != nil {
		out["response"] = resp
	}
	return true, out
}

func (passthroughInterpreter) InterpretRaw(resp *remote.RawResponse, callErr error) (bool, map[string]any, error) {
	return statusOutcome(resp, callErr)
}

// APICallPlugin makes an authenticated POST to a configured service and records
// the outcome. The request body and response interpretation are delegated to an
// Interpreter, so the plugin itself is domain-agnostic — any request/response
// shape works.
type APICallPlugin struct {
	manager     *remote.Manager
	interpreter Interpreter
}

// NewAPICallPlugin builds the AUTH_API_CALL plugin, which calls the services
// manager lists and sends the step's mapped inputs as they are. manager must be
// non-nil.
func NewAPICallPlugin(manager *remote.Manager) *APICallPlugin {
	if manager == nil {
		panic("manager is nil")
	}
	return &APICallPlugin{manager: manager, interpreter: passthroughInterpreter{}}
}

// NewAPICallPluginWithInterpreter builds an AUTH_API_CALL plugin whose
// requests and responses interp shapes. A nil interp sends the mapped inputs
// as they are, as NewAPICallPlugin does.
func NewAPICallPluginWithInterpreter(manager *remote.Manager, interp Interpreter) *APICallPlugin {
	p := NewAPICallPlugin(manager)
	if interp != nil {
		p.interpreter = interp
	}
	return p
}

type apiCallConfig struct {
	ServiceID string `json:"service_id"`
	Path      string `json:"path"`

	// Method defaults to POST, which every submission this plugin drives uses.
	// A lookup step sets GET, and then the interpreter's BuildQuery carries the
	// parameters instead of a body.
	//
	// The body is whatever the interpreter builds, whatever the method: HTTP
	// discourages content on a GET but does not forbid it, so which calls carry
	// one is the artifact's business rather than this plugin's. An interpreter
	// driving a GET returns nil from BuildRequest, and the generic passthrough
	// interpreter always builds one, so a GET configured on a plugin without its
	// own interpreter will send the mapped inputs as a body.
	Method      string `json:"method,omitempty"`
	ResultField string `json:"result_field,omitempty"` // record the accepted flag under this key

	// RetainState keeps the task in the state it was already in for the
	// duration of the call, instead of reporting QUEUED_EXTERNALLY.
	//
	// The default suits a call that hands the declaration to the agency: the
	// flow moves straight on to a step that waits for them, so the "with the
	// agency" panel covers the whole span. A call that comes straight back to
	// the step it was made from — a validity or pricing check, say — is not
	// that. Reporting it as queued would show the applicant the waiting panel for
	// a moment, and, because the portal keys a zone on task state, remount the
	// form under them and reseed it from what was last saved, losing whatever
	// they had typed since.
	RetainState bool `json:"retain_state,omitempty"`
}

func (p *APICallPlugin) Execute(ctx pluginContext, configRaw json.RawMessage) error {
	var cfg apiCallConfig
	if err := json.Unmarshal(configRaw, &cfg); err != nil {
		return configError(fmt.Errorf("api_call: invalid config: %w", err))
	}
	if cfg.ServiceID == "" || cfg.Path == "" {
		return configError(fmt.Errorf("api_call: service_id and path are required"))
	}

	method := strings.ToUpper(strings.TrimSpace(cfg.Method))
	if method == "" {
		method = http.MethodPost
	}

	path, err := expandPath(cfg.Path, ctx.Inputs)
	if err != nil {
		return inputError(err)
	}

	// The state a render config keys on while the call is out. A flow that
	// hands the declaration over moves straight from here to a step that waits
	// on the receiving agency, so reporting it now means the panel for "with
	// the agency" covers the whole span rather than starting a beat late. A
	// call that returns to the step it was made from sets retain_state and
	// stays where it is — see the field's own comment.
	if !cfg.RetainState {
		ctx.Record.State = "QUEUED_EXTERNALLY"
	}

	ri, raw := p.interpreter.(RawInterpreter)
	if raw {
		// An interpreter that decides the step's fate would retry a service the
		// manager doesn't list for a day; it is configuration, so it parks now.
		if _, err := p.manager.GetClient(cfg.ServiceID); err != nil {
			return configError(fmt.Errorf("api_call: %w", err))
		}
	}

	req, buildErr := p.buildRequest(ctx, method, path)
	if raw {
		return p.callRaw(ctx, ri, cfg, req, buildErr)
	}

	var resp map[string]any
	callErr := buildErr
	if callErr == nil {
		callErr = p.manager.Call(ctx.Context, cfg.ServiceID, req, &resp)
	}

	accepted, out := p.interpreter.Interpret(callErr, resp)
	p.record(ctx, cfg, accepted, out)
	if accepted {
		slog.Info("api_call: request accepted", "taskId", ctx.Record.TaskID, "serviceId", cfg.ServiceID)
	} else {
		slog.Warn("api_call: request not accepted", "taskId", ctx.Record.TaskID, "serviceId", cfg.ServiceID, "callErr", callErr, "result", out)
	}
	return nil
}

// buildRequest assembles the request the interpreter shapes: multipart parts
// for a MultipartInterpreter, a body from BuildRequest otherwise, with the
// headers and query it asks for. An error is a multipart body that could not be
// assembled; nothing is sent then, and the interpreter reports it through the
// same path as a failure to send.
func (p *APICallPlugin) buildRequest(ctx pluginContext, method, path string) (remote.Request, error) {
	if mp, ok := p.interpreter.(MultipartInterpreter); ok {
		parts, err := mp.BuildParts(ctx.Context, ctx.Inputs)
		if err != nil {
			return remote.Request{}, err
		}
		return remote.Request{
			Method:  http.MethodPost,
			Path:    path,
			Body:    remote.MultipartBody{Parts: parts},
			Headers: headersFor(mp, ctx.Inputs),
		}, nil
	}
	return remote.Request{
		Method:  method,
		Path:    path,
		Query:   queryFor(p.interpreter, ctx.Inputs),
		Body:    p.interpreter.BuildRequest(ctx.Inputs),
		Headers: headersFor(p.interpreter, ctx.Inputs),
	}, nil
}

// callRaw sends req for a RawInterpreter and lets it decide the step's fate. An
// error it returns fails the step (or parks it, when permanent) and records
// nothing; otherwise the outcome is recorded as on the Interpret path.
func (p *APICallPlugin) callRaw(ctx pluginContext, ri RawInterpreter, cfg apiCallConfig, req remote.Request, buildErr error) error {
	var resp *remote.RawResponse
	callErr := buildErr
	if callErr == nil {
		resp, callErr = p.manager.CallRaw(ctx.Context, cfg.ServiceID, req)
	}

	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	accepted, out, err := ri.InterpretRaw(resp, callErr)
	if err != nil {
		if _, permanent := flowplugins.PermanentType(err); permanent {
			slog.Error("api_call: call refused", "taskId", ctx.Record.TaskID, "serviceId", cfg.ServiceID, "status", status, "error", err)
		} else {
			slog.Warn("api_call: call failed; the step will be retried", "taskId", ctx.Record.TaskID, "serviceId", cfg.ServiceID, "status", status, "error", err)
		}
		return err
	}

	p.record(ctx, cfg, accepted, out)
	if accepted {
		slog.Info("api_call: request accepted", "taskId", ctx.Record.TaskID, "serviceId", cfg.ServiceID, "status", status)
	} else {
		slog.Error("api_call: call refused", "taskId", ctx.Record.TaskID, "serviceId", cfg.ServiceID, "status", status, "result", out)
	}
	return nil
}

// record writes the outcome of a call that completes the step: the captured
// fields, plus the accepted flag under result_field when the step asks for one,
// into the output namespace. An accepted call to a DispatchRecorder is recorded
// so its callbacks can find the task.
func (p *APICallPlugin) record(ctx pluginContext, cfg apiCallConfig, accepted bool, out map[string]any) {
	if out == nil {
		out = map[string]any{}
	}
	if cfg.ResultField != "" {
		out[cfg.ResultField] = accepted
	}
	if ns := ctx.OutputNamespace; ns != "" {
		ctx.Record.Data[ns] = out
	}
	if accepted {
		if rec, ok := p.interpreter.(DispatchRecorder); ok {
			recordDispatch(ctx.Context, rec, ctx.Record.TaskID, ctx.Record.CaseID, ctx.Record.ParentWorkflowID, out, recordDispatchBackoff)
		}
	}
}

// statusOutcome reads a response by its status, for a call that is safe to send
// again, such as a callback on a step waiting on another service:
//
//   - 2xx: accepted, with {status, response, error: ""}.
//   - 409: accepted the same way. The service answers 409 when what the call
//     addresses is no longer waiting for it, typically because an earlier
//     attempt of this same call got through. Sending it again can't change the
//     answer.
//   - 401 and 403: a permanent error, so the step parks. The credentials, or
//     what the caller may do, are wrong, and only an admin can fix that.
//   - 408, 429, 5xx, and no response at all: a plain error, so the engine
//     retries the step, and parks it once its retries run out.
//   - Any other status: not accepted, with {status, response, error}, for the
//     workflow to route.
//
// response is the body as JSON, or as text when it isn't JSON, and is left out
// when the body is empty. error is the body's "error" or "message" field, or
// the status (see refusalMessage).
func statusOutcome(resp *remote.RawResponse, callErr error) (bool, map[string]any, error) {
	if callErr != nil {
		return false, nil, fmt.Errorf("api_call: %w", callErr)
	}
	if resp == nil {
		return false, nil, errors.New("api_call: no response")
	}

	status := resp.StatusCode
	response := decodeResponse(resp.Body)
	var refusal string
	switch {
	case status >= 200 && status < 300, status == http.StatusConflict:
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return false, nil, flowplugins.Permanent(flowplugins.ErrorTypeRejected,
			fmt.Errorf("api_call: answered %d: %s", status, refusalMessage(status, response)))
	case flowplugins.RetryableStatus(status):
		return false, nil, fmt.Errorf("api_call: answered %d: %s", status, refusalMessage(status, response))
	default:
		refusal = refusalMessage(status, response)
	}

	out := map[string]any{"status": status, "error": refusal}
	if response != nil {
		out["response"] = response
	}
	return refusal == "", out, nil
}

// decodeResponse returns a response body as decoded JSON, or as text when it
// isn't JSON, or nil when it is empty.
func decodeResponse(body []byte) any {
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return string(body)
	}
	return v
}

// refusalMessage is the message a refused call is recorded with: the body's
// string "error" or "message" field when it has one, otherwise the status, as
// "404 Not Found".
func refusalMessage(status int, response any) string {
	if fields, ok := response.(map[string]any); ok {
		for _, key := range []string{"error", "message"} {
			if msg, ok := fields[key].(string); ok && strings.TrimSpace(msg) != "" {
				return msg
			}
		}
	}
	if text := http.StatusText(status); text != "" {
		return fmt.Sprintf("%d %s", status, text)
	}
	return fmt.Sprintf("%d", status)
}
