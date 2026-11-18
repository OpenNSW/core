package taskauthz

import (
	"context"
	"testing"
)

// The context round-trip is the seam between Layer 1 and every Layer-2
// evaluator: the middleware puts an Input in, the evaluator takes it out. A
// broken key would not fail to compile — every evaluator would simply see an
// unauthenticated caller and deny, which reads as a policy bug rather than a
// plumbing one.
func TestInputRoundTripsThroughContext(t *testing.T) {
	want := Input{Kind: KindUser, Roles: []string{"Trader"}, ClientID: "portal"}

	got, ok := InputFromContext(WithInput(context.Background(), want))
	if !ok {
		t.Fatal("InputFromContext: not present after WithInput")
	}
	if got.Kind != want.Kind || got.ClientID != want.ClientID || len(got.Roles) != 1 {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// WithSystem carries the system principal and nothing else: no roles or client id
// a rule could mistake for a user's or a client's.
func TestWithSystem(t *testing.T) {
	got, ok := InputFromContext(WithSystem(context.Background()))
	if !ok || got.Kind != KindSystem || got.Roles != nil || got.ClientID != "" || got.OwnedRoles != nil {
		t.Errorf("WithSystem: got %+v, %v; want only Kind %q", got, ok, KindSystem)
	}
}

// The system principal is never eligible for a catalog role, even one that names
// it: roles are for users, and the reservation keeps the name out of the catalog.
func TestSystemIsEligibleForNoRole(t *testing.T) {
	cat := Catalog{Roles: map[string]string{"trader": "Trader", SystemPrincipal: "Trader"}}
	e, err := cat.Eligible(context.Background(), Input{Kind: KindSystem}, "c1", []string{"trader", SystemPrincipal})
	if err != nil {
		t.Fatalf("Eligible: %v", err)
	}
	if e.HoldsAny([]string{"trader", SystemPrincipal}) || e.Any([]string{"trader", SystemPrincipal}) {
		t.Error("the system principal is eligible for a role")
	}
}

// A request that never passed through Layer 1 must be distinguishable from one
// carrying a principal, rather than yielding a usable zero value.
func TestInputAbsentFromBareContext(t *testing.T) {
	if _, ok := InputFromContext(context.Background()); ok {
		t.Error("InputFromContext: reported present on a context with no Input")
	}
}

// The key is unexported and typed, so nothing outside this package can collide
// with it — including a plain string key of the same shape.
func TestInputIgnoresForeignContextKeys(t *testing.T) {
	//nolint:staticcheck // deliberately using a string key: that is the collision under test.
	ctx := context.WithValue(context.Background(), "taskauthz.ctxKey", Input{Kind: KindUser})

	if _, ok := InputFromContext(ctx); ok {
		t.Error("InputFromContext: a foreign key satisfied the lookup")
	}
}
