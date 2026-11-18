package storage

import (
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
)

// TestRoutes checks the storage routes are valid ServeMux patterns under
// RoutePrefix, that they and the local content routes can all be mounted on
// one mux (ServeMux panics on an invalid or conflicting pattern), and that
// each routes the request it should.
func TestRoutes(t *testing.T) {
	if !strings.HasPrefix(RoutePrefix, "/") || path.Clean(RoutePrefix) != RoutePrefix {
		t.Fatalf("RoutePrefix %q is not a clean absolute path", RoutePrefix)
	}

	routes := map[string]struct {
		method, path string
	}{
		UploadRoute:   {http.MethodPost, "/api/v1/storage"},
		DownloadRoute: {http.MethodGet, "/api/v1/storage/{key}"},
	}

	mux := http.NewServeMux()
	for pattern, want := range routes {
		method, p, ok := strings.Cut(pattern, " ")
		if !ok || method != want.method || p != want.path {
			t.Errorf("route %q, want %s %s", pattern, want.method, want.path)
		}
		if path.Clean(p) != p {
			t.Errorf("route %q: path %q is not clean", pattern, p)
		}
		if !underPrefix(p, RoutePrefix) {
			t.Errorf("route %q is not under RoutePrefix %q", pattern, RoutePrefix)
		}
		mount(t, mux, pattern)
	}

	checkInternalRoute(t, mux)

	// The local content routes go on the same mux in app.go.
	stack, err := New(t.Context(), localConfig(t, "http://localhost:8080"), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("local content routes conflict with the storage routes: %v", r)
			}
		}()
		stack.LocalContent.RegisterRoutes(mux)
	}()

	// A file name as Upload creates them: a UUID plus its extension.
	const file = "550e8400-e29b-41d4-a716-446655440000.pdf"
	for _, tc := range []struct {
		method, target, wantPattern string
	}{
		{http.MethodPost, "/api/v1/storage", UploadRoute},
		{http.MethodPost, "/api/v1/internal/storage", InternalUploadRoute},
		{http.MethodGet, path.Join("/api/v1/storage", file), DownloadRoute},
		{http.MethodPut, path.Join("/api/v1/storage", file, "content"), "PUT /api/v1/storage/{key}/content"},
		{http.MethodGet, path.Join("/api/v1/storage", file, "content"), "GET /api/v1/storage/{key}/content"},
	} {
		if _, got := mux.Handler(httptest.NewRequest(tc.method, tc.target, nil)); got != tc.wantPattern {
			t.Errorf("%s %s routes to %q, want %q", tc.method, tc.target, got, tc.wantPattern)
		}
	}

	// Delete is not mounted: the path exists for GET, so DELETE is refused.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, path.Join("/api/v1/storage", file), nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE %s: status %d, want %d", path.Join("/api/v1/storage", file), rec.Code, http.StatusMethodNotAllowed)
	}
}

// checkInternalRoute checks the internal upload route is TNSW's alone: a clean
// POST path outside RoutePrefix, where no wildcard route can claim it, that
// mounts on mux.
func checkInternalRoute(t *testing.T, mux *http.ServeMux) {
	t.Helper()
	method, p, _ := strings.Cut(InternalUploadRoute, " ")
	if method != http.MethodPost || p != "/api/v1/internal/storage" || path.Clean(p) != p {
		t.Errorf("route %q, want POST /api/v1/internal/storage", InternalUploadRoute)
	}
	if underPrefix(p, RoutePrefix) {
		t.Errorf("route %q is under RoutePrefix %q", InternalUploadRoute, RoutePrefix)
	}
	mount(t, mux, InternalUploadRoute)
}

// mount registers pattern on mux, failing the test instead of panicking when
// ServeMux rejects it.
func mount(t *testing.T, mux *http.ServeMux, pattern string) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ServeMux rejects route %q: %v", pattern, r)
		}
	}()
	mux.Handle(pattern, http.NotFoundHandler())
}

// underPrefix reports whether p is prefix itself or a path below it.
func underPrefix(p, prefix string) bool {
	rest, ok := strings.CutPrefix(p, prefix)
	return ok && (rest == "" || strings.HasPrefix(rest, "/"))
}
