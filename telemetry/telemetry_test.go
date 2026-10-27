// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

const (
	remoteTraceID = "11111111111111111111111111111111"
	remoteSpanID  = "2222222222222222"
)

func TestStartWithoutEndpointStillTraces(t *testing.T) {
	p, err := Start(t.Context(), Config{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := p.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	if p.SlogHandler() != nil {
		t.Fatal("SlogHandler should be nil when export is off")
	}

	var inHandler string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		inHandler = TraceID(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	p.Handler("api", mux).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(inHandler) != 32 {
		t.Fatalf("TraceID in handler = %q, want 32 hex characters", inHandler)
	}
	if got := rec.Header().Get(TraceIDHeader); got != inHandler {
		t.Fatalf("%s = %q, want %q", TraceIDHeader, got, inHandler)
	}
}

func TestStartWithEndpointExportsEverySignal(t *testing.T) {
	c := newCollector(t)
	p, err := Start(t.Context(), Config{
		Endpoint:    c.URL,
		ServiceName: "svc",
		Headers:     map[string]string{"Authorization": "Basic abc"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	exercise(t, p)
	if err := p.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	for _, path := range []string{"/v1/traces", "/v1/metrics", "/v1/logs"} {
		if !c.got(path) {
			t.Errorf("no export to %s; got %v", path, c.paths())
		}
	}
	if got := c.header("Authorization"); got != "Basic abc" {
		t.Errorf("Authorization = %q, want Basic abc", got)
	}
}

func TestStartExportsOnlyConfiguredSignals(t *testing.T) {
	c := newCollector(t)
	p, err := Start(t.Context(), Config{Traces: SignalConfig{Endpoint: c.URL + "/custom/traces"}, ServiceName: "svc"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if p.SlogHandler() != nil {
		t.Fatal("SlogHandler should be nil when logs are not exported")
	}
	exercise(t, p)
	if err := p.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	if paths := c.paths(); len(paths) != 1 || paths[0] != "/custom/traces" {
		t.Fatalf("export paths = %v, want only /custom/traces", paths)
	}
}

func TestStartIgnoresOwnedEnv(t *testing.T) {
	stray := newCollector(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", stray.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", stray.URL+"/v1/traces")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "x-stray=1")

	t.Run("export off", func(t *testing.T) {
		p, err := Start(t.Context(), Config{})
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		exercise(t, p)
		if err := p.Shutdown(t.Context()); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		if paths := stray.paths(); len(paths) != 0 {
			t.Fatalf("exported to OTEL_* endpoint: %v", paths)
		}
	})

	t.Run("endpoint configured", func(t *testing.T) {
		c := newCollector(t)
		p, err := Start(t.Context(), Config{Endpoint: c.URL, ServiceName: "svc"})
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		exercise(t, p)
		if err := p.Shutdown(t.Context()); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		if paths := stray.paths(); len(paths) != 0 {
			t.Fatalf("exported to OTEL_* endpoint: %v", paths)
		}
		if !c.got("/v1/traces") {
			t.Fatalf("traces not exported to Config.Endpoint; got %v", c.paths())
		}
		if got := c.header("X-Stray"); got != "" {
			t.Fatalf("OTEL_EXPORTER_OTLP_HEADERS leaked: x-stray=%q", got)
		}
	})
}

func TestStartRejectsInvalidConfig(t *testing.T) {
	if _, err := Start(t.Context(), Config{Endpoint: "http://otel-collector:4318"}); err == nil {
		t.Fatal("Start without a service name should fail when export is on")
	}
}

// collector is a fake OTLP/HTTP backend that records each export's path and
// headers.
type collector struct {
	*httptest.Server
	mu       sync.Mutex
	requests []*http.Request
}

func newCollector(t *testing.T) *collector {
	t.Helper()
	c := &collector{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.requests = append(c.requests, r)
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *collector) paths() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var paths []string
	for _, r := range c.requests {
		if !slices.Contains(paths, r.URL.Path) {
			paths = append(paths, r.URL.Path)
		}
	}
	return paths
}

func (c *collector) got(path string) bool {
	return slices.Contains(c.paths(), path)
}

// header returns the value of key on the first export, or "".
func (c *collector) header(key string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.requests) == 0 {
		return ""
	}
	return c.requests[0].Header.Get(key)
}

// exercise produces a span, HTTP metrics and, when logs are exported, a log
// record through p.
func exercise(t *testing.T, p *Provider) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if h := p.SlogHandler(); h != nil {
			slog.New(h).InfoContext(r.Context(), "health")
		}
		w.WriteHeader(http.StatusOK)
	})
	p.Handler("api", mux).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))
}

func TestHandlerRecordsRoutePattern(t *testing.T) {
	p, reader, _ := testProvider(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/tasks/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	rec := httptest.NewRecorder()
	p.Handler("api", mux).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/tasks/abc-123", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}

	attrs := durationDataPoints(t, reader)[0].Attributes
	if got, _ := attrs.Value(semconv.HTTPRouteKey); got.AsString() != "/api/v1/tasks/{id}" {
		t.Fatalf("http.route = %q, want /api/v1/tasks/{id}", got.AsString())
	}
	if got, _ := attrs.Value(semconv.HTTPRequestMethodKey); got.AsString() != http.MethodPost {
		t.Fatalf("http.request.method = %q, want POST", got.AsString())
	}
	if got, _ := attrs.Value(semconv.HTTPResponseStatusCodeKey); got.AsInt64() != http.StatusCreated {
		t.Fatalf("http.response.status_code = %d, want 201", got.AsInt64())
	}
}

func TestRoutesLabelsThroughReplacedRequest(t *testing.T) {
	type ctxKey struct{}
	// replaceRequest stands in for middleware that derives a new request, as
	// authentication or request logging middleware do.
	replaceRequest := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, true)))
		})
	}
	newMux := func() *http.ServeMux {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/v1/tasks/{id}", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		return mux
	}

	tests := []struct {
		name      string
		wrap      func(*http.ServeMux) http.Handler
		wantRoute string
	}{
		{
			name:      "without Routes the pattern is lost",
			wrap:      func(mux *http.ServeMux) http.Handler { return replaceRequest(mux) },
			wantRoute: "",
		},
		{
			name:      "Routes inside the middleware",
			wrap:      func(mux *http.ServeMux) http.Handler { return replaceRequest(Routes(mux)) },
			wantRoute: "/api/v1/tasks/{id}",
		},
		{
			name:      "Routes directly under Handler",
			wrap:      func(mux *http.ServeMux) http.Handler { return Routes(mux) },
			wantRoute: "/api/v1/tasks/{id}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, reader, _ := testProvider(t)
			h := p.Handler("api", tt.wrap(newMux()))
			for _, id := range []string{"a", "b"} {
				h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+id, nil))
			}

			points := durationDataPoints(t, reader)
			if len(points) != 1 {
				t.Fatalf("got %d series, want 1", len(points))
			}
			got, _ := points[0].Attributes.Value(semconv.HTTPRouteKey)
			if got.AsString() != tt.wantRoute {
				t.Fatalf("http.route = %q, want %q", got.AsString(), tt.wantRoute)
			}
		})
	}
}

func TestRoutesOutsideHandler(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	Routes(mux).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
}

func TestHandlerDropsServerAddressFromHost(t *testing.T) {
	p, reader, _ := testProvider(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := p.Handler("api", mux)

	for _, host := range []string{"a.example", "b.example:8443", "c.example:443"} {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		req.Host = host
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	points := durationDataPoints(t, reader)
	if len(points) != 1 {
		t.Fatalf("got %d series, want 1 (server.address/port must not split by Host)", len(points))
	}
	for _, key := range []attribute.Key{semconv.ServerAddressKey, semconv.ServerPortKey} {
		if v, ok := points[0].Attributes.Value(key); ok {
			t.Fatalf("unexpected attribute %s=%v", key, v.AsInterface())
		}
	}
}

func TestHandlerPublicEndpointOnlyLinksCaller(t *testing.T) {
	p, _, spans := testProvider(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// sampled=0 from the caller must not drop or parent our span.
	req.Header.Set("traceparent", "00-"+remoteTraceID+"-"+remoteSpanID+"-00")
	rec := httptest.NewRecorder()
	p.Handler("api", http.NotFoundHandler()).ServeHTTP(rec, req)

	got := spans.GetSpans()
	if len(got) != 1 {
		t.Fatalf("spans = %d, want 1", len(got))
	}
	span := got[0]
	if span.Parent.IsValid() {
		t.Fatalf("parent = %s, want none", span.Parent.TraceID())
	}
	if span.SpanContext.TraceID().String() == remoteTraceID {
		t.Fatal("public endpoint took the caller's trace ID")
	}
	if len(span.Links) != 1 || span.Links[0].SpanContext.TraceID().String() != remoteTraceID {
		t.Fatalf("links = %+v, want one link to the caller's span", span.Links)
	}
	if got := rec.Header().Get(TraceIDHeader); got != span.SpanContext.TraceID().String() {
		t.Fatalf("%s = %q, want the span's trace ID", TraceIDHeader, got)
	}
}

func TestHandlerTrustedParentContinuesTrace(t *testing.T) {
	p, _, spans := testProvider(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("traceparent", "00-"+remoteTraceID+"-"+remoteSpanID+"-01")
	rec := httptest.NewRecorder()
	p.Handler("api", http.NotFoundHandler(), WithTrustedParent()).ServeHTTP(rec, req)

	got := spans.GetSpans()
	if len(got) != 1 {
		t.Fatalf("spans = %d, want 1", len(got))
	}
	if id := got[0].SpanContext.TraceID().String(); id != remoteTraceID {
		t.Fatalf("trace id = %s, want %s", id, remoteTraceID)
	}
	if id := got[0].Parent.SpanID().String(); id != remoteSpanID {
		t.Fatalf("parent span id = %s, want %s", id, remoteSpanID)
	}
	if got := rec.Header().Get(TraceIDHeader); got != remoteTraceID {
		t.Fatalf("%s = %q, want %q", TraceIDHeader, got, remoteTraceID)
	}
}

func TestHandlerTrustedParentFuncDecidesPerRequest(t *testing.T) {
	p, _, spans := testProvider(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/quote", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /api/v1/prices", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := p.Handler("api", mux, WithTrustedParentFunc(func(r *http.Request) bool {
		return strings.HasPrefix(r.URL.Path, "/internal/")
	}))

	tests := []struct {
		method, path string
		wantTrusted  bool
	}{
		{http.MethodPost, "/internal/quote", true},
		{http.MethodGet, "/api/v1/prices", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			spans.Reset()
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("traceparent", "00-"+remoteTraceID+"-"+remoteSpanID+"-01")
			h.ServeHTTP(httptest.NewRecorder(), req)

			got := spans.GetSpans()
			if len(got) != 1 {
				t.Fatalf("spans = %d, want 1", len(got))
			}
			continued := got[0].SpanContext.TraceID().String() == remoteTraceID
			if continued != tt.wantTrusted {
				t.Fatalf("continued caller's trace = %v, want %v", continued, tt.wantTrusted)
			}
			if !tt.wantTrusted && len(got[0].Links) != 1 {
				t.Fatalf("links = %d, want 1 to the caller's span", len(got[0].Links))
			}
		})
	}
}

func TestTransportPropagatesTrace(t *testing.T) {
	p, _, spans := testProvider(t)
	var traceparent string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceparent = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	ctx, parent := p.tp.Tracer("test").Start(t.Context(), "parent")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Transport: p.Transport(nil)}).Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	parent.End()

	traceID := parent.SpanContext().TraceID().String()
	if len(traceparent) != 55 || traceparent[3:35] != traceID {
		t.Fatalf("traceparent = %q, want trace id %s", traceparent, traceID)
	}
	if n := len(spans.GetSpans()); n != 2 {
		t.Fatalf("spans = %d, want 2 (parent and client)", n)
	}
}

func TestTraceIDWithoutSpan(t *testing.T) {
	if got := TraceID(t.Context()); got != "" {
		t.Fatalf("TraceID = %q, want empty", got)
	}
}

func TestRouteFromPattern(t *testing.T) {
	tests := map[string]string{
		"GET /api/v1/tasks/{id}": "/api/v1/tasks/{id}",
		"/health":                "/health",
		"example.com/health":     "/health",
		"":                       "",
	}
	for pattern, want := range tests {
		if got := routeFromPattern(pattern); got != want {
			t.Errorf("routeFromPattern(%q) = %q, want %q", pattern, got, want)
		}
	}
}

// testProvider returns a Provider that records metrics into a manual reader
// and spans into an in-memory exporter.
func testProvider(t *testing.T) (*Provider, *sdkmetric.ManualReader, *tracetest.InMemoryExporter) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(reader),
		sdkmetric.WithView(httpServerAttributeView()),
	)
	spans := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans))
	p := &Provider{
		mp: mp,
		tp: tp,
		shutdown: func(ctx context.Context) error {
			return errors.Join(mp.Shutdown(ctx), tp.Shutdown(ctx))
		},
	}
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	return p, reader, spans
}

func durationDataPoints(t *testing.T, reader *sdkmetric.ManualReader) []metricdata.HistogramDataPoint[float64] {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "http.server.request.duration" {
				continue
			}
			hist, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("unexpected data type %T", m.Data)
			}
			if len(hist.DataPoints) == 0 {
				t.Fatal("no histogram points")
			}
			return hist.DataPoints
		}
	}
	t.Fatal("http.server.request.duration not found")
	return nil
}
