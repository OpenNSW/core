// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package xmlgen_test

import (
	"context"
	"testing"

	"github.com/OpenNSW/core/xmlgen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHelpers(t *testing.T) {
	t.Run("part returns empty past the end instead of failing", func(t *testing.T) {
		doc := render(t, `[{{ part .v "/" 0 }}][{{ part .v "/" 5 }}]`, map[string]any{"v": "a/b"})
		assert.Equal(t, "[a][]", inner(t, doc))
	})

	t.Run("part on an absent value is empty", func(t *testing.T) {
		doc := render(t, `[{{ part .missing " " 0 }}]`, map[string]any{})
		assert.Equal(t, "[]", inner(t, doc))
	})

	t.Run("split feeds range", func(t *testing.T) {
		doc := render(t, `{{ range split .v "," }}<E>{{ . }}</E>{{ end }}`, map[string]any{"v": "a,b,c"})
		assert.Equal(t, "<R><E>a</E><E>b</E><E>c</E></R>", doc)
	})

	t.Run("date reformats between layouts", func(t *testing.T) {
		doc := render(t, `{{ date .v "2006-01-02" "1/2/06" }}`, map[string]any{"v": "2026-03-04"})
		assert.Equal(t, "3/4/26", inner(t, doc))
	})

	t.Run("date refuses a value that does not match the layout", func(t *testing.T) {
		_, err := xmlgen.Generate(context.Background(),
			[]byte(`<R>{{ date .v "2006-01-02" "1/2/06" }}</R>`), map[string]any{"v": "04/03/2026"})
		require.ErrorIs(t, err, xmlgen.ErrHelper)
	})

	t.Run("decimal fixes the number of places", func(t *testing.T) {
		doc := render(t, `{{ decimal .a 2 }}|{{ decimal .b 2 }}|{{ decimal .c 1 }}`,
			[]byte(`{"a":1400,"b":2030.6,"c":8522}`))
		assert.Equal(t, "1400.00|2030.60|8522.0", inner(t, doc))
	})

	// decimal holds the value exactly rather than as a float64. Going through
	// a float would undo, at the last step, the exactness UseNumber preserves
	// all the way from the JSON text.
	t.Run("decimal keeps an integer past float precision exact", func(t *testing.T) {
		doc := render(t, `{{ decimal .v 2 }}`, []byte(`{"v":9007199254740993}`))
		assert.Equal(t, "9007199254740993.00", inner(t, doc),
			"2^53+1 must not shift to ...992")
	})

	t.Run("decimal keeps a very long integer exact", func(t *testing.T) {
		doc := render(t, `{{ decimal .v 0 }}`, []byte(`{"v":123456789012345678901234567890}`))
		assert.Equal(t, "123456789012345678901234567890", inner(t, doc))
	})

	t.Run("decimal rounds a half away from zero", func(t *testing.T) {
		// Commercial rounding, and rounding of the digits as written rather
		// than of their nearest binary approximation: as a float64, 2.355 is
		// really 2.35499..., which rounds the other way.
		doc := render(t, `{{ decimal .a 2 }}|{{ decimal .b 2 }}|{{ decimal .c 2 }}`,
			[]byte(`{"a":0.125,"b":2.355,"c":-0.125}`))
		assert.Equal(t, "0.13|2.36|-0.13", inner(t, doc))
	})

	t.Run("decimal does not emit negative zero", func(t *testing.T) {
		doc := render(t, `{{ decimal .v 2 }}`, []byte(`{"v":-0}`))
		assert.Equal(t, "0.00", inner(t, doc))
	})

	t.Run("decimal refuses an absurd magnitude", func(t *testing.T) {
		// Nine characters of exponent would otherwise expand to a megabyte of
		// digits — useless in a document, and a poor use of a render.
		_, err := xmlgen.Generate(context.Background(),
			[]byte(`<R>{{ decimal .v 2 }}</R>`), []byte(`{"v":1e1000000}`))
		require.ErrorIs(t, err, xmlgen.ErrHelper)
		assert.Contains(t, err.Error(), "too large")
	})

	// An absent amount must stay absent. Writing 0.00 asserts a figure the
	// data never carried, which on a declaration is a silent data change of
	// exactly the kind this package exists to prevent. date already behaves
	// this way, and the package documents that an absent key renders empty.
	t.Run("decimal leaves an absent value absent", func(t *testing.T) {
		for _, tc := range []struct{ name, data string }{
			{"a missing key", `{}`},
			{"a JSON null", `{"fob":null}`},
			{"an empty string", `{"fob":""}`},
			{"whitespace only", `{"fob":"  "}`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				out, err := xmlgen.Generate(context.Background(),
					[]byte(`<R>{{ decimal .fob 2 }}</R>`), []byte(tc.data))
				require.NoError(t, err)
				assert.Equal(t, "<R></R>", string(out), "must not become 0.00")
			})
		}
	})

	t.Run("decimal still formats a value that really is zero", func(t *testing.T) {
		// The distinction the previous case turns on: absent is not zero, but
		// zero is still zero.
		doc := render(t, `{{ decimal .fob 2 }}`, []byte(`{"fob":0}`))
		assert.Equal(t, "0.00", inner(t, doc))
	})

	t.Run("decimal refuses a non-number", func(t *testing.T) {
		_, err := xmlgen.Generate(context.Background(),
			[]byte(`<R>{{ decimal .v 2 }}</R>`), map[string]any{"v": "abc"})
		require.ErrorIs(t, err, xmlgen.ErrHelper)
	})

	t.Run("lookup substitutes a mapped value", func(t *testing.T) {
		doc := render(t, `{{ lookup .v "yes" "1" "no" "0" }}`, map[string]any{"v": "yes"})
		assert.Equal(t, "1", inner(t, doc))
	})

	t.Run("lookup passes an unmapped value through", func(t *testing.T) {
		doc := render(t, `{{ lookup .v "yes" "1" }}`, map[string]any{"v": "maybe"})
		assert.Equal(t, "maybe", inner(t, doc))
	})

	t.Run("lookup refuses an odd argument count", func(t *testing.T) {
		_, err := xmlgen.Generate(context.Background(),
			[]byte(`<R>{{ lookup .v "yes" }}</R>`), map[string]any{"v": "yes"})
		require.ErrorIs(t, err, xmlgen.ErrHelper)
	})

	t.Run("zero treats nil, empty, false and numeric zero as absent", func(t *testing.T) {
		doc := render(t, `{{ if zero .a }}A{{ end }}{{ if zero .b }}B{{ end }}{{ if zero .c }}C{{ end }}{{ if zero .d }}D{{ end }}{{ if zero .e }}E{{ end }}`,
			[]byte(`{"a":null,"b":"","c":0,"d":false,"e":"0.00"}`))
		assert.Equal(t, "ABCDE", inner(t, doc))
	})

	t.Run("zero leaves a real value alone", func(t *testing.T) {
		doc := render(t, `{{ if zero .a }}A{{ end }}{{ if zero .b }}B{{ end }}`,
			[]byte(`{"a":1,"b":"no"}`))
		assert.Equal(t, "", inner(t, doc))
	})

	t.Run("join leaves an absent list absent", func(t *testing.T) {
		// An empty string is how an absent list often arrives from a form, and
		// a missing key already joins to nothing; erroring on one but not the
		// other was inconsistent.
		for _, data := range []string{`{}`, `{"v":null}`, `{"v":""}`} {
			out, err := xmlgen.Generate(context.Background(),
				[]byte(`<R>{{ join .v "-" }}</R>`), []byte(data))
			require.NoError(t, err, "data %s", data)
			assert.Equal(t, "<R></R>", string(out))
		}
	})

	t.Run("join still refuses a non-empty scalar", func(t *testing.T) {
		_, err := xmlgen.Generate(context.Background(),
			[]byte(`<R>{{ join .v "-" }}</R>`), []byte(`{"v":"abc"}`))
		require.ErrorIs(t, err, xmlgen.ErrHelper)
	})

	t.Run("join concatenates a slice", func(t *testing.T) {
		doc := render(t, `{{ join .v "-" }}`, []byte(`{"v":["a","b","c"]}`))
		assert.Equal(t, "a-b-c", inner(t, doc))
	})

	t.Run("coalesce picks the first value present", func(t *testing.T) {
		doc := render(t, `{{ coalesce .a .b .c }}`, []byte(`{"a":null,"b":"","c":"x"}`))
		assert.Equal(t, "x", inner(t, doc))
	})

	t.Run("trim removes surrounding whitespace", func(t *testing.T) {
		doc := render(t, `[{{ trim .v }}]`, map[string]any{"v": "  x  "})
		assert.Equal(t, "[x]", inner(t, doc))
	})
}
