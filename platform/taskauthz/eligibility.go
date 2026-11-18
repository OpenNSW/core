package taskauthz

import (
	"context"
	"fmt"
	"sort"
)

// Eligibility answers, for one caller and one task, which logical roles that
// caller may act in.
//
// A role is eligible only when the caller holds its token role **and** their
// company owns the task's consignment in that same role. Holding the role is not
// enough: a user with only a Trader token, at a company that happens to sit in
// some consignment's CHA slot, may not act as its CHA. Both task authorization
// paths rest on this rule, which is why it is computed in exactly one place.
type Eligibility struct {
	held  map[string]bool // logical name -> caller holds the mapped token role
	owned map[string]bool // logical name -> company owns that slot; empty if never resolved
}

// HoldsAny reports whether the caller holds the token role of any of names.
// It distinguishes "you are not this kind of user" from "you are, but this is not
// your consignment" — the same denial, but worth telling apart in a log.
func (e Eligibility) HoldsAny(names []string) bool {
	for _, name := range names {
		if e.held[name] {
			return true
		}
	}
	return false
}

// Allows reports whether the caller may act as the logical role name.
func (e Eligibility) Allows(name string) bool { return e.held[name] && e.owned[name] }

// Any reports whether the caller may act as any of names.
func (e Eligibility) Any(names []string) bool {
	for _, name := range names {
		if e.Allows(name) {
			return true
		}
	}
	return false
}

// Eligible determines which of names the caller may act in on a task of case
// caseID.
//
// Ownership costs a database round trip, so it is resolved at most once and only
// when the caller holds the token role of at least one of names. A caller whose
// roles match none of them — the common denial — is answered from the token
// alone. A client principal, or a user with no ownership resolver attached, is
// eligible for nothing; both are reported as such rather than as an error, so the
// decision stays with the policy layer that knows what a denial means.
func (c Catalog) Eligible(ctx context.Context, in Input, caseID string, names []string) (Eligibility, error) {
	e := Eligibility{
		held:  make(map[string]bool, len(names)),
		owned: map[string]bool{},
	}
	if in.Kind != KindUser {
		return e, nil
	}

	tokenRoles := make(map[string]bool, len(in.Roles))
	for _, r := range in.Roles {
		tokenRoles[r] = true
	}
	for _, name := range names {
		role, isRole := c.Roles[name]
		e.held[name] = isRole && tokenRoles[role]
	}

	if !e.HoldsAny(names) || in.OwnedRoles == nil {
		return e, nil
	}

	owned, err := in.OwnedRoles(ctx, caseID)
	if err != nil {
		return Eligibility{}, fmt.Errorf("taskauthz: resolve ownership: %w", err)
	}
	e.owned = owned
	return e, nil
}

// RoleNames returns every logical role name the catalog defines, sorted so the
// result is stable across calls — Go map iteration order is not. Callers pass it
// to Eligible when the set of roles under consideration is "all of them"; a
// caller working from a narrower, configured set passes that instead.
func (c Catalog) RoleNames() []string {
	names := make([]string, 0, len(c.Roles))
	for name := range c.Roles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
