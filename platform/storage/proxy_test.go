package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	coreauthn "github.com/OpenNSW/core/authn"
	"github.com/OpenNSW/core/remote"
	remoteauth "github.com/OpenNSW/core/remote/auth"
	corestorage "github.com/OpenNSW/core/storage"
	"github.com/OpenNSW/core/storage/drivers"
)

const ownerToken = "proxy-service-token"

// newOwningService stands up the service that owns the files: core/storage's
// HTTPHandler over a local-disk backend, mounted at the default proxy paths.
// Its API routes accept only the bearer token (standing in for
// service-to-service auth); the presigned content routes must arrive without
// it.
func newOwningService(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authz := r.Header.Get("Authorization")
		if strings.HasSuffix(r.URL.Path, "/content") {
			if authz != "" {
				t.Errorf("%s %s: presigned URL fetched with API credentials", r.Method, r.URL.Path)
			}
			mux.ServeHTTP(w, r)
			return
		}
		if authz != "Bearer "+ownerToken {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"Unauthorized"}`))
			return
		}
		ctx := context.WithValue(r.Context(), coreauthn.AuthContextKey, &coreauthn.AuthContext{
			User: &coreauthn.UserContext{ID: "proxying-service"},
		})
		mux.ServeHTTP(w, r.WithContext(ctx))
	}))
	t.Cleanup(srv.Close)

	driver, err := drivers.NewLocalFSDriver(t.TempDir(), srv.URL, "owner-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("NewLocalFSDriver: %v", err)
	}
	// The owner applies the same upload policy this deployment does, so a
	// rejection it relays is a real one.
	h := corestorage.NewHTTPHandler(corestorage.NewService(driver,
		corestorage.WithAllowedUploadTypes(testUploadTypes...),
		corestorage.WithUploadTypeExtensions(testUploadTypeExtensions),
	))
	mux.HandleFunc(UploadRoute, h.Upload)
	mux.HandleFunc(InternalUploadRoute, h.Upload)
	mux.HandleFunc(DownloadRoute, h.Download)
	// ProxyService keeps Delete as part of Service, though this application
	// mounts no delete route.
	mux.HandleFunc("DELETE /api/v1/storage/{key}", h.Delete)
	corestorage.NewLocalContentHandler(driver).RegisterRoutes(mux)
	return srv
}

// newRegistry registers the owning service as "files-api".
func newRegistry(t *testing.T, url, token string) *remote.Manager {
	t.Helper()
	m := remote.NewManager()
	if err := m.SetServices(remote.ServiceConfig{
		ID: "files-api", URL: url,
		Auth: &remoteauth.Config{Type: remoteauth.TypeBearer, Bearer: &remoteauth.BearerConfig{Token: token}},
	}); err != nil {
		t.Fatalf("SetServices: %v", err)
	}
	return m
}

func defaultProxyConfig() ProxyConfig {
	return ProxyConfig{
		Service:            "files-api",
		UploadPath:         "/api/v1/storage",
		InternalUploadPath: "/api/v1/internal/storage",
		DownloadPath:       "/api/v1/storage/{key}",
		DeletePath:         "/api/v1/storage/{key}",
		AllowedUploadTypes: []string{"application/pdf"},
	}
}

func newProxyStack(t *testing.T, token string) *Stack {
	t.Helper()
	owner := newOwningService(t)
	stack, err := New(context.Background(), Config{
		Config: corestorage.Config{Type: TypeProxy},
		Proxy:  defaultProxyConfig(),
	}, newRegistry(t, owner.URL, token))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return stack
}

// TestProxy_RoundTrip uploads through the key the owning service allocates,
// downloads the file back, and deletes it.
func TestProxy_RoundTrip(t *testing.T) {
	stack := newProxyStack(t, ownerToken)
	svc := stack.Service
	ctx := context.Background()

	content := []byte("%PDF-1.4 proxy round trip")
	meta, err := svc.Upload(ctx, "passport.pdf", int64(len(content)), "application/pdf")
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if meta.Key == "" || !strings.HasSuffix(meta.Key, ".pdf") || meta.Name != "passport.pdf" {
		t.Fatalf("Upload metadata = %+v, want an owner-allocated .pdf key", meta)
	}

	// The client PUTs straight to the upload URL the owning service issued.
	req, _ := http.NewRequest(http.MethodPut, meta.UploadURL, bytes.NewReader(content))
	req.Header.Set("Content-Type", "application/pdf")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT upload URL: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT upload URL status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	body, contentType, err := svc.Download(ctx, meta.Key)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, _ := io.ReadAll(body)
	_ = body.Close()
	if !bytes.Equal(got, content) {
		t.Errorf("Download body = %q, want %q", got, content)
	}
	if contentType != "application/pdf" {
		t.Errorf("Download content type = %q, want application/pdf", contentType)
	}

	if err := svc.Delete(ctx, meta.Key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := svc.Download(ctx, meta.Key); err == nil {
		t.Fatal("Download after Delete: expected error, got nil")
	}
}

func TestProxyHandler_UploadAndDownload(t *testing.T) {
	stack := newProxyStack(t, ownerToken)

	rec := httptest.NewRecorder()
	stack.Handler.Upload(rec, httptest.NewRequest(http.MethodPost, "/api/v1/storage",
		strings.NewReader(`{"filename":"a.pdf","mime_type":"application/pdf","size":10}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("Upload status = %d, body %s", rec.Code, rec.Body.String())
	}
	var meta corestorage.FileMetadata
	if err := json.NewDecoder(rec.Body).Decode(&meta); err != nil || meta.Key == "" || meta.UploadURL == "" {
		t.Fatalf("Upload response = %+v (%v), want key and upload_url", meta, err)
	}

	// The owning service issues a download only for a file it holds.
	put, _ := http.NewRequest(http.MethodPut, meta.UploadURL, strings.NewReader("0123456789"))
	put.Header.Set("Content-Type", "application/pdf")
	resp, err := http.DefaultClient.Do(put)
	if err != nil {
		t.Fatalf("PUT upload URL: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT upload URL status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/storage/"+meta.Key, nil)
	req.SetPathValue("key", meta.Key)
	stack.Handler.Download(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Download status = %d, body %s", rec.Code, rec.Body.String())
	}
	var dl struct {
		DownloadURL string `json:"download_url"`
		ExpiresAt   int64  `json:"expires_at"`
		MimeType    string `json:"mime_type"`
		Disposition string `json:"disposition"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&dl); err != nil || dl.DownloadURL == "" || dl.ExpiresAt <= time.Now().Unix() {
		t.Fatalf("Download response = %+v (%v), want the owner's download_url and a future expires_at", dl, err)
	}
	// What the owning service says about the file is relayed as it said it.
	if dl.MimeType != "application/pdf" || dl.Disposition != "inline" {
		t.Errorf("Download response type, disposition = %q, %q; want application/pdf, inline", dl.MimeType, dl.Disposition)
	}
}

// An owning service that says nothing about how the URL serves the file leaves the
// field out of the response, which a client reads as a file to save.
func TestProxyHandler_DownloadOmitsADispositionTheOwnerDoesNotGive(t *testing.T) {
	owner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"download_url":"https://files.example/k","expires_at":4102444800}`))
	}))
	t.Cleanup(owner.Close)
	svc, err := NewProxyService(newRegistry(t, owner.URL, ownerToken), defaultProxyConfig())
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/storage/any-key", nil)
	req.SetPathValue("key", "any-key")
	NewProxyHandler(svc).Download(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Download status = %d, body %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["disposition"]; ok {
		t.Errorf("response %v carries a disposition the owner did not give", body)
	}
	if body["download_url"] != "https://files.example/k" {
		t.Errorf("download_url = %v", body["download_url"])
	}
}

// A request the owning service rejects is relayed with its status and message.
func TestProxyHandler_RelaysOwnerRejection(t *testing.T) {
	stack := newProxyStack(t, ownerToken)

	rec := httptest.NewRecorder()
	stack.Handler.Upload(rec, httptest.NewRequest(http.MethodPost, "/api/v1/storage",
		strings.NewReader(`{"filename":"a.exe","mime_type":"application/x-msdownload","size":10}`)))
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("Upload status = %d, want %d; body %s", rec.Code, http.StatusUnsupportedMediaType, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid or prohibited file type") {
		t.Errorf("Upload body = %s, want the owning service's message", rec.Body.String())
	}
}

// The owning service refusing this service's credentials is not the caller's
// fault: it is reported as a gateway failure, never relayed as a 401.
func TestProxyHandler_OwnerRefusingCredentialsIsBadGateway(t *testing.T) {
	stack := newProxyStack(t, "wrong-token")

	rec := httptest.NewRecorder()
	stack.Handler.Upload(rec, httptest.NewRequest(http.MethodPost, "/api/v1/storage",
		strings.NewReader(`{"filename":"a.pdf","mime_type":"application/pdf","size":10}`)))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("Upload status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
}

func TestProxyHandler_RejectsIncompleteUpload(t *testing.T) {
	h := NewProxyHandler(&ProxyService{})
	for _, body := range []string{`not json`, `{"mime_type":"application/pdf","size":1}`, `{"filename":"a.pdf","size":1}`, `{"filename":"a.pdf","mime_type":"application/pdf"}`} {
		rec := httptest.NewRecorder()
		h.Upload(rec, httptest.NewRequest(http.MethodPost, "/api/v1/storage", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("Upload(%s) status = %d, want %d", body, rec.Code, http.StatusBadRequest)
		}
	}
}

func TestNewProxyService_UnknownServiceFailsAtStartup(t *testing.T) {
	cfg := defaultProxyConfig()
	cfg.Service = "not-registered"
	if _, err := NewProxyService(newRegistry(t, "https://files.example.com", ownerToken), cfg); err == nil {
		t.Fatal("NewProxyService: expected error for a service missing from the registry")
	}
}

func TestNew_BackendModeKeepsCoreStorage(t *testing.T) {
	stack, err := New(context.Background(), localConfig(t, "http://localhost:8080"), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := stack.Service.(*corestorage.Service); !ok {
		t.Errorf("Service = %T, want *corestorage.Service", stack.Service)
	}
	if stack.LocalContent == nil {
		t.Error("LocalContent = nil, want the local content handlers for a local backend")
	}
}

func TestNew_ProxyModeHasNoLocalContent(t *testing.T) {
	stack := newProxyStack(t, ownerToken)
	if _, ok := stack.Service.(*ProxyService); !ok {
		t.Errorf("Service = %T, want *ProxyService", stack.Service)
	}
	if stack.LocalContent != nil {
		t.Error("LocalContent != nil, want no local content routes in proxy mode")
	}
}

func TestProxyConfigValidate(t *testing.T) {
	cases := map[string]func(*ProxyConfig){
		"missing service":              func(c *ProxyConfig) { c.Service = "" },
		"missing upload path":          func(c *ProxyConfig) { c.UploadPath = "" },
		"missing internal upload path": func(c *ProxyConfig) { c.InternalUploadPath = "" },
		"missing upload types":         func(c *ProxyConfig) { c.AllowedUploadTypes = nil },
		"download path without key":    func(c *ProxyConfig) { c.DownloadPath = "/api/v1/storage" },
		"delete path without key":      func(c *ProxyConfig) { c.DeletePath = "/api/v1/storage" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := defaultProxyConfig()
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Error("Validate: expected error")
			}
		})
	}
	if err := defaultProxyConfig().Validate(); err != nil {
		t.Errorf("Validate(defaults) error = %v", err)
	}
}

func TestKeyPath_KeyAnywhereInPath(t *testing.T) {
	if got := keyPath("/api/v1/{key}/content", "abc.pdf"); got != "/api/v1/abc.pdf/content" {
		t.Errorf("keyPath = %q, want /api/v1/abc.pdf/content", got)
	}
}

// TestProxy_Save stores content under a key the owning service allocates and
// reads it back.
func TestProxy_Save(t *testing.T) {
	svc := newProxyStack(t, ownerToken).Service
	ctx := context.Background()

	content := []byte("%PDF-1.4 generated on the server")
	meta, err := svc.Save(ctx, "permit.pdf", "application/pdf", bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !strings.HasSuffix(meta.Key, ".pdf") || meta.Size != int64(len(content)) || meta.UploadURL != "" {
		t.Fatalf("Save metadata = %+v, want a .pdf key, the content's size and no upload URL", meta)
	}

	body, mime, err := svc.Download(ctx, meta.Key)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer func() { _ = body.Close() }()
	got, _ := io.ReadAll(body)
	if !bytes.Equal(got, content) || mime != "application/pdf" {
		t.Errorf("Download = (%q, %q), want (%q, application/pdf)", got, mime, content)
	}
}

// TestNew_BackendSave stores content through a local backend's Service, which
// is core/storage's own, and reads it back.
func TestNew_BackendSave(t *testing.T) {
	stack, err := New(context.Background(), localConfig(t, "http://localhost:8080"), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	content := []byte("<!DOCTYPE html><p>Permit</p>")
	meta, err := stack.Service.Save(ctx, "permit.html", "text/html; charset=utf-8", bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !strings.HasSuffix(meta.Key, ".html") || meta.Name != "permit.html" || meta.Size != int64(len(content)) {
		t.Fatalf("Save metadata = %+v, want a .html key named permit.html with the content's size", meta)
	}

	body, mime, err := stack.Service.Download(ctx, meta.Key)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer func() { _ = body.Close() }()
	got, _ := io.ReadAll(body)
	if !bytes.Equal(got, content) || mime != "text/html; charset=utf-8" {
		t.Errorf("Download = (%q, %q), want the saved content and type", got, mime)
	}
}

// TestProxy_SaveFallsBackToRequestedType sends the content as the type it
// asked for when the owning service's reply leaves the type out; sending no
// type would not match a URL signed over it. The content goes with the length
// it was given, not chunked, even from a reader that can't report its own.
func TestProxy_SaveFallsBackToRequestedType(t *testing.T) {
	var putType string
	var putLength int64
	var putChunked bool
	var putBody []byte
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"key":"0f8e7c1a-0000-4000-8000-000000000001.html","upload_url":%q}`, srv.URL+"/content")
		case http.MethodPut:
			putType = r.Header.Get("Content-Type")
			putLength = r.ContentLength
			putChunked = len(r.TransferEncoding) > 0
			putBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	svc, err := NewProxyService(newRegistry(t, srv.URL, ownerToken), defaultProxyConfig())
	if err != nil {
		t.Fatalf("NewProxyService: %v", err)
	}
	content := "<p>x</p>"
	// MultiReader hides the length that net/http would read off a
	// *strings.Reader or *bytes.Reader by itself.
	body := io.MultiReader(strings.NewReader(content))
	meta, err := svc.Save(context.Background(), "doc.html", "text/html; charset=utf-8", body, int64(len(content)))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if putType != "text/html; charset=utf-8" || meta.MimeType != "text/html; charset=utf-8" {
		t.Errorf("PUT Content-Type = %q, metadata type = %q; want the requested type for both", putType, meta.MimeType)
	}
	if putChunked || putLength != int64(len(content)) || string(putBody) != content {
		t.Errorf("PUT chunked = %v, Content-Length = %d, body = %q; want %d bytes of %q, not chunked", putChunked, putLength, putBody, len(content), content)
	}
}

// Fetching a file's content fails the same way as the rest of the proxy: as
// an upstream failure, whether the owning service's download URL answers an
// error or can't be reached.
func TestProxyService_DownloadFailuresAreUpstream(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	for name, contentURL := range map[string]func(owner string) string{
		"content answers 500": func(owner string) string { return owner + "/content" },
		"content unreachable": func(string) string { return closed.URL + "/content" },
	} {
		t.Run(name, func(t *testing.T) {
			var owner *httptest.Server
			owner = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/content") {
					http.Error(w, "storage backend down", http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"download_url": contentURL(owner.URL), "expires_at": 1})
			}))
			t.Cleanup(owner.Close)
			svc, err := NewProxyService(newRegistry(t, owner.URL, ownerToken), defaultProxyConfig())
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = svc.Download(context.Background(), "k.pdf")
			if !isUpstreamFailure(err) {
				t.Fatalf("Download: err = %v, want an upstream failure", err)
			}
		})
	}
}

// A user's file is reserved on the owning service's upload endpoint, and a file
// this deployment generates on its internal one.
func TestProxy_UploadAndSaveUseTheirOwnEndpoints(t *testing.T) {
	var hits []string
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	reserve := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			hits = append(hits, name)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"key":"k.pdf","name":"f","upload_url":"` + srv.URL + `/put","mime_type":"application/pdf"}`))
		}
	}
	mux.HandleFunc(UploadRoute, reserve("upload"))
	mux.HandleFunc(InternalUploadRoute, reserve("internal"))
	mux.HandleFunc("PUT /put", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	svc, err := NewProxyService(newRegistry(t, srv.URL, ownerToken), defaultProxyConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Upload(context.Background(), "f.pdf", 3, "application/pdf"); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if _, err := svc.Save(context.Background(), "f.pdf", "application/pdf", strings.NewReader("abc"), 3); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if want := []string{"upload", "internal"}; !slices.Equal(hits, want) {
		t.Errorf("endpoints hit = %v, want %v", hits, want)
	}
}
