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
