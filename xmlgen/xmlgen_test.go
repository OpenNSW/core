// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package xmlgen_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/OpenNSW/core/xmlgen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerate_DataShapes(t *testing.T) {
	const tmpl = `<R>{{ .name }}</R>`

	t.Run("JSON bytes and json.RawMessage both decode", func(t *testing.T) {
		raw := []byte(`{"name":"Acme"}`)
		a, err := xmlgen.Generate(context.Background(), []byte(tmpl), raw)
		require.NoError(t, err)
		b, err := xmlgen.Generate(context.Background(), []byte(tmpl), json.RawMessage(raw))
		require.NoError(t, err)
		assert.Equal(t, string(a), string(b))
		assert.Equal(t, "<R>Acme</R>", string(a))
	})

	t.Run("malformed JSON is reported as bad data", func(t *testing.T) {
		_, err := xmlgen.Generate(context.Background(), []byte(tmpl), []byte(`{"name":`))
		require.ErrorIs(t, err, xmlgen.ErrInvalidData)
	})

	t.Run("a map passes through", func(t *testing.T) {
		out, err := xmlgen.Generate(context.Background(), []byte(tmpl), map[string]any{"name": "Acme"})
		require.NoError(t, err)
		assert.Equal(t, "<R>Acme</R>", string(out))
	})

	t.Run("a struct is addressed by Go field name", func(t *testing.T) {
		data := struct {
			Name string `json:"name"`
		}{Name: "Acme"}

		out, err := xmlgen.Generate(context.Background(), []byte(`<R>{{ .Name }}</R>`), data)
		require.NoError(t, err)
		assert.Equal(t, "<R>Acme</R>", string(out))

		// The json tag is not consulted: text/template walks Go fields.
		_, err = xmlgen.Generate(context.Background(), []byte(tmpl), data)
		require.Error(t, err)
	})

	t.Run("nil data renders a template that needs none", func(t *testing.T) {
		out, err := xmlgen.Generate(context.Background(), []byte(`<R><A/></R>`), nil)
		require.NoError(t, err)
		assert.Equal(t, "<R><A/></R>", string(out))
	})

	t.Run("a top-level array can be ranged over", func(t *testing.T) {
		out, err := xmlgen.Generate(context.Background(),
			[]byte(`<R>{{ range . }}<E>{{ .k }}</E>{{ end }}</R>`), []byte(`[{"k":"a"},{"k":"b"}]`))
		require.NoError(t, err)
		assert.Equal(t, "<R><E>a</E><E>b</E></R>", string(out))
	})
}

func TestGenerate_MissingKeys(t *testing.T) {
	t.Run("an absent key renders empty by default", func(t *testing.T) {
		out, err := xmlgen.Generate(context.Background(), []byte(`<R>{{ .nope }}</R>`), map[string]any{})
		require.NoError(t, err)
		assert.Equal(t, "<R></R>", string(out))
	})

	t.Run("an absent key is falsy in a condition", func(t *testing.T) {
		out, err := xmlgen.Generate(context.Background(),
			[]byte(`<R>{{ if .nope }}yes{{ else }}no{{ end }}</R>`), map[string]any{})
		require.NoError(t, err)
		assert.Equal(t, "<R>no</R>", string(out))
	})

	t.Run("strict keys turn an absent key into an error naming it", func(t *testing.T) {
		_, err := xmlgen.Generate(context.Background(),
			[]byte(`<R><A/>`+"\n"+`<B>{{ .consignee }}</B></R>`), map[string]any{}, xmlgen.WithStrictKeys())
		require.ErrorIs(t, err, xmlgen.ErrMissingKey)
		assert.Contains(t, err.Error(), "consignee")
	})

	t.Run("strict keys leave a present null alone", func(t *testing.T) {
		out, err := xmlgen.Generate(context.Background(),
			[]byte(`<R>{{ .n }}</R>`), []byte(`{"n":null}`), xmlgen.WithStrictKeys())
		require.NoError(t, err)
		assert.Equal(t, "<R></R>", string(out))
	})

	t.Run("an optional field is guarded with index under strict keys", func(t *testing.T) {
		out, err := xmlgen.Generate(context.Background(),
			[]byte(`<R>{{ if index . "middle" }}{{ index . "middle" }}{{ end }}</R>`),
			map[string]any{}, xmlgen.WithStrictKeys())
		require.NoError(t, err)
		assert.Equal(t, "<R></R>", string(out))
	})
}

func TestGenerate_Iteration(t *testing.T) {
	t.Run("an empty array yields no children but keeps the parent", func(t *testing.T) {
		out, err := xmlgen.Generate(context.Background(),
			[]byte(`<R><Lines>{{ range .lines }}<L/>{{ end }}</Lines></R>`), []byte(`{"lines":[]}`))
		require.NoError(t, err)
		assert.Equal(t, "<R><Lines></Lines></R>", string(out))
	})

	t.Run("an empty object is falsy in with", func(t *testing.T) {
		out, err := xmlgen.Generate(context.Background(),
			[]byte(`<R>{{ with .o }}full{{ else }}empty{{ end }}</R>`), []byte(`{"o":{}}`))
		require.NoError(t, err)
		assert.Equal(t, "<R>empty</R>", string(out))
	})

	t.Run("ranging a map is ordered by key and stable", func(t *testing.T) {
		const tmpl = `<R>{{ range $k, $v := .m }}<E>{{ $k }}</E>{{ end }}</R>`
		data := []byte(`{"m":{"c":1,"a":2,"b":3}}`)

		first, err := xmlgen.Generate(context.Background(), []byte(tmpl), data)
		require.NoError(t, err)
		assert.Equal(t, "<R><E>a</E><E>b</E><E>c</E></R>", string(first))

		for range 20 {
			again, err := xmlgen.Generate(context.Background(), []byte(tmpl), data)
			require.NoError(t, err)
			require.Equal(t, string(first), string(again))
		}
	})

	t.Run("whitespace trimming is preserved byte for byte", func(t *testing.T) {
		out, err := xmlgen.Generate(context.Background(),
			[]byte("<R>\n  {{- range .l }}\n  <L>{{ . }}</L>\n  {{- end }}\n</R>"), []byte(`{"l":["a","b"]}`))
		require.NoError(t, err)
		assert.Equal(t, "<R>\n  <L>a</L>\n  <L>b</L>\n</R>", string(out))
	})
}

func TestGenerateTo(t *testing.T) {
	t.Run("agrees with Generate byte for byte", func(t *testing.T) {
		const tmpl = `<R>{{ .v }}</R>`
		data := map[string]any{"v": "x&y"}

		want, err := xmlgen.Generate(context.Background(), []byte(tmpl), data)
		require.NoError(t, err)

		var got strings.Builder
		require.NoError(t, xmlgen.GenerateTo(context.Background(), &got, []byte(tmpl), data))
		assert.Equal(t, string(want), got.String())
	})

	// The reason GenerateTo exists: a caller streaming to an http.ResponseWriter
	// must not be able to emit half a document after a 200.
	t.Run("writes nothing at all when the document is invalid", func(t *testing.T) {
		var w countingWriter
		err := xmlgen.GenerateTo(context.Background(), &w, []byte(`<R><A></R>`), nil)
		require.ErrorIs(t, err, xmlgen.ErrMalformedXML)
		assert.Zero(t, w.n, "a failed render must write no bytes")
	})

	t.Run("a writer error is returned", func(t *testing.T) {
		sentinel := errors.New("disk full")
		err := xmlgen.GenerateTo(context.Background(), failingWriter{sentinel}, []byte(`<R/>`), nil)
		require.ErrorIs(t, err, sentinel)
	})
}

func TestGenerate_Limits(t *testing.T) {
	t.Run("a runaway render is stopped by the size cap", func(t *testing.T) {
		rows := make([]any, 5000)
		for i := range rows {
			rows[i] = map[string]any{"v": strings.Repeat("x", 100)}
		}
		_, err := xmlgen.Generate(context.Background(),
			[]byte(`<R>{{ range .rows }}<E>{{ .v }}</E>{{ end }}</R>`),
			map[string]any{"rows": rows}, xmlgen.WithMaxOutputBytes(1024))
		require.ErrorIs(t, err, xmlgen.ErrOutputTooLarge)
	})

	t.Run("a cancelled context stops the render", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := xmlgen.Generate(ctx, []byte(`<R>{{ .v }}</R>`), map[string]any{"v": "x"})
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestValidate(t *testing.T) {
	t.Run("accepts a template that only uses built-ins", func(t *testing.T) {
		require.NoError(t, xmlgen.Validate([]byte(`<R>{{ date .d "2006-01-02" "1/2/06" }}</R>`)))
	})

	t.Run("reports a syntax error", func(t *testing.T) {
		require.ErrorIs(t, xmlgen.Validate([]byte(`<R>{{ if }}</R>`)), xmlgen.ErrParseTemplate)
	})

	t.Run("reports a function the template is not allowed to call", func(t *testing.T) {
		require.ErrorIs(t, xmlgen.Validate([]byte(`<R>{{ codelist .x }}</R>`)), xmlgen.ErrParseTemplate)
		require.NoError(t, xmlgen.Validate([]byte(`<R>{{ codelist .x }}</R>`), "codelist"))
	})

	t.Run("needs no data", func(t *testing.T) {
		require.NoError(t, xmlgen.Validate([]byte(`<R>{{ .anything.at.all }}</R>`)))
	})
}

func TestGenerate_Concurrent(t *testing.T) {
	const tmpl = `<R><V>{{ .v }}</V><L>{{ lookup .v "a" "1" }}</L></R>`

	var wg sync.WaitGroup
	for i := range 200 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v := string(rune('a' + i%26))
			out, err := xmlgen.Generate(context.Background(), []byte(tmpl), map[string]any{"v": v},
				withResolver("noop", func(context.Context, ...any) (any, error) { return nil, nil }))
			assert.NoError(t, err)
			assert.Contains(t, string(out), "<V>"+v+"</V>")
		}()
	}
	wg.Wait()
}

type countingWriter struct{ n int }

func (w *countingWriter) Write(p []byte) (int, error) { w.n += len(p); return len(p), nil }

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }
