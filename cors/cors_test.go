// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package cors

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// okHandler is a trivial next handler that writes 200.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// defaultConfig returns a valid Config suitable for most tests.
func defaultConfig() *Config {
	return &Config{
		AllowedOrigins:   []string{"http://localhost:3000"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "Authorization"},
		AllowCredentials: true,
		MaxAge:           3600,
	}
}

// ---- Config.Validate() ----

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name:    "valid config with specific origin",
			cfg:     Config{AllowedOrigins: []string{"http://localhost:3000"}},
			wantErr: false,
		},
		{
			name:    "valid config with wildcard",
			cfg:     Config{AllowedOrigins: []string{"*"}},
			wantErr: false,
		},
		{
			name:    "valid config with multiple origins",
			cfg:     Config{AllowedOrigins: []string{"http://localhost:3000", "https://example.com"}},
			wantErr: false,
		},
		{
			name:    "empty origins",
			cfg:     Config{AllowedOrigins: []string{}},
			wantErr: true,
		},
		{
			name:    "nil origins",
			cfg:     Config{},
			wantErr: true,
		},
		{
			name:    "invalid origin URL",
			cfg:     Config{AllowedOrigins: []string{"not-a-url"}},
			wantErr: true,
		},
		{
			name:    "ftp scheme rejected",
			cfg:     Config{AllowedOrigins: []string{"ftp://example.com"}},
			wantErr: true,
		},
		{
			name:    "wildcard mixed with valid origin",
			cfg:     Config{AllowedOrigins: []string{"*", "https://example.com"}},
			wantErr: false,
		},
		{
			name:    "wildcard origin with AllowCredentials=true rejected",
			cfg:     Config{AllowedOrigins: []string{"*"}, AllowCredentials: true},
			wantErr: true,
		},
		{
			name:    "wildcard origin with AllowCredentials=false accepted",
			cfg:     Config{AllowedOrigins: []string{"*"}, AllowCredentials: false},
			wantErr: false,
		},
		{
			name:    "wildcard mixed with valid origin and AllowCredentials=true rejected",
			cfg:     Config{AllowedOrigins: []string{"https://example.com", "*"}, AllowCredentials: true},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// ---- CORS() middleware ----

func TestCORSNoOriginHeader(t *testing.T) {
	handler := CORS(defaultConfig())(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("expected no CORS headers on non-CORS request")
	}
}

func TestCORSAllowedOrigin(t *testing.T) {
	handler := CORS(defaultConfig())(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "http://localhost:3000")
	}
	if rr.Header().Get("Vary") != "Origin" {
		t.Error("expected Vary: Origin header")
	}
	if rr.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Error("expected Access-Control-Allow-Credentials: true")
	}
}

func TestCORSDisallowedOriginActualRequest(t *testing.T) {
	handler := CORS(defaultConfig())(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "http://evil.com")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Passes through to the next handler — browser enforces the block.
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("expected no Access-Control-Allow-Origin for disallowed origin")
	}
}

func TestCORSDisallowedOriginPreflight(t *testing.T) {
	handler := CORS(defaultConfig())(okHandler)
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "http://evil.com")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rr.Code)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("expected no Access-Control-Allow-Origin for disallowed preflight")
	}
}

func TestCORSPreflightAllowedOrigin(t *testing.T) {
	handler := CORS(defaultConfig())(okHandler)
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Errorf("Access-Control-Allow-Origin = %q", got)
	}
	if rr.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("expected Access-Control-Allow-Methods on preflight")
	}
	if rr.Header().Get("Access-Control-Allow-Headers") == "" {
		t.Error("expected Access-Control-Allow-Headers on preflight")
	}
	if got := rr.Header().Get("Access-Control-Max-Age"); got != "3600" {
		t.Errorf("Access-Control-Max-Age = %q, want %q", got, "3600")
	}
}

func TestCORSPreflightNoMaxAge(t *testing.T) {
	cfg := defaultConfig()
	cfg.MaxAge = 0
	handler := CORS(cfg)(okHandler)
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Header().Get("Access-Control-Max-Age") != "" {
		t.Error("expected no Access-Control-Max-Age when MaxAge is 0")
	}
}

func TestCORSWildcardOrigin(t *testing.T) {
	cfg := &Config{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET"},
		AllowedHeaders: []string{"Content-Type"},
	}
	handler := CORS(cfg)(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "http://any-origin.example.com")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want '*'", got)
	}
}

func TestCORSEmptyAllowedOrigins(t *testing.T) {
	cfg := &Config{
		AllowedOrigins: []string{},
	}
	handler := CORS(cfg)(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected empty Access-Control-Allow-Origin when AllowedOrigins is empty, got %q", got)
	}
}

func TestCORSExplicitMatchWithWildcard(t *testing.T) {
	cfg := &Config{
		AllowedOrigins: []string{"http://explicit.example.com", "*"},
	}
	handler := CORS(cfg)(okHandler)

	// Explicit match should reflect the matching origin
	t.Run("explicit match", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "http://explicit.example.com")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "http://explicit.example.com" {
			t.Errorf("Access-Control-Allow-Origin = %q, want 'http://explicit.example.com'", got)
		}
	})

	// Non-explicit match should receive literal '*'
	t.Run("wildcard fallback match", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "http://other.example.com")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("Access-Control-Allow-Origin = %q, want '*'", got)
		}
	})
}

func TestCORSNoCredentials(t *testing.T) {
	cfg := defaultConfig()
	cfg.AllowCredentials = false
	handler := CORS(cfg)(okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Error("expected no Access-Control-Allow-Credentials when AllowCredentials is false")
	}
}

// ---- matchOrigin() ----

func TestMatchOrigin(t *testing.T) {
	tests := []struct {
		origin  string
		allowed []string
		want    originMatch
	}{
		{"http://localhost:3000", []string{"http://localhost:3000"}, matchExplicit},
		{"http://localhost:3000", []string{"http://other.com"}, matchNone},
		{"http://anything.com", []string{"*"}, matchWildcard},
		{"http://localhost:3000", []string{"http://other.com", "http://localhost:3000"}, matchExplicit},
		{"http://localhost:3000", []string{"*", "http://localhost:3000"}, matchExplicit},
		{"http://localhost:3000", []string{}, matchNone},
	}

	for _, tt := range tests {
		if got := matchOrigin(tt.origin, tt.allowed); got != tt.want {
			t.Errorf("matchOrigin(%q, %v) = %v, want %v", tt.origin, tt.allowed, got, tt.want)
		}
	}
}
