package authn

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewManager_AllowsNilUserProfileService(t *testing.T) {
	cfg := Config{
		JWKSURL:  "https://localhost/jwks",
		Issuer:   "https://localhost/token",
		Audience: "TRADER_PORTAL_APP",
		ClientIDs: []string{
			"TRADER_PORTAL_APP",
		},
	}

	manager, err := NewManager(nil, cfg)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if manager == nil {
		t.Fatalf("expected manager, got nil")
	}
	if manager.userProfileService != nil {
		t.Fatalf("expected nil userProfileService, got %T", manager.userProfileService)
	}
	if manager.tokenExtractor == nil {
		t.Fatal("expected tokenExtractor to be initialized")
	}
	if manager.tokenExtractor.httpClient.Transport != nil {
		t.Fatalf("expected default transport to be nil, got %T", manager.tokenExtractor.httpClient.Transport)
	}
}

func TestNewManager_InvalidConfig(t *testing.T) {
	cfg := Config{
		Issuer:   "https://localhost/token",
		Audience: "TRADER_PORTAL_APP",
		ClientIDs: []string{
			"TRADER_PORTAL_APP",
		},
	}

	if _, err := NewManager(nil, cfg); err == nil {
		t.Fatalf("expected error for invalid config")
	}
}

func TestManager_Health_NoTokenExtractor(t *testing.T) {
	manager := &Manager{}
	if err := manager.Health(); err == nil {
		t.Fatalf("expected error when token extractor is nil")
	}
}

func TestNewManager_InsecureSkipTLSVerify(t *testing.T) {
	cfg := Config{
		JWKSURL:               "https://localhost/jwks",
		Issuer:                "https://localhost/token",
		Audience:              "TRADER_PORTAL_APP",
		ClientIDs:             []string{"TRADER_PORTAL_APP"},
		InsecureSkipTLSVerify: true,
	}

	manager, err := NewManager(nil, cfg)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if manager.tokenExtractor == nil || manager.tokenExtractor.httpClient == nil {
		t.Fatalf("expected tokenExtractor with http client")
	}
	transport, ok := manager.tokenExtractor.httpClient.Transport.(*http.Transport)
	if !ok || transport == nil {
		t.Fatalf("expected *http.Transport, got %T", manager.tokenExtractor.httpClient.Transport)
	}
	if transport.TLSClientConfig == nil || !transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatalf("expected InsecureSkipVerify to be true")
	}
}

func TestManager_Health_Success(t *testing.T) {
	cfg := Config{
		JWKSURL:  "https://localhost/jwks",
		Issuer:   "https://localhost/token",
		Audience: "TRADER_PORTAL_APP",
		ClientIDs: []string{
			"TRADER_PORTAL_APP",
		},
	}

	manager, err := NewManager(nil, cfg)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if err := manager.Health(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestManager_MiddlewareFunctions(t *testing.T) {
	cfg := Config{
		JWKSURL:  "https://localhost/jwks",
		Issuer:   "https://localhost/token",
		Audience: "TRADER_PORTAL_APP",
		ClientIDs: []string{
			"TRADER_PORTAL_APP",
		},
	}

	manager, err := NewManager(nil, cfg)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	baseHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for _, middleware := range []func(http.Handler) http.Handler{
		manager.Middleware(),
		manager.OptionalAuthMiddleware(),
	} {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://example.com/test", nil)
		middleware(baseHandler).ServeHTTP(recorder, req)
		if recorder.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", recorder.Code)
		}
	}
}

func TestManager_RequireAuthMiddleware(t *testing.T) {
	cfg := Config{
		JWKSURL:  "https://localhost/jwks",
		Issuer:   "https://localhost/token",
		Audience: "TRADER_PORTAL_APP",
		ClientIDs: []string{
			"TRADER_PORTAL_APP",
		},
	}

	manager, err := NewManager(nil, cfg)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	handlerCalled := false
	protected := manager.RequireAuthMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example.com/protected", nil)
	protected.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", recorder.Code)
	}
	if handlerCalled {
		t.Fatalf("expected handler not to be called")
	}
}

func TestManager_Close(t *testing.T) {
	manager := &Manager{}
	if err := manager.Close(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}
