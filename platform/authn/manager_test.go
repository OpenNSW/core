package authn

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	coreauthn "github.com/OpenNSW/core/authn"
	"github.com/golang-jwt/jwt/v5"
)

func TestNewManager_InvalidConfig(t *testing.T) {
	if _, err := NewManager(nil, Config{}); err == nil {
		t.Fatal("expected an error for an empty config")
	}
}

func TestNewManager_AllowsNilUserProfileService(t *testing.T) {
	m, err := NewManager(nil, validConfig())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := m.Health(); err != nil {
		t.Fatalf("Health: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// attachPrincipal is the second half of RequireAuthMiddleware: it converts
// core/authn's request context into a Principal under this package's key, so
// handlers read identity through FromContext and never touch a core type.
func TestAttachPrincipal(t *testing.T) {
	t.Run("converts core auth context", func(t *testing.T) {
		var got *Principal
		var ok bool
		next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got, ok = FromContext(r.Context())
		})

		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r = r.WithContext(context.WithValue(r.Context(), coreauthn.AuthContextKey, &coreauthn.AuthContext{
			User: &coreauthn.UserContext{
				ID:          "u1",
				Roles:       []string{"Trader"},
				ExtraClaims: coreauthn.ExtraClaims{"ouHandle": "ou-001"},
			},
		}))

		attachPrincipal(next).ServeHTTP(httptest.NewRecorder(), r)

		if !ok {
			t.Fatal("expected a principal to be attached")
		}
		if got.UserID != "u1" || got.OUHandle != "ou-001" || got.Kind != KindUser {
			t.Fatalf("unexpected principal: %#v", got)
		}
	})

	// The security log sits outside the mux, and the principal is attached on a
	// child request. The slot is how that outer log still learns who called.
	t.Run("records the caller for an outer observer", func(t *testing.T) {
		slot := &RequestCaller{}
		next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r = r.WithContext(WithRequestCaller(r.Context(), slot))
		r = r.WithContext(context.WithValue(r.Context(), coreauthn.AuthContextKey, &coreauthn.AuthContext{
			Client: &coreauthn.ClientContext{ClientID: "FCAU_TO_NSW"},
		}))

		attachPrincipal(next).ServeHTTP(httptest.NewRecorder(), r)

		if slot.Principal == nil || slot.Principal.Kind != KindClient || slot.Principal.ClientID != "FCAU_TO_NSW" {
			t.Fatalf("slot principal = %#v", slot.Principal)
		}
	})

	// Without a core auth context there is nothing to convert; the request must
	// proceed unattached so downstream checks deny it, not panic.
	t.Run("passes through an unauthenticated request", func(t *testing.T) {
		called := false
		attached := true
		next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			called = true
			_, attached = FromContext(r.Context())
		})

		attachPrincipal(next).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

		if !called {
			t.Fatal("expected the next handler to run")
		}
		if attached {
			t.Fatal("expected no principal to be attached")
		}
	})
}

// RequireAuthMiddleware composes core's enforcement with attachPrincipal, so an
// unauthenticated request is rejected before reaching the handler.
func TestManager_RequireAuthMiddleware_RejectsUnauthenticated(t *testing.T) {
	m, err := NewManager(nil, validConfig())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	called := false
	protected := m.RequireAuthMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if called {
		t.Fatal("expected the protected handler not to run")
	}
}

// tokenFixture serves a JWKS for a throwaway key and mints tokens signed with
// it, so a test can drive a Manager the way a real request does.
type tokenFixture struct {
	key    *rsa.PrivateKey
	jwks   string
	issuer string
	aud    string
}

const fixtureKID = "test-kid"

func newTokenFixture(t *testing.T) tokenFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
			"kid": fixtureKID, "kty": "RSA", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	t.Cleanup(srv.Close)
	return tokenFixture{key: key, jwks: srv.URL, issuer: "https://idp.example.com", aud: "https://api.example.com"}
}

func (f tokenFixture) config() Config {
	return Config{JWKSURL: f.jwks, Issuer: f.issuer, Audience: f.aud}
}

// token mints a signed token carrying the standard claims plus extra.
func (f tokenFixture) token(t *testing.T, grant string, extra map[string]any) string {
	t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{
		"iss": f.issuer, "aud": f.aud, "sub": "idp-user-1", "client_id": "PORTAL", "grant_type": grant,
		"iat": now.Add(-time.Minute).Unix(), "nbf": now.Add(-time.Minute).Unix(), "exp": now.Add(10 * time.Minute).Unix(),
	}
	for k, v := range extra {
		claims[k] = v
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = fixtureKID
	signed, err := tok.SignedString(f.key)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

// userToken carries the claims every user token must have, plus extra.
func (f tokenFixture) userToken(t *testing.T, extra map[string]any) string {
	t.Helper()
	claims := map[string]any{"email": "officer@example.com", "ouId": "OU-1", "ouHandle": "ou-1"}
	for k, v := range extra {
		claims[k] = v
	}
	return f.token(t, "authorization_code", claims)
}

// call sends token through the manager's real middleware and returns the status
// and the Principal the handler saw (nil if it never ran).
func call(t *testing.T, m *Manager, token string) (int, *Principal) {
	t.Helper()
	var got *Principal
	protected := m.RequireAuthMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)
	return rec.Code, got
}

// extraClaimsManager builds a manager declaring user claims department and
// grades and client claim agency, and the fixture that mints its tokens.
func extraClaimsManager(t *testing.T) (tokenFixture, *Manager) {
	t.Helper()
	f := newTokenFixture(t)
	cfg := f.config()
	cfg.ExtraUserClaims = ClaimSpec{Optional: []string{"department", "grades"}}
	cfg.ExtraClientClaims = ClaimSpec{Optional: []string{"agency"}}
	m, err := NewManager(nil, cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return f, m
}

// The whole chain, with a real signed token: claims a deployment lists in
// config reach the Principal; claims it didn't list, and claims listed for the
// other kind of token, do not.
func TestManager_UserExtraClaimsFromToken(t *testing.T) {
	f, m := extraClaimsManager(t)

	code, p := call(t, m, f.userToken(t, map[string]any{
		"department": "Customs",
		"grades":     []string{"A", "B"},
		"agency":     "npqs",  // declared for clients only
		"undeclared": "value", // declared for nobody
	}))
	if code != http.StatusOK || p == nil {
		t.Fatalf("status %d, principal %#v", code, p)
	}
	if p.Kind != KindUser || p.Email != "officer@example.com" || p.OUHandle != "ou-1" {
		t.Fatalf("built-in claims lost: %#v", p)
	}
	if got := p.ExtraClaims.String("department"); got != "Customs" {
		t.Errorf("department = %q, want Customs", got)
	}
	if got := p.ExtraClaims.Strings("grades"); !slices.Equal(got, []string{"A", "B"}) {
		t.Errorf("grades = %v, want [A B]", got)
	}
	if len(p.ExtraClaims) != 2 {
		t.Errorf("expected exactly the two declared claims, got %v", p.ExtraClaims)
	}
}

// An absent optional claim is not an error, and is simply not there.
func TestManager_UserWithoutOptionalExtraClaim(t *testing.T) {
	f, m := extraClaimsManager(t)

	code, p := call(t, m, f.userToken(t, map[string]any{"department": "Customs"}))
	if code != http.StatusOK || p == nil {
		t.Fatalf("status %d, principal %#v", code, p)
	}
	if _, ok := p.ExtraClaims["grades"]; ok {
		t.Errorf("grades should be absent, got %v", p.ExtraClaims)
	}
}

func TestManager_ClientExtraClaimsFromToken(t *testing.T) {
	f, m := extraClaimsManager(t)

	code, p := call(t, m, f.token(t, "client_credentials", map[string]any{
		"agency":     "npqs",
		"department": "Customs", // declared for users only
	}))
	if code != http.StatusOK || p == nil {
		t.Fatalf("status %d, principal %#v", code, p)
	}
	if p.Kind != KindClient || p.ClientID != "PORTAL" {
		t.Fatalf("unexpected principal: %#v", p)
	}
	if len(p.ExtraClaims) != 1 || p.ExtraClaims.String("agency") != "npqs" {
		t.Errorf("expected only agency, got %v", p.ExtraClaims)
	}
}

// A required extra claim turns its absence into a 401, so a deployment that
// lists one makes it a condition of every token it accepts.
func TestManager_RequiredExtraClaimRejectsTokenWithout(t *testing.T) {
	f := newTokenFixture(t)
	cfg := f.config()
	cfg.ExtraUserClaims = ClaimSpec{Required: []string{"tenant"}}
	m, err := NewManager(nil, cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	if code, _ := call(t, m, f.userToken(t, nil)); code != http.StatusUnauthorized {
		t.Errorf("token without the required claim: status %d, want 401", code)
	}
	code, p := call(t, m, f.userToken(t, map[string]any{"tenant": "acme"}))
	if code != http.StatusOK || p == nil || p.ExtraClaims.String("tenant") != "acme" {
		t.Errorf("token with the required claim: status %d, principal %#v", code, p)
	}
}
