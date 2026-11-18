package taskauthz

import (
	"context"
	"errors"
	"testing"
)

var testCatalog = Catalog{
	Roles:   map[string]string{"trader": "Trader", "cha": "CHA"},
	Clients: map[string]string{"fcau": "FCAU_TO_NSW"},
}

func ownedBy(owned map[string]bool, calls *int) OwnedRolesFunc {
	return func(context.Context, string) (map[string]bool, error) {
		*calls++
		return owned, nil
	}
}

// Eligibility is the conjunction both task-authz paths rest on: holding a role is
// not enough, and owning a slot is not enough. Every case fixes one and varies the
// other, so dropping either conjunct fails here.
func TestEligible_RequiresBothHeldRoleAndOwnedSlot(t *testing.T) {
	tests := []struct {
		name      string
		heldRoles []string
		owned     map[string]bool
		names     []string
		want      map[string]bool
	}{
		{
			name:      "holds and owns",
			heldRoles: []string{"Trader"},
			owned:     map[string]bool{"trader": true},
			names:     []string{"trader", "cha"},
			want:      map[string]bool{"trader": true, "cha": false},
		},
		{
			name:      "owns the slot but does not hold the role",
			heldRoles: []string{"Trader"},
			owned:     map[string]bool{"trader": true, "cha": true},
			names:     []string{"cha"},
			want:      map[string]bool{"cha": false},
		},
		{
			name:      "holds the role but does not own the slot",
			heldRoles: []string{"CHA"},
			owned:     map[string]bool{"cha": false},
			names:     []string{"cha"},
			want:      map[string]bool{"cha": false},
		},
		{
			name:      "self-clearing company holding both roles is eligible for both",
			heldRoles: []string{"Trader", "CHA"},
			owned:     map[string]bool{"trader": true, "cha": true},
			names:     []string{"trader", "cha"},
			want:      map[string]bool{"trader": true, "cha": true},
		},
		{
			name:      "a name the catalog does not define is never eligible",
			heldRoles: []string{"Trader"},
			owned:     map[string]bool{"chaa": true},
			names:     []string{"chaa"},
			want:      map[string]bool{"chaa": false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			in := Input{Kind: KindUser, Roles: tt.heldRoles, OwnedRoles: ownedBy(tt.owned, &calls)}

			got, err := testCatalog.Eligible(context.Background(), in, "consignment-1", tt.names)
			if err != nil {
				t.Fatalf("Eligible: %v", err)
			}
			for name, want := range tt.want {
				if got.Allows(name) != want {
					t.Errorf("Allows(%q) = %v, want %v", name, got.Allows(name), want)
				}
			}
		})
	}
}

// Ownership costs a database round trip, so it must be skipped whenever the token
// alone already settles the answer. Both policy paths depend on this.
func TestEligible_ResolvesOwnershipOnlyWhenItCanMatter(t *testing.T) {
	tests := []struct {
		name         string
		in           func(calls *int) Input
		names        []string
		wantResolves int
	}{
		{
			name: "caller holds one of the names",
			in: func(c *int) Input {
				return Input{Kind: KindUser, Roles: []string{"CHA"}, OwnedRoles: ownedBy(map[string]bool{"cha": true}, c)}
			},
			names:        []string{"cha"},
			wantResolves: 1,
		},
		{
			name: "caller holds none of the names",
			in: func(c *int) Input {
				return Input{Kind: KindUser, Roles: []string{"Trader"}, OwnedRoles: ownedBy(map[string]bool{"cha": true}, c)}
			},
			names:        []string{"cha"},
			wantResolves: 0,
		},
		{
			name: "caller holds no catalog role at all",
			in: func(c *int) Input {
				return Input{Kind: KindUser, Roles: []string{"Auditor"}, OwnedRoles: ownedBy(map[string]bool{"trader": true}, c)}
			},
			names:        []string{"trader", "cha"},
			wantResolves: 0,
		},
		{
			name: "client principals own nothing",
			in: func(c *int) Input {
				return Input{Kind: KindClient, ClientID: "FCAU_TO_NSW", OwnedRoles: ownedBy(map[string]bool{"trader": true}, c)}
			},
			names:        []string{"trader"},
			wantResolves: 0,
		},
		{
			name: "no names to check",
			in: func(c *int) Input {
				return Input{Kind: KindUser, Roles: []string{"Trader"}, OwnedRoles: ownedBy(map[string]bool{"trader": true}, c)}
			},
			names:        nil,
			wantResolves: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			got, err := testCatalog.Eligible(context.Background(), tt.in(&calls), "consignment-1", tt.names)
			if err != nil {
				t.Fatalf("Eligible: %v", err)
			}
			if calls != tt.wantResolves {
				t.Errorf("ownership resolved %d times, want %d", calls, tt.wantResolves)
			}
			if tt.wantResolves == 0 && got.Any(tt.names) {
				t.Error("nothing may be allowed when ownership was never resolved")
			}
		})
	}
}

// A user principal with no resolver attached is a Layer 1 wiring bug. Report it as
// "eligible for nothing" and let the policy layer decide what that means, rather
// than guessing at a status code here.
func TestEligible_NilResolverIsEligibleForNothing(t *testing.T) {
	in := Input{Kind: KindUser, Roles: []string{"Trader"}}

	got, err := testCatalog.Eligible(context.Background(), in, "consignment-1", []string{"trader"})
	if err != nil {
		t.Fatalf("Eligible: %v", err)
	}
	if !got.HoldsAny([]string{"trader"}) {
		t.Error("the token role is still held; only ownership is unknown")
	}
	if got.Allows("trader") {
		t.Error("nothing may be allowed without resolved ownership")
	}
}

func TestEligible_OwnershipErrorPropagates(t *testing.T) {
	sentinel := errors.New("boom")
	in := Input{
		Kind:  KindUser,
		Roles: []string{"Trader"},
		OwnedRoles: func(context.Context, string) (map[string]bool, error) {
			return nil, sentinel
		},
	}

	if _, err := testCatalog.Eligible(context.Background(), in, "consignment-1", []string{"trader"}); !errors.Is(err, sentinel) {
		t.Fatalf("got %v, want the resolver's error wrapped", err)
	}
}

// HoldsAny separates "you are not this kind of user" from "you are, but this is
// not your consignment" — the same denial, worth telling apart in a log.
func TestEligibility_HoldsAnyIgnoresOwnership(t *testing.T) {
	calls := 0
	in := Input{Kind: KindUser, Roles: []string{"Trader"}, OwnedRoles: ownedBy(map[string]bool{"trader": false}, &calls)}

	got, err := testCatalog.Eligible(context.Background(), in, "consignment-1", []string{"trader", "cha"})
	if err != nil {
		t.Fatalf("Eligible: %v", err)
	}
	if !got.HoldsAny([]string{"trader", "cha"}) {
		t.Error("HoldsAny should report the held role even when it owns nothing")
	}
	if got.Any([]string{"trader", "cha"}) {
		t.Error("Any must stay false when no slot is owned")
	}
}

func TestWithInput_RoundTrips(t *testing.T) {
	if _, ok := InputFromContext(context.Background()); ok {
		t.Error("a bare context must carry no Input")
	}
	ctx := WithInput(context.Background(), Input{Kind: KindUser, Roles: []string{"Trader"}})
	got, ok := InputFromContext(ctx)
	if !ok || got.Kind != KindUser || len(got.Roles) != 1 {
		t.Fatalf("got (%+v, %v), want the attached Input", got, ok)
	}
}

func TestRoleNames_IsSorted(t *testing.T) {
	got := testCatalog.RoleNames()
	if len(got) != 2 || got[0] != "cha" || got[1] != "trader" {
		t.Errorf("RoleNames() = %v, want [cha trader]", got)
	}
	if len(Catalog{}.RoleNames()) != 0 {
		t.Error("an empty catalog must yield no role names")
	}
}
