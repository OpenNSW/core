package authn

import (
	"context"
	"slices"
	"testing"

	coreauthn "github.com/OpenNSW/core/authn"
	"github.com/OpenNSW/core/authz"
)

// ScopePrincipal must satisfy core/authz's Principal interface structurally.
// This assertion is why internal/authn can own the identity type without
// importing core/authz in non-test code, and why the composition root can keep
// building the Authorizer. Breaking it breaks every scope-gated route.
var _ authz.Principal = ScopePrincipal{}

func TestFromContext_Unauthenticated(t *testing.T) {
	if _, ok := FromContext(context.Background()); ok {
		t.Fatal("expected no principal on a bare context")
	}
	// A nil principal stored explicitly must still read as unauthenticated,
	// rather than handing callers a nil pointer to dereference.
	if _, ok := FromContext(ContextWithPrincipal(context.Background(), nil)); ok {
		t.Fatal("expected a nil principal to read as unauthenticated")
	}
}

func TestContextWithPrincipal_DoesNotReplaceRecordedCaller(t *testing.T) {
	slot := &RequestCaller{Principal: &Principal{Kind: KindUser, UserID: "caller"}}
	ctx := WithRequestCaller(context.Background(), slot)
	_ = ContextWithPrincipal(ctx, &Principal{Kind: KindClient, ClientID: "system"})
	if slot.Principal == nil || slot.Principal.UserID != "caller" {
		t.Fatalf("recorded caller = %#v", slot.Principal)
	}
}

func TestContextWithPrincipal_RoundTrip(t *testing.T) {
	want := &Principal{Kind: KindUser, UserID: "u1", OUHandle: "ou-1"}
	got, ok := FromContext(ContextWithPrincipal(context.Background(), want))
	if !ok {
		t.Fatal("expected a principal")
	}
	if got != want {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestPrincipal_Subject(t *testing.T) {
	tests := []struct {
		name string
		p    *Principal
		want string
	}{
		{"nil principal", nil, ""},
		{"user with resolved id", &Principal{Kind: KindUser, UserID: "u1", IDPUserID: "idp1"}, "u1"},
		{"user falls back to idp id", &Principal{Kind: KindUser, IDPUserID: "idp1"}, "idp1"},
		{"client", &Principal{Kind: KindClient, ClientID: "SVC"}, "SVC"},
		{"unknown kind", &Principal{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.Subject(); got != tt.want {
				t.Fatalf("Subject() = %q, want %q", got, tt.want)
			}
		})
	}
}

// principalFromAuthContext is the conversion the middleware runs once per
// request, and the only place the IdP's claim names are read.
func TestPrincipalFromAuthContext_UserFlattensDeclaredClaims(t *testing.T) {
	p := principalFromAuthContext(&coreauthn.AuthContext{
		User: &coreauthn.UserContext{
			ID:        "u1",
			IDPUserID: "idp1",
			Roles:     []string{"Trader"},
			Scopes:    []string{"task:read"},
			ExtraClaims: coreauthn.ExtraClaims{
				"email":        "trader@example.com",
				"phone_number": "+94771234567",
				"ouId":         "OU-001",
				"ouHandle":     "ou-001",
			},
		},
	})
	if p == nil {
		t.Fatal("expected a principal")
	}
	if p.Kind != KindUser || p.UserID != "u1" || p.IDPUserID != "idp1" {
		t.Fatalf("unexpected identity: %#v", p)
	}
	if p.Email != "trader@example.com" || p.PhoneNumber != "+94771234567" ||
		p.OUID != "OU-001" || p.OUHandle != "ou-001" {
		t.Fatalf("unexpected claims: %#v", p)
	}
	if len(p.Roles) != 1 || p.Roles[0] != "Trader" {
		t.Fatalf("unexpected roles: %#v", p.Roles)
	}
	if len(p.Scopes) != 1 || p.Scopes[0] != "task:read" {
		t.Fatalf("unexpected scopes: %#v", p.Scopes)
	}
}

func TestPrincipalFromAuthContext_UserWithoutExtraClaims(t *testing.T) {
	p := principalFromAuthContext(&coreauthn.AuthContext{
		User: &coreauthn.UserContext{ID: "u1"},
	})
	if p == nil {
		t.Fatal("expected a principal")
	}
	if p.Email != "" || p.PhoneNumber != "" || p.OUID != "" || p.OUHandle != "" {
		t.Fatalf("expected empty claim fields, got %#v", p)
	}
}

func TestPrincipalFromAuthContext_ClientCarriesNoIdentityClaims(t *testing.T) {
	p := principalFromAuthContext(&coreauthn.AuthContext{
		Client: &coreauthn.ClientContext{
			ClientID: "GOVPAY_TO_NSW",
			Roles:    []string{"PaymentWebhookM2M"},
			Scopes:   []string{"payment-webhooks:process"},
		},
	})
	if p == nil {
		t.Fatal("expected a principal")
	}
	if p.Kind != KindClient || p.ClientID != "GOVPAY_TO_NSW" {
		t.Fatalf("unexpected client principal: %#v", p)
	}
	if p.OUHandle != "" || p.Email != "" {
		t.Fatalf("client principal should carry no user claims: %#v", p)
	}
}

// Fail closed: a context carrying neither principal must not produce a
// principal, so downstream scope and ownership checks deny the request.
func TestPrincipalFromAuthContext_EmptyYieldsNoPrincipal(t *testing.T) {
	if p := principalFromAuthContext(nil); p != nil {
		t.Fatalf("expected nil for a nil auth context, got %#v", p)
	}
	if p := principalFromAuthContext(&coreauthn.AuthContext{}); p != nil {
		t.Fatalf("expected nil for an empty auth context, got %#v", p)
	}
}

func TestScopePrincipalFromContext(t *testing.T) {
	if _, ok := ScopePrincipalFromContext(context.Background()); ok {
		t.Fatal("expected no scope principal on an unauthenticated context")
	}

	ctx := ContextWithPrincipal(context.Background(), &Principal{
		Kind:   KindUser,
		UserID: "u1",
		Roles:  []string{"Trader"},
		Scopes: []string{"consignment:read"},
	})
	sp, ok := ScopePrincipalFromContext(ctx)
	if !ok {
		t.Fatal("expected a scope principal")
	}
	if sp.Subject() != "u1" {
		t.Fatalf("Subject() = %q, want u1", sp.Subject())
	}
	if len(sp.Roles()) != 1 || sp.Roles()[0] != "Trader" {
		t.Fatalf("Roles() = %#v", sp.Roles())
	}
	if len(sp.Scopes()) != 1 || sp.Scopes()[0] != "consignment:read" {
		t.Fatalf("Scopes() = %#v", sp.Scopes())
	}
}

// The claims a deployment lists in config.yaml reach handlers on
// Principal.ExtraClaims, while the built-ins stay on their named fields only.
func TestPrincipalFromAuthContext_UserCarriesExtraClaims(t *testing.T) {
	p := principalFromAuthContext(&coreauthn.AuthContext{
		User: &coreauthn.UserContext{
			ID: "u1",
			ExtraClaims: coreauthn.ExtraClaims{
				"email":        "officer@example.com",
				"phone_number": "+94771234567",
				"ouId":         "OU-001",
				"ouHandle":     "ou-001",
				"department":   "Customs",
				"grades":       []any{"A", "B"},
			},
		},
	})
	if p == nil {
		t.Fatal("expected a principal")
	}
	if got := p.ExtraClaims.String("department"); got != "Customs" {
		t.Errorf("department = %q, want Customs", got)
	}
	if got := p.ExtraClaims.Strings("grades"); !slices.Equal(got, []string{"A", "B"}) {
		t.Errorf("grades = %v, want [A B]", got)
	}
	for _, name := range builtinUserClaimNames {
		if _, dup := p.ExtraClaims[name]; dup {
			t.Errorf("built-in claim %q must not be repeated in ExtraClaims", name)
		}
	}
	if len(p.ExtraClaims) != 2 {
		t.Errorf("expected only the two configured extras, got %v", p.ExtraClaims)
	}
	if p.OUHandle != "ou-001" {
		t.Errorf("built-in OUHandle lost: %#v", p)
	}
}

// captureProfileService records the Principal a UserProfileService is called
// with.
type captureProfileService struct{ got *Principal }

func (c *captureProfileService) GetOrCreateUser(_ context.Context, p *Principal) (string, error) {
	c.got = p
	return "u1", nil
}

// UserProfileService is called with a Principal built from core's user
// principal, which must carry the same extra claims the request's Principal
// does, and keep the built-ins on their named fields only.
func TestCoreProfileShim_CarriesExtraClaims(t *testing.T) {
	svc := &captureProfileService{}
	id, err := coreProfileShim{svc: svc}.GetOrCreateUser(context.Background(), &coreauthn.UserPrincipal{
		Subject: "idp1",
		Roles:   []string{"Officer"},
		Scopes:  []string{"task:read"},
		ExtraClaims: coreauthn.ExtraClaims{
			"email":        "officer@example.com",
			"phone_number": "+94771234567",
			"ouId":         "OU-001",
			"ouHandle":     "ou-001",
			"department":   "Customs",
			"grades":       []any{"A", "B"},
		},
	})
	if err != nil || id != "u1" {
		t.Fatalf("GetOrCreateUser = %q, %v", id, err)
	}

	p := svc.got
	if p == nil {
		t.Fatal("expected the service to be called with a principal")
	}
	if p.Kind != KindUser || p.IDPUserID != "idp1" || p.Email != "officer@example.com" ||
		p.PhoneNumber != "+94771234567" || p.OUID != "OU-001" || p.OUHandle != "ou-001" {
		t.Fatalf("named fields lost: %#v", p)
	}
	if got := p.ExtraClaims.String("department"); got != "Customs" {
		t.Errorf("department = %q, want Customs", got)
	}
	if got := p.ExtraClaims.Strings("grades"); !slices.Equal(got, []string{"A", "B"}) {
		t.Errorf("grades = %v, want [A B]", got)
	}
	for _, name := range builtinUserClaimNames {
		if _, dup := p.ExtraClaims[name]; dup {
			t.Errorf("built-in claim %q must not be repeated in ExtraClaims", name)
		}
	}
	if len(p.ExtraClaims) != 2 {
		t.Errorf("expected only the two extras, got %v", p.ExtraClaims)
	}
}

func TestPrincipalFromAuthContext_ClientCarriesExtraClaims(t *testing.T) {
	p := principalFromAuthContext(&coreauthn.AuthContext{
		Client: &coreauthn.ClientContext{
			ClientID:    "NSW_TO_NPQS",
			ExtraClaims: coreauthn.ExtraClaims{"agency": "npqs"},
		},
	})
	if p == nil {
		t.Fatal("expected a principal")
	}
	if p.Kind != KindClient || p.ExtraClaims.String("agency") != "npqs" {
		t.Fatalf("unexpected client principal: %#v", p)
	}
	// A client's claims are not subject to the user built-in reservation: they
	// are never flattened onto fields, so every extracted name is kept.
	p = principalFromAuthContext(&coreauthn.AuthContext{
		Client: &coreauthn.ClientContext{ExtraClaims: coreauthn.ExtraClaims{"email": "svc@example.com"}},
	})
	if p.ExtraClaims.String("email") != "svc@example.com" {
		t.Fatalf("client claim named like a user built-in was dropped: %#v", p.ExtraClaims)
	}
}

// Nothing configured, or nothing present in the token, leaves ExtraClaims nil.
func TestPrincipalFromAuthContext_NoExtraClaimsIsNil(t *testing.T) {
	for name, ac := range map[string]*coreauthn.AuthContext{
		"user without extras": {User: &coreauthn.UserContext{ID: "u1"}},
		"user with only built-ins": {User: &coreauthn.UserContext{
			ExtraClaims: coreauthn.ExtraClaims{"email": "a@example.com", "ouHandle": "ou-1"},
		}},
		"client without extras": {Client: &coreauthn.ClientContext{ClientID: "SVC"}},
	} {
		t.Run(name, func(t *testing.T) {
			if p := principalFromAuthContext(ac); p == nil || p.ExtraClaims != nil {
				t.Fatalf("expected a principal with nil ExtraClaims, got %#v", p)
			}
		})
	}
}

// core/authn shares its claim map with the profile-service shim and the rest of
// the request, so the Principal must hold its own copy.
func TestPrincipalFromAuthContext_ExtraClaimsAreCopied(t *testing.T) {
	src := coreauthn.ExtraClaims{"department": "Customs"}
	p := principalFromAuthContext(&coreauthn.AuthContext{User: &coreauthn.UserContext{ExtraClaims: src}})

	p.ExtraClaims["department"] = "tampered"
	p.ExtraClaims["added"] = "x"
	delete(p.ExtraClaims, "department")

	if len(src) != 1 || src["department"] != "Customs" {
		t.Fatalf("core's claim map was changed through the Principal: %v", src)
	}
}

func TestClaims_String(t *testing.T) {
	c := Claims{"dept": "Customs", "level": float64(3), "grades": []any{"A"}}
	tests := []struct {
		name, claim, want string
	}{
		{"string claim", "dept", "Customs"},
		{"number is not a string", "level", ""},
		{"array is not a string", "grades", ""},
		{"absent", "missing", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.String(tt.claim); got != tt.want {
				t.Fatalf("String(%q) = %q, want %q", tt.claim, got, tt.want)
			}
		})
	}
	if got := Claims(nil).String("dept"); got != "" {
		t.Fatalf("nil Claims: String = %q, want empty", got)
	}
}

func TestClaims_Strings(t *testing.T) {
	c := Claims{
		"json array":  []any{"A", "B"},
		"empty array": []any{},
		"mixed array": []any{"A", float64(1)},
		"free text":   "Customs Division",
		"number":      float64(3),
	}
	tests := []struct {
		claim string
		want  []string
	}{
		{"json array", []string{"A", "B"}},
		{"empty array", []string{}},
		{"mixed array", nil},
		// A string is not split on spaces: a free-text attribute must not
		// turn into two values.
		{"free text", nil},
		{"number", nil},
		{"missing", nil},
	}
	for _, tt := range tests {
		t.Run(tt.claim, func(t *testing.T) {
			got := c.Strings(tt.claim)
			if !slices.Equal(got, tt.want) || (tt.want == nil) != (got == nil) {
				t.Fatalf("Strings(%q) = %#v, want %#v", tt.claim, got, tt.want)
			}
		})
	}
	if got := Claims(nil).Strings("json array"); got != nil {
		t.Fatalf("nil Claims: Strings = %#v, want nil", got)
	}

	// The returned slice is the caller's own.
	got := c.Strings("json array")
	got[0] = "tampered"
	if c.Strings("json array")[0] != "A" {
		t.Fatal("Strings returned the claim's backing data")
	}
}
