package tasktypes

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/OpenNSW/core/remote"
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

// passthroughInterpreter sends the "payload" input as-is and treats any
// transport-level success as accepted.
type passthroughInterpreter struct{}

func (passthroughInterpreter) BuildRequest(inputs map[string]any) remote.Body {
	if v, ok := inputs["payload"]; ok {
		return remote.JSONBody{V: v}
	}
	return remote.JSONBody{V: inputs}
}

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

	var resp map[string]any
	var callErr error
	if mp, ok := p.interpreter.(MultipartInterpreter); ok {
		callErr = p.callMultipart(ctx, mp, path, cfg, &resp)
	} else {
		req := remote.Request{
			Method:  method,
			Path:    path,
			Query:   queryFor(p.interpreter, ctx.Inputs),
			Body:    p.interpreter.BuildRequest(ctx.Inputs),
			Headers: headersFor(p.interpreter, ctx.Inputs),
		}
		callErr = p.manager.Call(ctx.Context, cfg.ServiceID, req, &resp)
	}

	accepted, out := p.interpreter.Interpret(callErr, resp)
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
		slog.Info("api_call: request accepted", "taskId", ctx.Record.TaskID, "serviceId", cfg.ServiceID)
	} else {
		slog.Warn("api_call: request not accepted", "taskId", ctx.Record.TaskID, "serviceId", cfg.ServiceID, "callErr", callErr, "result", out)
	}
	return nil
}

// callMultipart assembles and sends a multipart/form-data submission. A
// failure to assemble the body is returned rather than sent, so the
// interpreter reports it to the applicant through the same path as a rejection —
// nothing reaches the service in that case.
func (p *APICallPlugin) callMultipart(ctx pluginContext, mp MultipartInterpreter, path string, cfg apiCallConfig, resp *map[string]any) error {
	parts, err := mp.BuildParts(ctx.Context, ctx.Inputs)
	if err != nil {
		return err
	}
	return p.manager.Call(ctx.Context, cfg.ServiceID, remote.Request{
		Method:  http.MethodPost,
		Path:    path,
		Body:    remote.MultipartBody{Parts: parts},
		Headers: headersFor(mp, ctx.Inputs),
	}, resp)
}
