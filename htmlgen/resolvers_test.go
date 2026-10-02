// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package htmlgen_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/OpenNSW/core/htmlgen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withResolver(name string, fn htmlgen.ResolveFunc) htmlgen.Option {
	return htmlgen.WithResolvers(htmlgen.Resolvers{name: fn})
}

func TestResolvers_Invocation(t *testing.T) {
	t.Run("is called by its own name with the template's arguments", func(t *testing.T) {
		doc := render(t, `{{ codelist "country" .v }}`, map[string]any{"v": "LK"},
			withResolver("codelist", func(_ context.Context, args ...any) (any, error) {
				require.Len(t, args, 2)
				assert.Equal(t, "country", args[0])
				assert.Equal(t, "LK", args[1])
				return "Example Country", nil
			}))
		assert.Equal(t, "Example Country", doc)
	})

	t.Run("receives the caller's own Go values, not wrappers", func(t *testing.T) {
		var got []any
		render(t, `{{ probe .s .n .o .missing }}`, []byte(`{"s":"x","n":1.5,"o":{"k":"v"}}`),
			withResolver("probe", func(_ context.Context, args ...any) (any, error) {
				got = args
				return "", nil
			}))
		require.Len(t, got, 4)
		assert.IsType(t, "", got[0])
		assert.IsType(t, json.Number(""), got[1], "JSON numbers reach a resolver as json.Number")
		assert.IsType(t, map[string]any{}, got[2])
		assert.Nil(t, got[3], "an absent key arrives as nil")
	})

	t.Run("its return value is escaped", func(t *testing.T) {
		doc := render(t, `{{ danger }}`, nil,
			withResolver("danger", func(context.Context, ...any) (any, error) {
				return "<Evil/>", nil
			}))
		assert.Contains(t, doc, "&lt;Evil/&gt;")
	})

	t.Run("sees the context passed to Generate", func(t *testing.T) {
		type key struct{}
		ctx := context.WithValue(context.Background(), key{}, "carried")
		out, err := htmlgen.Generate(ctx, []byte(`<R>{{ peek }}</R>`), nil,
			withResolver("peek", func(c context.Context, _ ...any) (any, error) {
				return c.Value(key{}), nil
			}))
		require.NoError(t, err)
		assert.Contains(t, string(out), "carried")
	})
}

func TestResolvers_Errors(t *testing.T) {
	t.Run("a resolver error matches both sentinels", func(t *testing.T) {
		sentinel := errors.New("code list unavailable")
		_, err := htmlgen.Generate(context.Background(), []byte(`<R>{{ codelist }}</R>`), nil,
			withResolver("codelist", func(context.Context, ...any) (any, error) {
				return nil, sentinel
			}))
		require.Error(t, err)
		assert.ErrorIs(t, err, htmlgen.ErrResolver)
		assert.ErrorIs(t, err, sentinel, "the caller's own error must remain reachable")
		assert.Contains(t, err.Error(), "codelist")
	})

	t.Run("an unregistered resolver fails when the template is parsed", func(t *testing.T) {
		// The call sits in a branch the data would never take, so an
		// execution-time check would never reach it.
		_, err := htmlgen.Generate(context.Background(),
			[]byte(`<R>{{ if .never }}{{ nosuch }}{{ end }}</R>`), map[string]any{"never": false})
		require.ErrorIs(t, err, htmlgen.ErrParseTemplate)
		assert.Contains(t, err.Error(), "not defined")
	})

	t.Run("a panicking resolver is recovered rather than crashing", func(t *testing.T) {
		require.NotPanics(t, func() {
			_, err := htmlgen.Generate(context.Background(), []byte(`<R>{{ boom }}</R>`), nil,
				withResolver("boom", func(context.Context, ...any) (any, error) {
					panic("resolver exploded")
				}))
			require.Error(t, err)
		})
	})
}

func TestResolvers_NameValidation(t *testing.T) {
	// text/template's Funcs panics on a name that is not a Go identifier, so
	// an unchecked name would take down the process rather than fail the
	// request. This is the reason names are validated first.
	t.Run("a name that is not an identifier is an error, never a panic", func(t *testing.T) {
		for _, bad := range []string{"code-list", "1abc", "", "my func", "a.b"} {
			require.NotPanics(t, func() {
				_, err := htmlgen.Generate(context.Background(), []byte(`<R/>`), nil,
					withResolver(bad, func(context.Context, ...any) (any, error) { return "", nil }))
				require.ErrorIs(t, err, htmlgen.ErrInvalidResolverName, "name %q", bad)
			}, "name %q must not panic", bad)
		}
	})

	t.Run("a nil resolver is refused", func(t *testing.T) {
		_, err := htmlgen.Generate(context.Background(), []byte(`<R/>`), nil,
			withResolver("thing", nil))
		require.ErrorIs(t, err, htmlgen.ErrInvalidResolverName)
	})

	t.Run("shadowing a builtin or a helper is refused", func(t *testing.T) {
		// html/template inserts its escapers as functions named _html_template_*,
		// so a resolver under one of those names would replace the escaper.
		// break and continue are keywords only while no function of that name
		// exists, so registering one silently changes what {{ break }} does in
		// every template rather than merely shadowing a function.
		for _, name := range []string{
			"index", "printf", "eq", "len",
			"text", "date", "lookup", "zero",
			"_html_template_htmlescaper", "_html_template_urlfilter",
			"break", "continue",
		} {
			_, err := htmlgen.Generate(context.Background(), []byte(`<R/>`), nil,
				withResolver(name, func(context.Context, ...any) (any, error) { return "", nil }))
			require.ErrorIs(t, err, htmlgen.ErrReservedResolverName, "name %q", name)
		}
	})

	t.Run("no resolvers at all is fine", func(t *testing.T) {
		_, err := htmlgen.Generate(context.Background(), []byte(`<R/>`), nil,
			htmlgen.WithResolvers(nil))
		require.NoError(t, err)
	})
}

// TestResolvers_CallCount pins the hazard documented on ResolveFunc: a
// resolver runs once per evaluated occurrence, and not at all in a branch that
// is not taken. A resolver with durable side effects would therefore behave
// differently depending on the shape of the template.
func TestResolvers_CallCount(t *testing.T) {
	count := func(tmpl string, data any) int64 {
		var calls atomic.Int64
		_, err := htmlgen.Generate(context.Background(), []byte(tmpl), data,
			withResolver("mint", func(context.Context, ...any) (any, error) {
				calls.Add(1)
				return "REF", nil
			}))
		require.NoError(t, err)
		return calls.Load()
	}

	assert.Equal(t, int64(2), count(`<R>{{ mint }}{{ mint }}</R>`, nil),
		"two occurrences are two calls")
	assert.Equal(t, int64(0), count(`<R>{{ if .go }}{{ mint }}{{ end }}</R>`, map[string]any{"go": false}),
		"an untaken branch calls nothing")
	assert.Equal(t, int64(1), count(`<R>{{ $r := mint }}{{ $r }}{{ $r }}{{ $r }}</R>`, nil),
		"binding to a variable is the documented way to mint once")
}
