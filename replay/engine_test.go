package replay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestInterpolate(t *testing.T) {
	r := New("", nil)
	r.Vars = map[string]any{"id": "abc", "n": 7}

	cases := map[string]string{
		"/x/{{id}}":         "/x/abc",
		"/x/{{id}}/y/{{n}}": "/x/abc/y/7",
		"/x/{{missing}}":    "/x/{{missing}}", // unknown vars are left intact
		"no placeholders":   "no placeholders",
	}
	for in, want := range cases {
		if got := r.interpolate(in); got != want {
			t.Errorf("interpolate(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInterpolateValue(t *testing.T) {
	r := New("", nil)
	r.Vars = map[string]any{"cha": "adam-pvt-ltd"}

	in := map[string]any{
		"cha_company_id": "{{cha}}",
		"nested":         map[string]any{"k": "{{cha}}"},
		"list":           []any{"{{cha}}", "static"},
		"num":            42,
	}
	got := r.interpolateValue(in)
	want := map[string]any{
		"cha_company_id": "adam-pvt-ltd",
		"nested":         map[string]any{"k": "adam-pvt-ltd"},
		"list":           []any{"adam-pvt-ltd", "static"},
		"num":            42,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("interpolateValue = %#v, want %#v", got, want)
	}
}

func TestLoadFlow(t *testing.T) {
	dir := t.TempDir()

	good := filepath.Join(dir, "good.json")
	mustWrite(t, good, `{"name":"f","steps":[{"name":"s","request":{"method":"GET","path":"/x"}}]}`)
	flow, err := LoadFlow(good)
	if err != nil {
		t.Fatalf("LoadFlow(good): %v", err)
	}
	if flow.Name != "f" || len(flow.Steps) != 1 {
		t.Fatalf("unexpected flow: %+v", flow)
	}

	empty := filepath.Join(dir, "empty.json")
	mustWrite(t, empty, `{"name":"f","steps":[]}`)
	if _, err := LoadFlow(empty); err == nil {
		t.Error("LoadFlow(empty): expected error for zero steps")
	}

	if _, err := LoadFlow(filepath.Join(dir, "nope.json")); err == nil {
		t.Error("LoadFlow(missing): expected error")
	}
}

func TestRunner_RequestExtractAndStatusAssertion(t *testing.T) {
	var gotActor, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotActor = r.Header.Get(AuthActorHeader)
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "c-123"})
	}))
	defer srv.Close()

	r := New(srv.URL, srv.Client())
	flow := &Flow{Name: "t", Steps: []Step{
		{Name: "create", Request: &Request{
			Actor: "trader", Method: "POST", Path: "/api/v1/consignments",
			Body: map[string]any{"x": 1}, ExpectStatus: 201,
			Extract: map[string]string{"consignmentId": "id"},
		}},
	}}
	if err := r.Run(context.Background(), flow); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.Vars["consignmentId"] != "c-123" {
		t.Errorf("extract: consignmentId = %v, want c-123", r.Vars["consignmentId"])
	}
	if gotActor != "trader" {
		t.Errorf("actor header = %q, want trader", gotActor)
	}
	if !strings.Contains(gotBody, `"x":1`) {
		t.Errorf("request body = %q, want it to contain x:1", gotBody)
	}

	// Status mismatch must fail the step.
	bad := &Flow{Name: "t", Steps: []Step{
		{Name: "create", Request: &Request{Method: "POST", Path: "/", ExpectStatus: 200}},
	}}
	if err := r.Run(context.Background(), bad); err == nil {
		t.Error("expected error on status mismatch (got 201, want 200)")
	}
}

// A retried GET is re-issued until the extract path appears, as a task's step_id
// does once its step is claimed.
func TestRunner_RequestRetriesUntilExtractPresent(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body := map[string]any{"task_id": "t-1"}
		if calls >= 3 {
			body["step_id"] = "s-1"
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	r := New(srv.URL, srv.Client())
	flow := &Flow{Name: "t", Steps: []Step{
		{Name: "read", Request: &Request{
			Method: "GET", Path: "/api/v1/tasks/t-1", Retry: "10s",
			Extract: map[string]string{"step": "step_id"},
		}},
	}}
	if err := r.Run(context.Background(), flow); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.Vars["step"] != "s-1" || calls != 3 {
		t.Errorf("step = %v after %d calls, want s-1 after 3", r.Vars["step"], calls)
	}

	// Retrying a POST could repeat its side effect, so it is refused.
	post := &Flow{Name: "t", Steps: []Step{
		{Name: "submit", Request: &Request{Method: "POST", Path: "/", Retry: "10s"}},
	}}
	if err := r.Run(context.Background(), post); err == nil || !strings.Contains(err.Error(), "only allowed on GET") {
		t.Errorf("POST with retry: err = %v, want a refusal", err)
	}
}

// A retried GET that stalls is cancelled when the retry window ends, instead of
// holding the replay until the whole test times out.
func TestRunner_RequestRetryBoundsAStalledRequest(t *testing.T) {
	// The handler answers on its own after 2s, so without the bound the test
	// fails on the elapsed time instead of hanging.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	r := New(srv.URL, srv.Client())
	flow := &Flow{Name: "t", Steps: []Step{
		{Name: "read", Request: &Request{Method: "GET", Path: "/api/v1/tasks/t-1", Retry: "200ms"}},
	}}
	start := time.Now()
	err := r.Run(context.Background(), flow)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Run took %v, want it bounded by the 200ms retry window", elapsed)
	}
	if err == nil {
		t.Error("Run: expected an error from the stalled request")
	}
}

func TestRunner_WaitMatchesNode(t *testing.T) {
	detail := `{"id":"c-1","state":"IN_PROGRESS","workflowNodes":[
		{"id":"task-init","state":"COMPLETED","workflowNodeTemplate":{"name":"[Trade] Initialize Consignment","type":"APPLICATION"}},
		{"id":"task-hs","state":"IN_PROGRESS","workflowNodeTemplate":{"name":"[Trade] Select HS Codes","type":"APPLICATION"}}
	]}`
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/consignments/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(detail))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	r := New(srv.URL, srv.Client())
	r.Vars[consignmentIDVar] = "c-1"

	if err := r.doWait(context.Background(), &Wait{Node: "Select HS Codes", State: "IN_PROGRESS", Into: "hsTask", Timeout: "2s"}); err != nil {
		t.Fatalf("doWait: %v", err)
	}
	if r.Vars["hsTask"] != "task-hs" {
		t.Errorf("wait Into: hsTask = %v, want task-hs", r.Vars["hsTask"])
	}

	// A node that never matches must time out.
	err := r.doWait(context.Background(), &Wait{Node: "Nonexistent", State: "IN_PROGRESS", Timeout: "1ms"})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout error, got %v", err)
	}
}

func TestLookupPath(t *testing.T) {
	obj := map[string]any{
		"id":          "top",
		"consignment": map[string]any{"id": "nested", "deep": map[string]any{"k": "v"}},
	}
	cases := []struct {
		path    string
		want    any
		present bool
	}{
		{"id", "top", true},
		{"consignment.id", "nested", true},
		{"consignment.deep.k", "v", true},
		{"missing", nil, false},
		{"consignment.missing", nil, false},
		{"id.x", nil, false}, // descending into a non-object
	}
	for _, c := range cases {
		got, ok := lookupPath(obj, c.path)
		if ok != c.present || (ok && got != c.want) {
			t.Errorf("lookupPath(%q) = (%v, %v), want (%v, %v)", c.path, got, ok, c.want, c.present)
		}
	}
}

func TestDoCallback(t *testing.T) {
	r := New("", nil)
	// Without an Agency the callback step is an error.
	if err := r.doCallback(context.Background(), &Callback{TaskVar: "myTask"}); err == nil {
		t.Error("expected error when Agency is nil")
	}

	fake := &fakeAgency{}
	r.Agency = fake
	// Missing taskVar is an error.
	if err := r.doCallback(context.Background(), &Callback{TaskVar: "myTask", Command: "approve"}); err == nil {
		t.Error("expected error when taskVar variable is not set")
	}

	r.Vars["myTask"] = "task-abc"
	r.Vars["ref"] = "REF-9"
	cb := &Callback{
		TaskVar: "myTask",
		Command: "approve",
		Content: map[string]any{"application_review_outcome": "approve", "reference_number": "{{ref}}"},
		Timeout: "5s",
	}
	if err := r.doCallback(context.Background(), cb); err != nil {
		t.Fatalf("doCallback: %v", err)
	}
	if fake.taskID != "task-abc" {
		t.Errorf("agency taskID = %q", fake.taskID)
	}
	if fake.command != "approve" {
		t.Errorf("agency command = %q", fake.command)
	}
	if fake.content["reference_number"] != "REF-9" {
		t.Errorf("callback content not interpolated: %v", fake.content)
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

type fakeAgency struct {
	taskID  string
	command string
	content map[string]any
}

func (f *fakeAgency) Respond(_ context.Context, taskID, command string, content map[string]any, _ time.Duration) error {
	f.taskID = taskID
	f.command = command
	f.content = content
	return nil
}

func TestDoPay(t *testing.T) {
	r := New("", nil)
	// Without a PaymentGateway the pay step is an error.
	if err := r.doPay(context.Background(), &Pay{TaskVar: "payTask"}); err == nil {
		t.Error("expected error when Gateway is nil")
	}

	fake := &fakeGateway{}
	r.PaymentGateway = fake
	// Missing taskVar is an error.
	if err := r.doPay(context.Background(), &Pay{TaskVar: "payTask"}); err == nil {
		t.Error("expected error when taskVar is not set")
	}

	r.Vars["payTask"] = "fcau_2_0_pay_fee:abc"
	if err := r.doPay(context.Background(), &Pay{TaskVar: "payTask"}); err != nil {
		t.Fatalf("doPay: %v", err)
	}
	if fake.taskID != "fcau_2_0_pay_fee:abc" {
		t.Errorf("gateway taskID = %q", fake.taskID)
	}
	if fake.status != "paid" { // default when Pay.Status is empty
		t.Errorf("gateway status = %q, want default paid", fake.status)
	}
}

type fakeGateway struct {
	taskID string
	method string
	status string
}

func (f *fakeGateway) Pay(_ context.Context, taskID, method, status string, _ time.Duration) error {
	f.taskID = taskID
	f.method = method
	f.status = status
	return nil
}

// An upload reserves the file as the actor, PUTs its bytes to the presigned URL
// without the actor, and captures the file's reference.
func TestDoUpload(t *testing.T) {
	var reserveActor, putActor, putType, putBody string
	var reserved map[string]any
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/storage":
			reserveActor = r.Header.Get(AuthActorHeader)
			_ = json.NewDecoder(r.Body).Decode(&reserved)
			_ = json.NewEncoder(w).Encode(map[string]any{"key": "ref-1", "upload_url": srv.URL + "/content/k1?token=t"})
		case r.Method == http.MethodPut && r.URL.Path == "/content/k1":
			putActor = r.Header.Get(AuthActorHeader)
			putType = r.Header.Get("Content-Type")
			b, _ := io.ReadAll(r.Body)
			putBody = string(b)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	r := New(srv.URL, srv.Client())
	err := r.doUpload(context.Background(), &Upload{
		Actor: "trader", Filename: "cert.pdf", MimeType: "application/pdf", Content: "%PDF-1.4", Into: "cert",
	})
	if err != nil {
		t.Fatalf("doUpload: %v", err)
	}
	if r.Vars["cert"] != "ref-1" {
		t.Errorf("cert = %v, want ref-1", r.Vars["cert"])
	}
	if reserveActor != "trader" || putActor != "" {
		t.Errorf("actors: reserve %q, put %q; want trader, none", reserveActor, putActor)
	}
	want := map[string]any{"filename": "cert.pdf", "mime_type": "application/pdf", "size": float64(len("%PDF-1.4"))}
	if !reflect.DeepEqual(reserved, want) {
		t.Errorf("reservation = %v, want %v", reserved, want)
	}
	if putType != "application/pdf" || putBody != "%PDF-1.4" {
		t.Errorf("put: Content-Type %q, body %q", putType, putBody)
	}

	if err := r.doUpload(context.Background(), &Upload{Content: "x"}); err == nil {
		t.Error("expected error without into")
	}
}

// A refused PUT fails the upload, and no reference is captured.
func TestDoUploadRefusedPut(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusUnsupportedMediaType)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"key": "ref-1", "upload_url": srv.URL + "/content/k1"})
	}))
	defer srv.Close()

	r := New(srv.URL, srv.Client())
	err := r.doUpload(context.Background(), &Upload{Filename: "a.pdf", MimeType: "application/pdf", Content: "x", Into: "cert"})
	if err == nil {
		t.Fatal("expected error when the PUT is refused")
	}
	if _, ok := r.Vars["cert"]; ok {
		t.Error("a failed upload captured a reference")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestLookupPathIndexesArrays(t *testing.T) {
	obj := map[string]any{"flows": []any{map[string]any{"workflowId": "w0"}, map[string]any{"workflowId": "w1"}}}
	for path, want := range map[string]any{"flows.1.workflowId": "w1", "flows.0.workflowId": "w0"} {
		got, ok := lookupPath(obj, path)
		if !ok || got != want {
			t.Errorf("lookupPath(%q) = %v, %v; want %v", path, got, ok, want)
		}
	}
	for _, path := range []string{"flows.2.workflowId", "flows.x.workflowId", "flows.-1", "flows.0.missing"} {
		if got, ok := lookupPath(obj, path); ok {
			t.Errorf("lookupPath(%q) = %v; want not present", path, got)
		}
	}
}
