package systemlog

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenNSW/core/trace"
	"github.com/OpenNSW/core/trace/logging"

	"github.com/OpenNSW/nsw-srilanka/internal/authn"
	"github.com/OpenNSW/nsw-srilanka/internal/securitylog"
)

var errAnnotateTest = errors.New("sig mismatch")

func testLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(StampCategory(slog.NewJSONHandler(buf, nil)))
}

func TestMiddlewareLogsCallerWithoutPayload(t *testing.T) {
	var buf bytes.Buffer
	logger := testLogger(&buf)

	const payload = `{"command":"approve","email":"a@example.com","phone":"+94111"}`
	const response = `{"status":"accepted","email":"a@example.com"}`

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/tasks/{id}", attachUserAndEcho(t, "user-1", payload, response))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/abc?x=1", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer super-secret-token")
	req.RemoteAddr = "203.0.113.10:54321"

	rec := httptest.NewRecorder()
	Middleware(logger)(mux).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated || rec.Body.String() != response {
		t.Fatalf("client response = %d %q", rec.Code, rec.Body.String())
	}

	raw := buf.String()
	assertLogOmits(t, raw,
		"super-secret-token", "a@example.com", "+94111", payload, response, "203.0.113.10")
	line := decodeLine(t, buf.Bytes())
	assertEqual(t, "category", line["category"], "system")
	assertEqual(t, "msg", line["msg"], "http request")
	assertEqual(t, "method", line["method"], "POST")
	assertEqual(t, "path", line["path"], "/api/v1/tasks/abc")
	assertEqual(t, "pattern", line["pattern"], "POST /api/v1/tasks/{id}")
	assertEqual(t, "callerKind", line["callerKind"], "user")
	assertEqual(t, "callerID", line["callerID"], "user-1")
	assertEqual(t, "status", line["status"], float64(http.StatusCreated))
	assertEqual(t, "requestBytes", line["requestBytes"], float64(len(payload)))
	assertEqual(t, "responseBytes", line["responseBytes"], float64(len(response)))
	assertKeysAbsent(t, line, "remoteAddr", "query", "requestBody", "responseBody")
	assertKeysPresent(t, line, "time", "durationMs")
}

func attachUserAndEcho(t *testing.T, userID, wantBody, response string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		p := &authn.Principal{Kind: authn.KindUser, UserID: userID}
		if slot := authn.RequestCallerFrom(r.Context()); slot != nil {
			slot.Principal = p
		}
		got, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if string(got) != wantBody {
			t.Fatalf("handler body = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(response))
	}
}

func assertLogOmits(t *testing.T, raw string, forbidden ...string) {
	t.Helper()
	for _, s := range forbidden {
		if strings.Contains(raw, s) {
			t.Fatalf("security log contained %q: %s", s, raw)
		}
	}
}

func assertKeysAbsent(t *testing.T, line map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := line[k]; ok {
			t.Fatalf("%s should not be logged: %v", k, line[k])
		}
	}
}

func assertKeysPresent(t *testing.T, line map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := line[k]; !ok {
			t.Fatalf("expected %s", k)
		}
	}
}

func TestMiddlewareAnonymousWhenUnauthenticated(t *testing.T) {
	var buf bytes.Buffer
	logger := testLogger(&buf)
	const payload = `{"event":"ping"}`

	h := Middleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authn.FromContext(r.Context()); ok {
			t.Fatal("expected no principal")
		}
		if _, err := io.ReadAll(r.Body); err != nil {
			t.Fatalf("read body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"authentication required"}`)
	}))

	req := httptest.NewRequest(http.MethodPost, "/webhooks/slpa", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(httptest.NewRecorder(), req)

	systemLine, securityLine := decodeSystemAndSecurity(t, buf.Bytes())
	assertEqual(t, "system.category", systemLine["category"], "system")
	assertEqual(t, "system.callerKind", systemLine["callerKind"], "anonymous")
	assertEqual(t, "system.callerID", systemLine["callerID"], "")
	assertEqual(t, "system.status", systemLine["status"], float64(http.StatusUnauthorized))
	if _, ok := systemLine["requestBody"]; ok {
		t.Fatalf("requestBody logged: %v", systemLine["requestBody"])
	}
	if _, ok := systemLine["responseBody"]; ok {
		t.Fatalf("responseBody logged: %v", systemLine["responseBody"])
	}

	assertEqual(t, "security.category", securityLine["category"], "security")
	assertEqual(t, "security.msg", securityLine["msg"], "http denied")
	assertEqual(t, "security.reason", securityLine["reason"], "unauthenticated")
	assertEqual(t, "security.status", securityLine["status"], float64(http.StatusUnauthorized))
	assertEqual(t, "security.path", securityLine["path"], "/webhooks/slpa")
	assertKeysAbsent(t, securityLine, "requestBody", "responseBody", "query")
}

func TestMiddlewareEmitsSecurityOnForbidden(t *testing.T) {
	var buf bytes.Buffer
	logger := testLogger(&buf)

	h := Middleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if slot := authn.RequestCallerFrom(r.Context()); slot != nil {
			slot.Principal = &authn.Principal{Kind: authn.KindUser, UserID: "user-9"}
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/tasks/abc", nil))

	systemLine, securityLine := decodeSystemAndSecurity(t, buf.Bytes())
	assertEqual(t, "system.category", systemLine["category"], "system")
	assertEqual(t, "security.reason", securityLine["reason"], "forbidden")
	assertEqual(t, "security.callerID", securityLine["callerID"], "user-9")
	assertEqual(t, "security.status", securityLine["status"], float64(http.StatusForbidden))
}

func TestMiddlewareEmitsOneSecurityLineWhenAnnotated(t *testing.T) {
	var buf bytes.Buffer
	logger := testLogger(&buf)

	h := Middleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		securitylog.Annotate(r.Context(), "webhook auth failed",
			"reason", "webhook_auth_failed",
			"partner", "slpa",
			"error", errAnnotateTest,
		)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/webhooks/slpa", nil))

	systemLine, securityLine := decodeSystemAndSecurity(t, buf.Bytes())
	assertEqual(t, "system.status", systemLine["status"], float64(http.StatusUnauthorized))
	assertEqual(t, "security.msg", securityLine["msg"], "webhook auth failed")
	assertEqual(t, "security.reason", securityLine["reason"], "webhook_auth_failed")
	assertEqual(t, "security.partner", securityLine["partner"], "slpa")
	assertEqual(t, "security.error", securityLine["error"], errAnnotateTest.Error())
	assertEqual(t, "security.status", securityLine["status"], float64(http.StatusUnauthorized))
	assertEqual(t, "security.path", securityLine["path"], "/webhooks/slpa")
}

func TestMiddlewareEmitsSecurityWhenAnnotatedWithoutDenialStatus(t *testing.T) {
	var buf bytes.Buffer
	logger := testLogger(&buf)

	h := Middleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		securitylog.Annotate(r.Context(), "invalid callback token",
			"reason", "invalid_callback_token",
		)
		w.WriteHeader(http.StatusBadRequest)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/v1/callbacks/bad", nil))

	systemLine, securityLine := decodeSystemAndSecurity(t, buf.Bytes())
	assertEqual(t, "system.status", systemLine["status"], float64(http.StatusBadRequest))
	assertEqual(t, "security.msg", securityLine["msg"], "invalid callback token")
	assertEqual(t, "security.reason", securityLine["reason"], "invalid_callback_token")
	assertEqual(t, "security.path", securityLine["path"], "/api/v1/callbacks/{token}")
	assertKeysAbsent(t, securityLine, "partner")
}

func TestMiddlewareIgnoresInterimStatus(t *testing.T) {
	var buf bytes.Buffer
	logger := testLogger(&buf)

	dst := &finalStatus{ResponseWriter: httptest.NewRecorder()}
	h := Middleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusContinue)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	h.ServeHTTP(dst, httptest.NewRequest(http.MethodPost, "/api/v1/tasks/abc", nil))

	line := decodeLine(t, buf.Bytes())
	assertEqual(t, "status", line["status"], float64(http.StatusCreated))
	assertEqual(t, "responseBytes", line["responseBytes"], float64(len(`{"ok":true}`)))
	if _, ok := line["responseBody"]; ok {
		t.Fatalf("responseBody logged: %v", line["responseBody"])
	}
}

func TestMiddlewarePassesReadFromThrough(t *testing.T) {
	var buf bytes.Buffer
	logger := testLogger(&buf)
	const file = "file-bytes-not-for-the-log"
	dst := &sendFile{ResponseWriter: httptest.NewRecorder()}

	h := Middleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		n, err := io.Copy(w, plainReader{strings.NewReader(file)})
		if err != nil || n != int64(len(file)) {
			t.Fatalf("copy = %d, %v", n, err)
		}
	}))
	h.ServeHTTP(dst, httptest.NewRequest(http.MethodGet, "/api/v1/storage/k", nil))

	if !dst.used {
		t.Fatal("download did not use ReadFrom")
	}
	if dst.buf.String() != file {
		t.Fatalf("client got %q", dst.buf.String())
	}
	raw := buf.String()
	if strings.Contains(raw, file) {
		t.Fatal("security log copied the downloaded file")
	}
	line := decodeLine(t, []byte(raw))
	assertEqual(t, "responseBytes", line["responseBytes"], float64(len(file)))
}

func TestMiddlewareLogsWhenHandlerPanics(t *testing.T) {
	var buf bytes.Buffer
	logger := testLogger(&buf)
	h := Middleware(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/tasks/abc", nil))
	}()
	if !panicked {
		t.Fatal("panic was swallowed")
	}

	line := decodeLine(t, buf.Bytes())
	assertEqual(t, "path", line["path"], "/api/v1/tasks/abc")
	assertEqual(t, "callerKind", line["callerKind"], "anonymous")
	assertEqual(t, "status", line["status"], float64(http.StatusInternalServerError))
}

func TestMiddlewareSkipsHealth(t *testing.T) {
	var buf bytes.Buffer
	logger := testLogger(&buf)
	called := false
	h := Middleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))
	if !called {
		t.Fatal("health handler was not called")
	}
	if buf.Len() != 0 {
		t.Fatalf("health was logged: %s", buf.String())
	}
}

func TestMiddlewareDoesNotReadBody(t *testing.T) {
	var buf bytes.Buffer
	logger := testLogger(&buf)
	body := &countReader{r: strings.NewReader(`{"note":"secret payload"}`)}

	h := Middleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if body.n != 0 {
			t.Fatalf("body read before the handler decided, %d bytes", body.n)
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/abc", body)
	req.ContentLength = -1
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if body.n != 0 {
		t.Fatalf("middleware read %d bytes the handler never asked for", body.n)
	}
	systemLine, _ := decodeSystemAndSecurity(t, buf.Bytes())
	if _, ok := systemLine["requestBody"]; ok {
		t.Fatalf("logged a body: %v", systemLine["requestBody"])
	}
	if _, ok := systemLine["requestBytes"]; ok {
		t.Fatalf("requestBytes logged when the handler did not read: %v", systemLine["requestBytes"])
	}
}

func TestMiddlewareCountsUnknownLengthBody(t *testing.T) {
	var buf bytes.Buffer
	logger := testLogger(&buf)
	payload := strings.Repeat("a", 64)

	h := Middleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if string(got) != payload {
			t.Fatalf("handler body length = %d, want %d", len(got), len(payload))
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/abc", strings.NewReader(payload))
	req.ContentLength = -1
	req.Header.Set("Content-Type", "text/plain")
	h.ServeHTTP(httptest.NewRecorder(), req)

	line := decodeLine(t, buf.Bytes())
	assertEqual(t, "requestBytes", line["requestBytes"], float64(len(payload)))
	if _, ok := line["requestBody"]; ok {
		t.Fatalf("requestBody logged: %v", line["requestBody"])
	}
	if strings.Contains(buf.String(), payload) {
		t.Fatal("security log retained the payload")
	}
}

func TestMiddlewareRedactsCallbackTokenInPath(t *testing.T) {
	var buf bytes.Buffer
	logger := testLogger(&buf)
	const token = "callback-secret"

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/callbacks/{token}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/callbacks/"+token, nil)
	Middleware(logger)(mux).ServeHTTP(httptest.NewRecorder(), req)

	raw := buf.String()
	if strings.Contains(raw, token) {
		t.Fatalf("security log contained callback token: %s", raw)
	}
	line := decodeLine(t, []byte(raw))
	assertEqual(t, "path", line["path"], "/api/v1/callbacks/{token}")
}

// TestMiddlewareRedactsCallbackTokenThroughRequestCopy matches the real chain:
// Middleware outside a WithContext copy (otelhttp), with CaptureRoute next to
// the mux so pattern is still recorded.
func TestMiddlewareRedactsCallbackTokenThroughRequestCopy(t *testing.T) {
	var buf bytes.Buffer
	logger := testLogger(&buf)
	const token = "callback-secret"

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/callbacks/{token}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	// otelhttp copies via WithContext before the mux; path values land on that
	// copy only. CaptureRoute still sees Pattern on the copy and fills the slot.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		CaptureRoute(mux).ServeHTTP(w, r.WithContext(r.Context()))
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/callbacks/"+token, nil)
	Middleware(logger)(inner).ServeHTTP(httptest.NewRecorder(), req)

	raw := buf.String()
	if strings.Contains(raw, token) {
		t.Fatalf("security log contained callback token: %s", raw)
	}
	line := decodeLine(t, []byte(raw))
	assertEqual(t, "path", line["path"], "/api/v1/callbacks/{token}")
	assertEqual(t, "pattern", line["pattern"], "POST /api/v1/callbacks/{token}")
}

func TestMiddlewareLogsSwitchingProtocols(t *testing.T) {
	var buf bytes.Buffer
	logger := testLogger(&buf)
	h := Middleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ws", nil))

	line := decodeLine(t, buf.Bytes())
	assertEqual(t, "status", line["status"], float64(http.StatusSwitchingProtocols))
}

func TestMiddlewareCarriesTraceID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(logging.NewHandler(slog.NewJSONHandler(&buf, nil)))

	h := trace.TraceMiddleware(Middleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/abc", nil)
	req.Header.Set("X-Trace-ID", "trace-abc")
	h.ServeHTTP(httptest.NewRecorder(), req)

	line := decodeLine(t, buf.Bytes())
	assertEqual(t, "traceId", line["traceId"], "trace-abc")
	assertEqual(t, "callerKind", line["callerKind"], "anonymous")
	assertEqual(t, "status", line["status"], float64(http.StatusNoContent))
}

type countReader struct {
	r io.Reader
	n int
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

type finalStatus struct {
	http.ResponseWriter
	code int
}

func (f *finalStatus) WriteHeader(code int) {
	if code >= 100 && code <= 199 {
		return
	}
	f.code = code
	f.ResponseWriter.WriteHeader(code)
}

type plainReader struct{ io.Reader }

type sendFile struct {
	http.ResponseWriter
	buf  bytes.Buffer
	used bool
}

func (s *sendFile) ReadFrom(r io.Reader) (int64, error) {
	s.used = true
	return io.Copy(&s.buf, r)
}

func decodeLine(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var line map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &line); err != nil {
		t.Fatalf("decode log line %q: %v", raw, err)
	}
	return line
}

func decodeSystemAndSecurity(t *testing.T, raw []byte) (system, security map[string]any) {
	t.Helper()
	parts := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
	if len(parts) != 2 {
		t.Fatalf("want system + security lines, got %d: %s", len(parts), raw)
	}
	system = decodeLine(t, parts[0])
	security = decodeLine(t, parts[1])
	if system["category"] != "system" || security["category"] != "security" {
		t.Fatalf("line categories = %v, %v", system["category"], security["category"])
	}
	return system, security
}

func assertEqual(t *testing.T, name string, got, want any) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %#v, want %#v", name, got, want)
	}
}
