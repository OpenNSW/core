// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package xmlgen

import (
	"context"
	"errors"
	"fmt"
	"text/template"
	"unicode"
)

// builtinFuncs are the function names text/template defines. A caller function
// silently shadows a builtin, so resolvers are rejected if they collide.
var builtinFuncs = map[string]bool{
	"and": true, "call": true, "html": true, "index": true, "slice": true,
	"js": true, "len": true, "not": true, "or": true, "print": true,
	"printf": true, "println": true, "urlquery": true,
	"eq": true, "ge": true, "gt": true, "le": true, "lt": true, "ne": true,
}

// reservedName reports whether a name is one a resolver may not take.
func reservedName(name string) bool {
	if builtinFuncs[name] {
		return true
	}
	_, ok := helperFuncs()[name]
	return ok
}

// ResolveFunc is a caller-supplied function a template may call by name. It
// exists for values that cannot be computed from the data alone — a code-list
// description fetched from a database, a rate looked up in another service.
// Pure formatting is already covered by the built-in helpers.
//
// Arguments arrive as the Go values the template evaluated: string, bool,
// json.Number when xmlgen decoded the data from JSON, float64 when the caller
// passed an already-decoded map, map[string]any, []any, or nil.
//
// A ResolveFunc must be safe for concurrent use, and must not have durable
// side effects. text/template calls a function once per evaluated occurrence
// and skips occurrences in branches it does not take, so a counter increment
// would depend on the shape of the template and would run again on a retried
// render. Mint reference numbers before calling Generate and put them in the
// data.
type ResolveFunc func(ctx context.Context, args ...any) (any, error)

// Resolvers maps template-visible names to functions. Each name must be a
// valid Go identifier and must not collide with a text/template builtin or a
// function xmlgen defines itself.
//
// Each resolver is registered under its own name, so {{ codelist ... }} rather
// than {{ resolve "codelist" ... }}. A template that calls a resolver which
// was not supplied then fails when the template is parsed, instead of at
// execution time if and when a branch happens to be taken.
type Resolvers map[string]ResolveFunc

// resolverError tags a caller's error with the resolver that produced it, so
// it survives text/template's own wrapping and can be recovered afterwards.
type resolverError struct {
	name string
	err  error
}

func (e *resolverError) Error() string { return "resolver " + e.name + ": " + e.err.Error() }
func (e *resolverError) Unwrap() error { return e.err }

// buildFuncMap assembles the functions a template may call: xmlgen's own, then
// the caller's resolvers bound to ctx.
func buildFuncMap(ctx context.Context, resolvers Resolvers) (template.FuncMap, error) {
	funcs := helperFuncs()
	for name, fn := range resolvers {
		if err := checkResolverName(name, fn); err != nil {
			return nil, err
		}
		funcs[name] = bindResolver(ctx, name, fn)
	}
	return funcs, nil
}

// checkResolverName rejects what text/template would panic on, plus names that
// would shadow a builtin.
func checkResolverName(name string, fn ResolveFunc) error {
	if !goodName(name) {
		return fmt.Errorf("%w: %q", ErrInvalidResolverName, name)
	}
	if fn == nil {
		return fmt.Errorf("%w: resolver %q is nil", ErrInvalidResolverName, name)
	}
	if reservedName(name) {
		return fmt.Errorf("%w: %q", ErrReservedResolverName, name)
	}
	return nil
}

// goodName reports whether a name is one text/template will accept. It mirrors
// the check in text/template's Funcs, which panics rather than returning an
// error — so a resolver named "code-list" would otherwise take down the
// process rather than fail the request.
func goodName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r == '_':
		case i == 0 && !unicode.IsLetter(r):
			return false
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			return false
		}
	}
	return true
}

func bindResolver(ctx context.Context, name string, fn ResolveFunc) func(...any) (any, error) {
	return func(args ...any) (any, error) {
		v, err := fn(ctx, args...)
		if err != nil {
			return nil, &resolverError{name: name, err: err}
		}
		return v, nil
	}
}

// asResolverError recovers a tagged resolver error from whatever
// text/template wrapped it in.
func asResolverError(err error) (*resolverError, bool) {
	var re *resolverError
	ok := errors.As(err, &re)
	return re, ok
}
