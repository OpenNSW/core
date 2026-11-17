package systemlog

import (
	"context"
	"log/slog"
)

// StampCategory wraps next so any record that does not already carry a
// category attribute gets Category ("system"). Security and audit loggers set
// their own category via WithAttrs; those records are left alone. Bootstrap
// wraps the process handlers with this so plain slog.* calls are filterable
// as category=system without each call site setting the attribute.
func StampCategory(next slog.Handler) slog.Handler {
	if next == nil {
		return nil
	}
	return &categoryStamp{next: next}
}

type categoryStamp struct {
	next        slog.Handler
	hasCategory bool
}

func (h *categoryStamp) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *categoryStamp) Handle(ctx context.Context, r slog.Record) error {
	if !h.hasCategory && !recordHasCategory(r) {
		r.AddAttrs(slog.String("category", Category))
	}
	return h.next.Handle(ctx, r)
}

func (h *categoryStamp) WithAttrs(attrs []slog.Attr) slog.Handler {
	has := h.hasCategory
	for _, a := range attrs {
		if a.Key == "category" {
			has = true
			break
		}
	}
	return &categoryStamp{next: h.next.WithAttrs(attrs), hasCategory: has}
}

func (h *categoryStamp) WithGroup(name string) slog.Handler {
	return &categoryStamp{next: h.next.WithGroup(name), hasCategory: h.hasCategory}
}

func recordHasCategory(r slog.Record) bool {
	found := false
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "category" {
			found = true
			return false
		}
		return true
	})
	return found
}
