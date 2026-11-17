// Package systemlog records every inbound HTTP request as a slog line with
// category=system, except GET /health. The line names who called (caller
// kind and id), the route, status, and duration. Request and response
// payloads are not logged: forms are JSON Schema–driven (jsonforms), so field-
// level redaction cannot list every PII key. RemoteAddr is omitted: behind
// ingress it is the proxy, and a real client IP would be PII in the pod log.
//
// Authorization headers are not logged. The callback token in
// /api/v1/callbacks/{token} is replaced in the path.
//
// 401 and 403 responses also emit a sparse category=security line (see
// package securitylog) for alerting; every other status is system-only.
package systemlog

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/felixge/httpsnoop"

	"github.com/OpenNSW/nsw-srilanka/internal/authn"
	"github.com/OpenNSW/nsw-srilanka/internal/securitylog"
)

// Category is the slog attribute value on every request line.
const Category = "system"

// callbacksPathPrefix is the path prefix of POST /api/v1/callbacks/{token}.
// Redaction uses the prefix rather than PathValue("token"): otelhttp copies
// the request before the mux runs, so path values never appear on the
// request Middleware holds.
const callbacksPathPrefix = "/api/v1/callbacks/"

// Middleware logs one system line after next returns, including when next
// panics. logger nil uses slog.Default. The logger must already be the process
// handler (with StampCategory) so the line picks up category=system and the
// request trace id. GET /health is served and not logged.
//
// Wrap the mux with CaptureRoute so pattern is recorded when Middleware sits
// outside otelhttp (which copies the request before ServeMux sets Pattern).
func Middleware(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" {
				next.ServeHTTP(w, r)
				return
			}

			caller := &authn.RequestCaller{}
			route := &matchedRoute{}
			ann := &securitylog.Annotation{}
			ctx := authn.WithRequestCaller(r.Context(), caller)
			ctx = securitylog.WithAnnotation(ctx, ann)
			ctx = withMatchedRoute(ctx, route)
			r = r.WithContext(ctx)

			// Count bytes as the handler reads. Declared Content-Length wins;
			// chunked / unknown length uses the count. Nothing is buffered.
			reqBody := wrapRequestBody(r)

			resp := &responseLog{}
			wrapped := httpsnoop.Wrap(w, resp.hooks())
			start := time.Now()

			defer func() {
				panicked := recover()
				kind, id := callerKindAndID(caller)
				// A handler that returns without writing is answered 200 by
				// net/http. A panic before any write is not: the server aborts
				// the connection. Log that as 500, then re-panic so the server
				// still handles it.
				status := responseStatus(resp.status, panicked)
				path := loggedPath(r.URL.Path)
				pattern := r.Pattern
				if pattern == "" {
					pattern = route.pattern
				}
				logger.InfoContext(r.Context(), "http request", systemAttrs(
					r, path, pattern, kind, id, status,
					time.Since(start).Milliseconds(),
					requestBytes(r.ContentLength, reqBody), int64(resp.n),
				)...)
				emitSecurityDenial(r.Context(), logger, r, path, kind, id, status, ann)
				if panicked != nil {
					panic(panicked)
				}
			}()

			next.ServeHTTP(wrapped, r)
		})
	}
}

func callerKindAndID(caller *authn.RequestCaller) (kind, id string) {
	kind, id = "anonymous", ""
	if caller != nil && caller.Principal != nil {
		kind = string(caller.Principal.Kind)
		id = caller.Principal.Subject()
	}
	return kind, id
}

func responseStatus(written int, panicked any) int {
	if written != 0 {
		return written
	}
	if panicked != nil {
		return http.StatusInternalServerError
	}
	return http.StatusOK
}

func systemAttrs(
	r *http.Request, path, pattern, kind, id string,
	status int, durationMs, reqN, respN int64,
) []any {
	attrs := []any{
		"method", r.Method,
		"path", path,
		"callerKind", kind,
		"callerID", id,
		"status", status,
		"durationMs", durationMs,
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		attrs = append(attrs, "origin", origin)
	}
	if reqN > 0 {
		attrs = append(attrs, "requestBytes", reqN)
	}
	if pattern != "" {
		attrs = append(attrs, "pattern", pattern)
	}
	if respN > 0 {
		attrs = append(attrs, "responseBytes", respN)
	}
	return attrs
}

// emitSecurityDenial writes one security line for a 401/403, or when a handler
// Annotate'd (e.g. invalid callback token at 400). Handlers only fill the slot;
// they never call Event themselves.
func emitSecurityDenial(
	ctx context.Context, base *slog.Logger, r *http.Request,
	path, kind, id string, status int, ann *securitylog.Annotation,
) {
	if status != http.StatusUnauthorized && status != http.StatusForbidden && !ann.Annotated() {
		return
	}
	msg, reason := "http denied", "forbidden"
	if status == http.StatusUnauthorized {
		reason = "unauthenticated"
	}
	if ann.Msg != "" {
		msg = ann.Msg
	}
	if ann.Reason != "" {
		reason = ann.Reason
	}
	attrs := []any{
		"method", r.Method,
		"path", path,
		"callerKind", kind,
		"callerID", id,
		"status", status,
		"reason", reason,
	}
	if ann.Partner != "" {
		attrs = append(attrs, "partner", ann.Partner)
	}
	if ann.Err != nil {
		attrs = append(attrs, "error", ann.Err)
	}
	securitylog.Event(ctx, base, msg, attrs...)
}

// CaptureRoute records the ServeMux pattern onto the matchedRoute slot that
// Middleware put in the context. Place it next to the mux so Middleware
// outside otelhttp still sees pattern after the handler returns.
func CaptureRoute(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		if m := matchedRouteFrom(r.Context()); m != nil {
			m.pattern = r.Pattern
		}
	})
}

// matchedRoute is a slot Middleware allocates before ServeHTTP. CaptureRoute
// fills pattern after the mux runs.
type matchedRoute struct {
	pattern string
}

type matchedRouteKey struct{}

func withMatchedRoute(ctx context.Context, slot *matchedRoute) context.Context {
	return context.WithValue(ctx, matchedRouteKey{}, slot)
}

func matchedRouteFrom(ctx context.Context) *matchedRoute {
	slot, _ := ctx.Value(matchedRouteKey{}).(*matchedRoute)
	return slot
}

// wrapRequestBody installs a counter on r.Body when present. It does not read.
func wrapRequestBody(r *http.Request) *countingBody {
	if r.Body == nil || r.Body == http.NoBody {
		return nil
	}
	body := &countingBody{ReadCloser: r.Body}
	r.Body = body
	return body
}

// requestBytes prefers a declared Content-Length. Otherwise it is how much
// the handler actually read (chunked / unknown length).
func requestBytes(contentLength int64, body *countingBody) int64 {
	if contentLength >= 0 {
		return contentLength
	}
	if body == nil {
		return 0
	}
	return body.n
}

// countingBody counts bytes the handler reads without retaining them.
type countingBody struct {
	io.ReadCloser
	n int64
}

func (c *countingBody) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	if n > 0 {
		c.n += int64(n)
	}
	return n, err
}

// loggedPath hides the callback token by path prefix. PathValue is empty on
// Middleware's request when otelhttp (or any WithContext copy) sits between
// this middleware and the mux.
func loggedPath(path string) string {
	if after, ok := strings.CutPrefix(path, callbacksPathPrefix); ok && after != "" {
		return callbacksPathPrefix + "{token}"
	}
	return path
}

// responseLog records the status and response size. Bodies are not copied.
// ReadFrom is delegated unchanged so a file download can still use sendfile.
type responseLog struct {
	status   int
	wroteHdr bool
	n        int
}

func (rec *responseLog) hooks() httpsnoop.Hooks {
	return httpsnoop.Hooks{
		WriteHeader: func(next httpsnoop.WriteHeaderFunc) httpsnoop.WriteHeaderFunc {
			return func(code int) {
				next(code)
				// 101 is the final status of an upgrade. Other 1xx codes are
				// interim and must not hide the status that follows.
				if code == http.StatusSwitchingProtocols {
					rec.status = code
					rec.wroteHdr = true
					return
				}
				if code >= 100 && code <= 199 || rec.wroteHdr {
					return
				}
				rec.status = code
				rec.wroteHdr = true
			}
		},
		Write: func(next httpsnoop.WriteFunc) httpsnoop.WriteFunc {
			return func(p []byte) (int, error) {
				n, err := next(p)
				if n > 0 {
					rec.n += n
					rec.wroteHdr = true
				}
				return n, err
			}
		},
		WriteString: func(next httpsnoop.WriteStringFunc) httpsnoop.WriteStringFunc {
			return func(s string) (int, error) {
				n, err := next(s)
				if n > 0 {
					rec.n += n
					rec.wroteHdr = true
				}
				return n, err
			}
		},
		ReadFrom: func(next httpsnoop.ReadFromFunc) httpsnoop.ReadFromFunc {
			return func(src io.Reader) (int64, error) {
				n, err := next(src)
				if n > 0 {
					rec.n += int(n)
					rec.wroteHdr = true
				}
				return n, err
			}
		},
	}
}
