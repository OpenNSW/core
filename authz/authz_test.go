package authz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakePrincipal is a plain struct (no authn dependency) used to prove the package
// authorizes purely against the Principal contract.
type fakePrincipal struct {
	subject string
	roles   []string
	scopes  []string
}

func (f *fakePrincipal) Subject() string  { return f.subject }
func (f *fakePrincipal) Roles() []string  { return f.roles }
func (f *fakePrincipal) Scopes() []string { return f.scopes }

// Compile-time proof that a plain struct satisfies Principal (no authn needed).
var _ Principal = (*fakePrincipal)(nil)

// staticExtractor returns p for every context; a nil p models an unauthenticated request.
func staticExtractor(p Principal) Extractor {
	return func(context.Context) (Principal, bool) {
		if p == nil {
			return nil, false
		}
		return p, true
	}
}

func mustNew(t *testing.T, extract Extractor) *Authorizer {
	t.Helper()
	a, err := New(extract)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

func TestScopeHelpers(t *testing.T) {
	p := &fakePrincipal{scopes: []string{"nsw:task:read", "nsw:consignment:read"}}
	cases := []struct {
		name string
		got  bool
		want bool
	}{
		{"has exact", HasScope(p, "nsw:task:read"), true},
		{"missing", HasScope(p, "nsw:task:write"), false},
		{"empty scope", HasScope(p, ""), false},
		{"nil principal", HasScope(nil, "x"), false},
		{"any present", HasAnyScope(p, "x", "nsw:consignment:read"), true},
		{"any absent", HasAnyScope(p, "x", "y"), false},
		{"any none given", HasAnyScope(p), false},
		{"all present", HasAllScopes(p, "nsw:task:read", "nsw:consignment:read"), true},
		{"all partial", HasAllScopes(p, "nsw:task:read", "x"), false},
		{"all none given", HasAllScopes(p), true},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestNew_NilExtractorReturnsError(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("expected error for nil extractor")
	}
}

func TestRequireScope_EmptyScopePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for empty scope")
		}
	}()
	mustNew(t, staticExtractor(&fakePrincipal{})).RequireScope("")
}

func TestAuthorizer_Principal(t *testing.T) {
	want := &fakePrincipal{subject: "u1"}
	if got, ok := mustNew(t, staticExtractor(want)).Principal(context.Background()); !ok || got != want {
		t.Fatalf("Principal() = %v, %v; want %v, true", got, ok, want)
	}
	if _, ok := mustNew(t, staticExtractor(nil)).Principal(context.Background()); ok {
		t.Fatal("expected no principal for unauthenticated request")
	}
	var nilA *Authorizer
	if _, ok := nilA.Principal(context.Background()); ok {
		t.Fatal("nil Authorizer should return (nil, false)")
	}
}

func serve(t *testing.T, mw func(http.Handler) http.Handler) (status int, nextCalled bool) {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	return rec.Code, nextCalled
}

func TestRequireScope_Enforcement(t *testing.T) {
	user := &fakePrincipal{subject: "u1", roles: []string{"Trader"}, scopes: []string{"nsw:consignment:read"}}
	client := &fakePrincipal{subject: "NPQS_TO_NSW", scopes: []string{"nsw:task:write"}} // scopes only, no roles

	cases := []struct {
		name       string
		principal  Principal // nil => unauthenticated
		scope      string
		wantStatus int
		wantNext   bool
	}{
		{"unauthenticated -> 401", nil, "nsw:consignment:read", http.StatusUnauthorized, false},
		{"user missing scope -> 403", user, "nsw:consignment:write", http.StatusForbidden, false},
		{"user has scope -> 200", user, "nsw:consignment:read", http.StatusOK, true},
		{"client has scope -> 200", client, "nsw:task:write", http.StatusOK, true},
		{"client missing scope -> 403", client, "nsw:task:read", http.StatusForbidden, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, nextCalled := serve(t, mustNew(t, staticExtractor(c.principal)).RequireScope(c.scope))
			if status != c.wantStatus {
				t.Errorf("status = %d, want %d", status, c.wantStatus)
			}
			if nextCalled != c.wantNext {
				t.Errorf("next called = %v, want %v", nextCalled, c.wantNext)
			}
		})
	}
}

func TestRequireAnyAndAllScopes(t *testing.T) {
	a := mustNew(t, staticExtractor(&fakePrincipal{scopes: []string{"a", "b"}}))

	checks := []struct {
		name       string
		mw         func(http.Handler) http.Handler
		wantStatus int
	}{
		{"any present", a.RequireAnyScope("x", "b"), http.StatusOK},
		{"any absent", a.RequireAnyScope("x", "y"), http.StatusForbidden},
		{"all present", a.RequireAllScopes("a", "b"), http.StatusOK},
		{"all partial", a.RequireAllScopes("a", "x"), http.StatusForbidden},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if status, _ := serve(t, c.mw); status != c.wantStatus {
				t.Errorf("status = %d, want %d", status, c.wantStatus)
			}
		})
	}

	for _, name := range []string{"any", "all"} {
		t.Run("no scopes panics ("+name+")", func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic when no scopes provided")
				}
			}()
			if name == "any" {
				a.RequireAnyScope()
			} else {
				a.RequireAllScopes()
			}
		})

		t.Run("empty scope string panics ("+name+")", func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic when empty scope string provided")
				}
			}()
			if name == "any" {
				a.RequireAnyScope("nsw:task:read", "")
			} else {
				a.RequireAllScopes("nsw:task:read", "")
			}
		})
	}
}
