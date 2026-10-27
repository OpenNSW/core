// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package telemetry sets up OpenTelemetry for a service: a tracer provider
// that always creates spans and trace IDs, and, when an OTLP endpoint is
// configured, metric, trace and log export over OTLP/HTTP. The span's trace
// ID is the request's correlation ID.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// TraceIDHeader is the response header Handler sets to the request's trace ID
// so a client can quote it.
const TraceIDHeader = "X-Trace-ID"

// propagator is the W3C trace context propagator used for incoming and
// outgoing requests.
var propagator = propagation.TraceContext{}

// Provider owns the meter, tracer and logger providers.
type Provider struct {
	mp          metric.MeterProvider
	tp          *sdktrace.TracerProvider
	lp          *sdklog.LoggerProvider // nil when logs are not exported
	serviceName string
	shutdown    func(context.Context) error
}

// Start builds the resource and the meter, tracer and logger providers,
// registers the tracer provider, the meter provider (when metrics are
// exported) and the W3C propagator globally, and returns a Provider.
//
// A tracer provider is installed even when export is off, so every request
// still gets a span and a trace ID for logs and error responses. Each signal
// is exported only when Config resolves an endpoint for it.
func Start(ctx context.Context, cfg Config) (*Provider, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	eps, err := cfg.endpoints()
	if err != nil {
		return nil, err
	}
	res, err := cfg.resource(ctx)
	if err != nil {
		return nil, err
	}
	// Every exporter gets WithHeaders, even with no headers, so
	// OTEL_EXPORTER_OTLP_HEADERS can't add any that Config doesn't show.
	headers := cfg.Headers

	p := &Provider{
		mp:          metricnoop.NewMeterProvider(),
		serviceName: strings.TrimSpace(cfg.ServiceName),
	}
	var shutdowns []func(context.Context) error
	fail := func(err error) (*Provider, error) {
		for _, shutdown := range shutdowns {
			_ = shutdown(ctx)
		}
		return nil, err
	}

	traceOpts := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
	if eps.traces != "" {
		exp, err := otlptracehttp.New(ctx,
			otlptracehttp.WithEndpointURL(eps.traces),
			otlptracehttp.WithHeaders(headers),
		)
		if err != nil {
			return fail(fmt.Errorf("telemetry: create OTLP trace exporter: %w", err))
		}
		traceOpts = append(traceOpts, sdktrace.WithBatcher(exp))
	}
	p.tp = sdktrace.NewTracerProvider(traceOpts...)
	shutdowns = append(shutdowns, p.tp.Shutdown)

	if eps.metrics != "" {
		exp, err := otlpmetrichttp.New(ctx,
			otlpmetrichttp.WithEndpointURL(eps.metrics),
			otlpmetrichttp.WithHeaders(headers),
		)
		if err != nil {
			return fail(fmt.Errorf("telemetry: create OTLP metric exporter: %w", err))
		}
		mp := sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp)),
			sdkmetric.WithResource(res),
			sdkmetric.WithView(httpServerAttributeView()),
		)
		p.mp = mp
		shutdowns = append(shutdowns, mp.Shutdown)
	}

	if eps.logs != "" {
		exp, err := otlploghttp.New(ctx,
			otlploghttp.WithEndpointURL(eps.logs),
			otlploghttp.WithHeaders(headers),
		)
		if err != nil {
			return fail(fmt.Errorf("telemetry: create OTLP log exporter: %w", err))
		}
		p.lp = sdklog.NewLoggerProvider(
			sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)),
			sdklog.WithResource(res),
		)
		shutdowns = append(shutdowns, p.lp.Shutdown)
	}

	p.shutdown = func(ctx context.Context) error {
		var errs []error
		for _, shutdown := range shutdowns {
			errs = append(errs, shutdown(ctx))
		}
		return errors.Join(errs...)
	}

	otel.SetTracerProvider(p.tp)
	if eps.metrics != "" {
		otel.SetMeterProvider(p.mp)
	}
	otel.SetTextMapPropagator(propagator)
	return p, nil
}

// HandlerOption configures Handler.
type HandlerOption func(*handlerConfig)

type handlerConfig struct {
	trustedParent func(*http.Request) bool // nil: every request is public
}

// WithTrustedParent makes the handler continue the caller's trace from an
// incoming traceparent header on every request. Use it only for handlers
// reached by services you control. By default a handler is a public endpoint:
// it starts a new root span and only links an incoming traceparent, so a
// caller can't choose the trace ID or turn sampling off.
func WithTrustedParent() HandlerOption {
	return WithTrustedParentFunc(func(*http.Request) bool { return true })
}

// WithTrustedParentFunc makes the handler continue the caller's trace for the
// requests trusted reports true for, and treat the rest as public, so one
// handler can serve both internal and public routes. trusted runs before the
// wrapped handler, so it sees the raw request (path, headers, TLS state) but
// not r.Pattern or anything middleware adds to the context. It must check
// something an outside caller can't fake, such as a path that outside traffic
// can't reach or a verified TLS client certificate.
func WithTrustedParentFunc(trusted func(*http.Request) bool) HandlerOption {
	return func(c *handlerConfig) { c.trustedParent = trusted }
}

// Handler wraps next with otelhttp: one server span and HTTP metrics per
// request, with http.route taken from the ServeMux pattern. The response
// carries the trace ID in the X-Trace-ID header. Make Handler the outermost
// layer so other middleware runs inside the span; if any of it replaces the
// request, wrap the mux with Routes.
func (p *Provider) Handler(operation string, next http.Handler, opts ...HandlerOption) http.Handler {
	var hc handlerConfig
	for _, opt := range opts {
		opt(&hc)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := TraceID(r.Context()); id != "" {
			w.Header().Set(TraceIDHeader, id)
		}
		next.ServeHTTP(w, r)
		labelRoute(r)
	})

	otelOpts := []otelhttp.Option{
		otelhttp.WithMeterProvider(p.mp),
		otelhttp.WithTracerProvider(p.tp),
		otelhttp.WithPropagators(propagator),
	}
	public := func(*http.Request) bool { return true }
	if trusted := hc.trustedParent; trusted != nil {
		public = func(r *http.Request) bool { return !trusted(r) }
	}
	otelOpts = append(otelOpts, otelhttp.WithPublicEndpointFn(public))
	return otelhttp.NewHandler(inner, operation, otelOpts...)
}

// Routes wraps a mux so its matched pattern reaches Handler's metrics as
// http.route. Handler does this itself when it wraps the mux directly. When
// middleware between them replaces the request (r.WithContext), the mux sets
// Pattern on a copy Handler never sees, so wrap the mux with Routes:
//
//	tel.Handler("api", middleware(telemetry.Routes(mux)))
//
// Outside a Handler it only calls next.
func Routes(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		labelRoute(r)
	})
}

// labelRoute adds http.route from r.Pattern to the metrics of the request's
// Handler. otelhttp does not set it on metrics; it records after the wrapped
// handler returns, and the labeler travels in the context, so adding it once
// the mux has set Pattern is in time.
func labelRoute(r *http.Request) {
	route := routeFromPattern(r.Pattern)
	if route == "" {
		return
	}
	if labeler, ok := otelhttp.LabelerFromContext(r.Context()); ok {
		labeler.Add(semconv.HTTPRoute(route))
	}
}

// Transport wraps base so outgoing calls get a client span and HTTP client
// metrics and carry traceparent. A nil base uses http.DefaultTransport.
func (p *Provider) Transport(base http.RoundTripper) http.RoundTripper {
	return otelhttp.NewTransport(base,
		otelhttp.WithMeterProvider(p.mp),
		otelhttp.WithTracerProvider(p.tp),
		otelhttp.WithPropagators(propagator),
	)
}

// SlogHandler returns an slog handler that exports records over OTLP, or nil
// when export is off.
func (p *Provider) SlogHandler() slog.Handler {
	if p.lp == nil {
		return nil
	}
	return otelslog.NewHandler(p.serviceName, otelslog.WithLoggerProvider(p.lp))
}

// Shutdown flushes and stops every provider.
func (p *Provider) Shutdown(ctx context.Context) error {
	return p.shutdown(ctx)
}

// TraceID returns the trace ID of the span in ctx as 32 hex characters, or ""
// when ctx has no span. Use it as the correlation ID, e.g.
// httputil.CorrelationIDFunc = telemetry.TraceID.
func TraceID(ctx context.Context) string {
	sc := oteltrace.SpanContextFromContext(ctx)
	if !sc.HasTraceID() {
		return ""
	}
	return sc.TraceID().String()
}

// httpServerAttributeView drops server.address and server.port from HTTP
// server metrics. otelhttp takes those from the request Host header, so a
// client could create a new series per value; they are constant per service.
func httpServerAttributeView() sdkmetric.View {
	return sdkmetric.NewView(
		sdkmetric.Instrument{Name: "http.server.*"},
		sdkmetric.Stream{AttributeFilter: attribute.NewDenyKeysFilter(
			semconv.ServerAddressKey, semconv.ServerPortKey,
		)},
	)
}

// routeFromPattern strips the method and host from a ServeMux pattern.
func routeFromPattern(pattern string) string {
	if idx := strings.IndexByte(pattern, '/'); idx >= 0 {
		return pattern[idx:]
	}
	return ""
}
