// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package systemlog

import "context"

// Attrs is a request-scoped slot Middleware allocates before ServeHTTP.
// Handlers call Set to add identifiers (e.g. taskId) onto the system http
// request line. Values are not request bodies.
type Attrs struct {
	pairs []any
}

type attrsKey struct{}

// WithAttrs returns ctx carrying slot.
func WithAttrs(ctx context.Context, slot *Attrs) context.Context {
	return context.WithValue(ctx, attrsKey{}, slot)
}

// AttrsFromContext returns the slot on ctx, or nil when none was allocated.
func AttrsFromContext(ctx context.Context) *Attrs {
	slot, _ := ctx.Value(attrsKey{}).(*Attrs)
	return slot
}

// Set appends key and value onto the request's Attrs slot for Middleware to
// include on the system http request line. Empty keys or values are ignored.
// No-op when no slot is on ctx (handler ran outside Middleware).
func Set(ctx context.Context, key, value string) {
	if key == "" || value == "" {
		return
	}
	slot := AttrsFromContext(ctx)
	if slot == nil {
		return
	}
	slot.pairs = append(slot.pairs, key, value)
}

func (a *Attrs) appendTo(attrs []any) []any {
	if a == nil || len(a.pairs) == 0 {
		return attrs
	}
	return append(attrs, a.pairs...)
}
