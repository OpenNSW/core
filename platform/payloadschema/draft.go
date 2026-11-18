package payloadschema

import "context"

// draftKey is the context key WithDraft sets.
type draftKey struct{}

// WithDraft returns ctx marked as a draft save. Only the draft route sets it,
// so the extension knows a draft from a submission by the route that was
// called, not by the command in the request: a submission naming the step's
// draft command is still a submission and is validated in full.
func WithDraft(ctx context.Context) context.Context {
	return context.WithValue(ctx, draftKey{}, true)
}

// IsDraft reports whether ctx was marked by WithDraft.
func IsDraft(ctx context.Context) bool {
	draft, _ := ctx.Value(draftKey{}).(bool)
	return draft
}
