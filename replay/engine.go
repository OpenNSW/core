// Package replay is a small, data-driven engine for end-to-end tests: it
// executes an ordered list of steps (defined in a JSON flow file) against a
// running NSW backend. Each flow is data, so new scenarios (FCAU, NPQS, …) are
// authored as config rather than code.
//
// The engine speaks only HTTP + JSON, so it is independent of the backend's
// internals and could later drive a black-box target. Per-step identity is
// selected by the "actor" field, surfaced to the server via the
// X-Auth-Actor header (the in-process test transport maps actors to real tokens).
//
// Step kinds:
//   - request:  issue an HTTP call, assert the status, optionally extract
//     response fields into variables for later steps.
//   - wait:     poll the consignment detail until a workflow node (matched by
//     display-name substring) reaches a state; optionally capture its task id.
//   - callback: drive a mock external agency to complete a parked EXTERNAL_REVIEW
//     task, posting {command, payload} back into NSW.
//   - pay:      drive a mock payment gateway to confirm a parked payment task via
//     a gateway webhook.
//   - upload:   upload a file as an actor, as a portal does, and capture its file
//     reference for a form's file field.
//
// Strings in paths and request bodies may reference variables as {{name}}.
package replay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// AuthActorHeader carries the replay actor key to the stub-auth middleware.
const AuthActorHeader = "X-Auth-Actor"

// consignmentIDVar is the well-known variable holding the consignment id that
// wait/callback steps poll against. A create step typically extracts it.
const consignmentIDVar = "consignmentId"

// Default timeouts used when a step omits the timeout field.
const (
	defaultWaitTimeout     = 45 * time.Second
	defaultCallbackTimeout = 30 * time.Second
	defaultPayTimeout      = 45 * time.Second
	waitPollInterval       = 500 * time.Millisecond
)

// Flow is an ordered list of steps loaded from a JSON file.
type Flow struct {
	Name  string `json:"name"`
	Steps []Step `json:"steps"`
}

// Step is exactly one of request, wait, callback, pay, or upload.
type Step struct {
	Name     string    `json:"name"`
	Request  *Request  `json:"request,omitempty"`
	Wait     *Wait     `json:"wait,omitempty"`
	Callback *Callback `json:"callback,omitempty"`
	Pay      *Pay      `json:"pay,omitempty"`
	Upload   *Upload   `json:"upload,omitempty"`
}

// Request issues an HTTP call as a given actor.
type Request struct {
	Actor        string            `json:"actor"`
	Method       string            `json:"method"`
	Path         string            `json:"path"`
	Body         any               `json:"body,omitempty"`
	ExpectStatus int               `json:"expectStatus,omitempty"` // default 200
	Extract      map[string]string `json:"extract,omitempty"`      // var -> response field path (dot-notation, e.g. "consignment.id"; a number indexes an array, e.g. "flows.0.workflowId")
	// Retry, for a GET only, re-issues the request until it gets the expected
	// status and every Extract path is present, or this long elapses (e.g. "30s").
	// For state that lands a moment after a wait matched: a task's step_id is
	// set when its step is claimed, just after the node shows IN_PROGRESS.
	Retry string `json:"retry,omitempty"`
}

// Wait polls the consignment detail until a workflow node matches.
type Wait struct {
	Node    string `json:"node"`           // substring match on the node display name
	State   string `json:"state"`          // required node state (empty = any)
	Into    string `json:"into,omitempty"` // store the matched node's task id here
	Timeout string `json:"timeout,omitempty"`
}

// Callback drives the mock external agency to complete a parked EXTERNAL_REVIEW task.
type Callback struct {
	TaskVar string         `json:"taskVar"` // name of the flow variable that holds the task id (set by a prior wait `into`)
	Command string         `json:"command"` // outcome command (e.g. "approve", "reject")
	Content map[string]any `json:"content"` // reviewer payload sent back as the command payload
	Timeout string         `json:"timeout,omitempty"`
}

// Agency is the generic mock external agency the callback step drives. The in-process
// test provides an implementation backed by a stub HTTP server.
type Agency interface {
	// Respond waits (up to timeout) for an inject for taskID, then posts
	// {command, payload:content} to the NSW task endpoint to complete the
	// parked EXTERNAL_REVIEW step.
	Respond(ctx context.Context, taskID, command string, content map[string]any, timeout time.Duration) error
}

// Pay drives a parked PAYMENT task to completion by simulating a gateway
// success webhook for the payment created against the task in TaskVar.
type Pay struct {
	TaskVar string `json:"taskVar"`          // variable holding the pay task's id
	Method  string `json:"method,omitempty"` // payment gateway id, e.g. "govpay"
	Status  string `json:"status,omitempty"` // gateway success status (default "paid")
	Timeout string `json:"timeout,omitempty"`
}

// PaymentGateway is the mock payment gateway the pay step drives. The in-process
// test provides an implementation that resolves the payment reference and posts
// the gateway webhook.
type PaymentGateway interface {
	// Pay waits (up to timeout) for the payment created against taskID, then
	// confirms it by posting a gateway success webhook with the given status.
	// method identifies which payment gateway to use.
	Pay(ctx context.Context, taskID, method, status string, timeout time.Duration) error
}

// Upload uploads a file as Actor: it reserves the upload (POST /api/v1/storage),
// PUTs Content to the upload URL that returns, and stores the file's reference in
// the variable Into, to send as the value of a form's file field.
type Upload struct {
	Actor    string `json:"actor"`
	Filename string `json:"filename"`
	MimeType string `json:"mimeType"`
	Content  string `json:"content"` // the file's bytes
	Into     string `json:"into"`
}

// LoadFlow reads and parses a JSON flow file.
func LoadFlow(path string) (*Flow, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read flow %s: %w", path, err)
	}
	var f Flow
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse flow %s: %w", path, err)
	}
	if len(f.Steps) == 0 {
		return nil, fmt.Errorf("flow %s has no steps", path)
	}
	return &f, nil
}

// Runner executes flows against a backend reachable at BaseURL.
type Runner struct {
	BaseURL        string
	Client         *http.Client
	Vars           map[string]any
	Logf           func(string, ...any)
	Agency         Agency
	PaymentGateway PaymentGateway
}

// New builds a Runner with an empty variable store and a no-op logger.
func New(baseURL string, client *http.Client) *Runner {
	if client == nil {
		client = http.DefaultClient
	}
	return &Runner{
		BaseURL: baseURL,
		Client:  client,
		Vars:    map[string]any{},
		Logf:    func(string, ...any) {},
	}
}

// Run executes every step in order, returning the first error encountered.
func (r *Runner) Run(ctx context.Context, flow *Flow) error {
	for i, step := range flow.Steps {
		label := fmt.Sprintf("step %d/%d %q", i+1, len(flow.Steps), step.Name)
		var err error
		switch {
		case step.Request != nil:
			err = r.doRequest(ctx, step.Request)
		case step.Wait != nil:
			err = r.doWait(ctx, step.Wait)
		case step.Callback != nil:
			err = r.doCallback(ctx, step.Callback)
		case step.Pay != nil:
			err = r.doPay(ctx, step.Pay)
		case step.Upload != nil:
			err = r.doUpload(ctx, step.Upload)
		default:
			err = fmt.Errorf("step has no request/wait/callback/pay/upload")
		}
		if err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		r.Logf("✓ %s", label)
	}
	return nil
}

func (r *Runner) doRequest(ctx context.Context, req *Request) error {
	if req.Retry == "" {
		return r.doRequestOnce(ctx, req)
	}
	if req.Method != http.MethodGet {
		return fmt.Errorf("retry is only allowed on GET, not %s: retrying could repeat a side effect", req.Method)
	}
	timeout, err := parseTimeout(req.Retry, defaultWaitTimeout)
	if err != nil {
		return err
	}
	// Each attempt is bounded by the same deadline, so a request that stalls is
	// cancelled when the retry window ends rather than holding the replay open.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		err := r.doRequestOnce(ctx, req)
		if err == nil || ctx.Err() != nil {
			return err
		}
		// The window can close during the pause too; the last attempt's error says
		// more about why the step never succeeded than the deadline does.
		if sleep(ctx, waitPollInterval) != nil {
			return err
		}
	}
}

func (r *Runner) doRequestOnce(ctx context.Context, req *Request) error {
	path := r.interpolate(req.Path)

	var rdr io.Reader
	if req.Body != nil {
		raw, err := json.Marshal(r.interpolateValue(req.Body))
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		rdr = bytes.NewReader(raw)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, r.BaseURL+path, rdr)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if req.Actor != "" {
		httpReq.Header.Set(AuthActorHeader, req.Actor)
	}

	resp, err := r.Client.Do(httpReq)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	want := req.ExpectStatus
	if want == 0 {
		want = http.StatusOK
	}
	if resp.StatusCode != want {
		return fmt.Errorf("%s %s: got status %d, want %d: %s", req.Method, path, resp.StatusCode, want, truncate(respBody))
	}

	if len(req.Extract) > 0 {
		var obj map[string]any
		if err := json.Unmarshal(respBody, &obj); err != nil {
			return fmt.Errorf("extract: response must be a JSON object: %w (body=%s)", err, truncate(respBody))
		}
		for varName, path := range req.Extract {
			v, ok := lookupPath(obj, path)
			if !ok {
				return fmt.Errorf("extract: path %q not present in response", path)
			}
			r.Vars[varName] = v
		}
	}
	return nil
}

func (r *Runner) doWait(ctx context.Context, w *Wait) error {
	timeout, err := parseTimeout(w.Timeout, defaultWaitTimeout)
	if err != nil {
		return err
	}
	cid, _ := r.Vars[consignmentIDVar].(string)
	if cid == "" {
		return fmt.Errorf("wait: %q variable not set (extract it in an earlier step)", consignmentIDVar)
	}

	deadline := time.Now().Add(timeout)
	var last consignmentDetail
	for {
		d, err := r.fetchDetail(ctx, cid)
		if err != nil {
			return err
		}
		last = d
		for _, n := range d.WorkflowNodes {
			if strings.Contains(n.Template.Name, w.Node) && (w.State == "" || n.State == w.State) {
				if w.Into != "" {
					r.Vars[w.Into] = n.ID
				}
				return nil
			}
		}
		if time.Now().After(deadline) {
			buf, _ := json.MarshalIndent(last.WorkflowNodes, "", "  ")
			return fmt.Errorf("timed out after %s waiting for node %q state %q; current nodes:\n%s", timeout, w.Node, w.State, buf)
		}
		if err := sleep(ctx, waitPollInterval); err != nil {
			return err
		}
	}
}

func (r *Runner) doCallback(ctx context.Context, cb *Callback) error {
	if r.Agency == nil {
		return fmt.Errorf("callback step requires an Agency responder (none configured)")
	}
	timeout, err := parseTimeout(cb.Timeout, defaultCallbackTimeout)
	if err != nil {
		return err
	}
	taskID, _ := r.Vars[cb.TaskVar].(string)
	if taskID == "" {
		return fmt.Errorf("callback: %q variable not set (capture the task id via a wait `into` first)", cb.TaskVar)
	}
	return r.Agency.Respond(ctx, taskID, cb.Command, r.interpolateMap(cb.Content), timeout)
}

func (r *Runner) doPay(ctx context.Context, p *Pay) error {
	if r.PaymentGateway == nil {
		return fmt.Errorf("pay step requires a PaymentGateway (none configured)")
	}
	timeout, err := parseTimeout(p.Timeout, defaultPayTimeout)
	if err != nil {
		return err
	}
	taskID, _ := r.Vars[p.TaskVar].(string)
	if taskID == "" {
		return fmt.Errorf("pay: %q variable not set (capture the pay task id via a wait `into` first)", p.TaskVar)
	}
	status := p.Status
	if status == "" {
		status = "paid"
	}
	return r.PaymentGateway.Pay(ctx, taskID, p.Method, status, timeout)
}

func (r *Runner) doUpload(ctx context.Context, u *Upload) error {
	if u.Into == "" {
		return fmt.Errorf("upload: into is required")
	}
	if u.Content == "" {
		return fmt.Errorf("upload: content is required")
	}

	reservation, err := json.Marshal(map[string]any{
		"filename":  u.Filename,
		"mime_type": u.MimeType,
		"size":      len(u.Content),
	})
	if err != nil {
		return fmt.Errorf("upload: marshal reservation: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.BaseURL+"/api/v1/storage", bytes.NewReader(reservation))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if u.Actor != "" {
		req.Header.Set(AuthActorHeader, u.Actor)
	}
	body, err := r.send(req, http.StatusOK)
	if err != nil {
		return fmt.Errorf("upload: reserve: %w", err)
	}
	var reserved struct {
		Key       string `json:"key"`
		UploadURL string `json:"upload_url"`
	}
	if err := json.Unmarshal(body, &reserved); err != nil {
		return fmt.Errorf("upload: decode reservation: %w (body=%s)", err, truncate(body))
	}
	if reserved.Key == "" || reserved.UploadURL == "" {
		return fmt.Errorf("upload: reservation has no key or upload_url: %s", truncate(body))
	}

	// The upload URL is presigned, so the PUT carries no actor.
	put, err := http.NewRequestWithContext(ctx, http.MethodPut, reserved.UploadURL, strings.NewReader(u.Content))
	if err != nil {
		return err
	}
	put.Header.Set("Content-Type", u.MimeType)
	if _, err := r.send(put, 0); err != nil {
		return fmt.Errorf("upload: put content: %w", err)
	}

	r.Vars[u.Into] = reserved.Key
	return nil
}

// send issues req and returns its response body. It fails unless the status is
// want, or any 2xx when want is 0.
func (r *Runner) send(req *http.Request, want int) ([]byte, error) {
	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	ok := resp.StatusCode == want
	if want == 0 {
		ok = resp.StatusCode >= 200 && resp.StatusCode < 300
	}
	if !ok {
		return nil, fmt.Errorf("%s %s: status %d: %s", req.Method, req.URL.Path, resp.StatusCode, truncate(body))
	}
	return body, nil
}

// ── consignment detail polling ──────────────────────────────────────────────

type nodeView struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Template struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"workflowNodeTemplate"`
}

type consignmentDetail struct {
	ID            string     `json:"id"`
	State         string     `json:"state"`
	ChaCompanyID  string     `json:"chaCompanyId"`
	WorkflowNodes []nodeView `json:"workflowNodes"`
}

func (r *Runner) fetchDetail(ctx context.Context, consignmentID string) (consignmentDetail, error) {
	var d consignmentDetail
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.BaseURL+"/api/v1/consignments/"+consignmentID, nil)
	if err != nil {
		return d, err
	}
	req.Header.Set(AuthActorHeader, "trader")
	resp, err := r.Client.Do(req)
	if err != nil {
		return d, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return d, fmt.Errorf("read consignment detail response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return d, fmt.Errorf("GET consignment %s: status %d: %s", consignmentID, resp.StatusCode, truncate(body))
	}
	if err := json.Unmarshal(body, &d); err != nil {
		return d, fmt.Errorf("decode consignment detail: %w", err)
	}
	return d, nil
}

// ── helpers ─────────────────────────────────────────────────────────────────

var varRe = regexp.MustCompile(`\{\{(\w+)\}\}`)

// interpolate replaces {{name}} tokens in s with the string form of Vars[name].
func (r *Runner) interpolate(s string) string {
	return varRe.ReplaceAllStringFunc(s, func(m string) string {
		name := varRe.FindStringSubmatch(m)[1]
		if v, ok := r.Vars[name]; ok {
			return fmt.Sprint(v)
		}
		return m
	})
}

// interpolateValue walks maps/slices/strings, interpolating string values.
func (r *Runner) interpolateValue(v any) any {
	switch t := v.(type) {
	case string:
		return r.interpolate(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = r.interpolateValue(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = r.interpolateValue(val)
		}
		return out
	default:
		return v
	}
}

func (r *Runner) interpolateMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	return r.interpolateValue(m).(map[string]any)
}

func parseTimeout(s string, def time.Duration) (time.Duration, error) {
	if s == "" {
		return def, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid timeout %q: %w", s, err)
	}
	return d, nil
}

func sleep(ctx context.Context, d time.Duration) error {
	// Use a managed timer (not time.After) so it is released immediately when
	// ctx is cancelled, rather than lingering until d elapses — avoids timer
	// accumulation across many poll iterations.
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// lookupPath reads a dot-notation path from obj. A segment selects an object's field, or,
// when it is a number, an array's element (e.g. "flows.0.workflowId").
func lookupPath(obj map[string]any, path string) (any, bool) {
	var cur any = obj
	for _, seg := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[seg]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}
			cur = node[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

func truncate(b []byte) string {
	const max = 512
	if len(b) > max {
		return string(b[:max]) + "…"
	}
	return string(b)
}
