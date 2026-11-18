// Package taskauthz is the shared vocabulary of task authorization: who the
// caller is, what the deployment's logical principal names mean, and which roles
// the caller may act in on a given task.
//
// It holds no policy. Three packages build on it, and none of them depends on
// another:
//
//   - internal/tasks/authzgate — Layer 1. An HTTP middleware that resolves the
//     caller's identity into an Input and attaches it to the request context,
//     along with a lazy ownership resolver.
//   - internal/tasks/extensions/authz — Layer 2 for writes. A PRE_RESUME task
//     extension deciding whether the caller may run a command on a task.
//   - internal/tasks/readauthz — Layer 2 for reads. Decides whether the caller may
//     read a task at all.
//
// These types used to live in the write extension. But only one of the packages
// that need them is a core task extension, so the shared half cannot live under
// extensions/ without Layer 1 and the read evaluator importing a write-policy
// package for types that have nothing to do with write policy. Eligibility is
// here for the same reason: both Layer-2 evaluators rest on that one rule, and a
// security rule with two implementations can drift.
package taskauthz

import "context"

// PrincipalKind distinguishes a portal user from an M2M service client, and both
// from this service's own code.
type PrincipalKind string

const (
	KindUser   PrincipalKind = "user"
	KindClient PrincipalKind = "client"
	// KindSystem is this service acting on its own behalf: an in-process caller,
	// such as a webhook handler or the payment service, completing a step it was
	// told about by some other means. No request ever resolves to it.
	KindSystem PrincipalKind = "system"
)

// SystemPrincipal is the logical name a rule uses to let in-process callers
// (KindSystem) act. It is reserved: the catalog may not define it as a role or a
// client, so no user or M2M caller can ever match it.
const SystemPrincipal = "system"

// OwnedRolesFunc lazily resolves which logical owner roles the caller's company
// satisfies for the task's case (keyed by the catalog's logical role
// names, e.g. "trader"/"cha"). It performs the database work, so callers invoke
// it only once they know the answer can matter. It is nil for client principals.
type OwnedRolesFunc func(ctx context.Context, caseID string) (map[string]bool, error)

// Input is the authorization context Layer 1 resolves and attaches for a Layer-2
// evaluator to judge. It carries no domain detail — only the caller's kind, token
// roles, client or user id, and a lazy ownership resolver.
type Input struct {
	Kind       PrincipalKind
	Roles      []string
	ClientID   string
	OwnedRoles OwnedRolesFunc

	// UserID is the caller's internal user id (user_records.id). It is the holder a
	// task claim is recorded under, so a user's claims follow them across logins and
	// IdP changes. Empty for client principals.
	UserID string
}

type ctxKey struct{}

// WithInput returns a context carrying in for an evaluator to read. Layer 1 is
// its only intended caller, besides WithSystem: minting an Input elsewhere would
// let a handler assert an identity nothing verified.
func WithInput(ctx context.Context, in Input) context.Context {
	return context.WithValue(ctx, ctxKey{}, in)
}

// WithSystem returns a context carrying the system principal, for a caller in this
// process that completes a step on its own authority rather than on a request's:
// the payment service settling a payment, or a webhook handler resuming the step
// an external event answers. Layer 1 never attaches it, so it cannot be reached
// over HTTP; only code that is itself the authority for the step may use it.
func WithSystem(ctx context.Context) context.Context {
	return WithInput(ctx, Input{Kind: KindSystem})
}

// InputFromContext returns the Input attached by WithInput; ok is false when none
// is present (an unauthenticated request).
func InputFromContext(ctx context.Context) (Input, bool) {
	in, ok := ctx.Value(ctxKey{}).(Input)
	return in, ok
}
