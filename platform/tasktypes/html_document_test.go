// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package tasktypes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/htmlgen"
	corestorage "github.com/OpenNSW/core/storage"
	"github.com/OpenNSW/core/taskflow/plugins"
	"github.com/OpenNSW/core/taskflow/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTemplates serves templates from a map.
type fakeTemplates map[string]string

func (f fakeTemplates) HTMLTemplate(_ context.Context, id string) ([]byte, error) {
	src, ok := f[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return []byte(src), nil
}

// fakeStaticData serves static data titles by "id@manifestVersion", then
// const; an artifact a manifest version lacks is not found, and the "down"
// artifact fails to read.
type fakeStaticData map[string]map[string]string

var errStaticDataDown = errors.New("artifact store unavailable")

func (f fakeStaticData) Title(_ context.Context, manifestVersion int, id, value string) (string, bool, error) {
	if id == "down" {
		return "", false, errStaticDataDown
	}
	titles, ok := f[fmt.Sprintf("%s@%d", id, manifestVersion)]
	if !ok {
		return "", false, fmt.Errorf("static data %q in manifest version %d: %w", id, manifestVersion, artifact.ErrNotFound)
	}
	title, found := titles[value]
	return title, found, nil
}

// fakeSaver records what it was asked to store.
type fakeSaver struct {
	err      error
	calls    int
	filename string
	mime     string
	content  []byte
	size     int64
}

func (f *fakeSaver) Save(_ context.Context, filename, mime string, body io.Reader, size int64) (*corestorage.FileMetadata, error) {
	f.calls++
	content, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	f.filename, f.mime, f.content, f.size = filename, mime, content, size
	if f.err != nil {
		return nil, f.err
	}
	// A new key per call, as storage allocates one, so a test can tell a
	// reused document from a newly stored one. The size is recorded as
	// given, as core/storage's Save records it.
	key := fmt.Sprintf("0f8e7c1a-0000-4000-8000-%012d.html", f.calls)
	return &corestorage.FileMetadata{Key: key, Name: filename, MimeType: mime, Size: size}, nil
}

const permitTemplate = `<p>Permit No: {{ .refid.reference_id }}</p>` +
	`<p>Applicant: {{ .application.applicant_name }}</p>` +
	`<p>Fee: {{ decimal .application.fee_amount 2 }}</p>`

func htmlDocCtx(inputs map[string]any) plugins.PluginContext {
	return plugins.PluginContext{
		Context:         context.Background(),
		Inputs:          inputs,
		Record:          &store.TaskRecord{TaskID: "task-1", ManifestVersion: 2, Data: map[string]any{}},
		OutputNamespace: "permit_doc",
	}
}

func TestHTMLDocumentGenerator_RendersStoresAndRecordsKey(t *testing.T) {
	files := &fakeSaver{}
	p := NewHTMLDocumentGeneratorPlugin(fakeTemplates{"permit": permitTemplate}, files, fakeStaticData{})
	ctx := htmlDocCtx(map[string]any{
		"refid":       map[string]any{"reference_id": "PRM-2026-00555"},
		"application": map[string]any{"applicant_name": "Smith & Co", "fee_amount": json.Number("1400")},
	})

	require.NoError(t, p.Execute(ctx, json.RawMessage(`{"template_id": "permit", "filename": "permit.html"}`)))

	assert.Equal(t, `<p>Permit No: PRM-2026-00555</p><p>Applicant: Smith &amp; Co</p><p>Fee: 1400.00</p>`, string(files.content))
	assert.Equal(t, "permit.html", files.filename)
	assert.Equal(t, "text/html; charset=utf-8", files.mime)
	assert.Equal(t, int64(len(files.content)), files.size, "the size given is the document's length")
	assert.Equal(t, map[string]any{
		"key":       "0f8e7c1a-0000-4000-8000-000000000001.html",
		"name":      "permit.html",
		"mime_type": "text/html; charset=utf-8",
		"size":      int64(len(files.content)),
	}, ctx.Record.Data["permit_doc"])
}

const permitProps = `{"template_id": "permit"}`

func permitInputs(applicant string) map[string]any {
	return map[string]any{
		"refid":       map[string]any{"reference_id": "PRM-2026-00555"},
		"application": map[string]any{"applicant_name": applicant, "fee_amount": json.Number("1400")},
	}
}

func TestHTMLDocumentGenerator_ChangedInputsStoreNewDocument(t *testing.T) {
	files := &fakeSaver{}
	p := NewHTMLDocumentGeneratorPlugin(fakeTemplates{"permit": permitTemplate}, files, fakeStaticData{})
	ctx := htmlDocCtx(permitInputs("Smith & Co"))

	require.NoError(t, p.Execute(ctx, json.RawMessage(permitProps)))
	first := ctx.Record.Data["permit_doc"].(map[string]any)

	// A workflow loop comes back to the step after the applicant corrected the form.
	ctx.Inputs = permitInputs("Smith & Sons")
	require.NoError(t, p.Execute(ctx, json.RawMessage(permitProps)))
	second := ctx.Record.Data["permit_doc"].(map[string]any)

	assert.Equal(t, 2, files.calls)
	assert.Contains(t, string(files.content), "Smith &amp; Sons")
	assert.NotEqual(t, first["key"], second["key"], "the task must point at the document for the new inputs")
}

func TestHTMLDocumentGenerator_EveryRunStores(t *testing.T) {
	files := &fakeSaver{}
	p := NewHTMLDocumentGeneratorPlugin(fakeTemplates{"permit": permitTemplate}, files, fakeStaticData{})
	ctx := htmlDocCtx(permitInputs("Smith & Co"))

	require.NoError(t, p.Execute(ctx, json.RawMessage(permitProps)))
	require.NoError(t, p.Execute(ctx, json.RawMessage(permitProps)))

	assert.Equal(t, 2, files.calls, "a document already recorded for the task is not reused")
	assert.Equal(t, "0f8e7c1a-0000-4000-8000-000000000002.html", ctx.Record.Data["permit_doc"].(map[string]any)["key"])
}

func TestHTMLDocumentGenerator_Filename(t *testing.T) {
	cases := map[string]struct{ props, want string }{
		"defaults to the template id": {`{"template_id": "permit"}`, "permit.html"},
		"adds a missing extension":    {`{"template_id": "permit", "filename": "PRM-001"}`, "PRM-001.html"},
		"keeps an .html extension":    {`{"template_id": "permit", "filename": "PRM-001.HTML"}`, "PRM-001.HTML"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			files := &fakeSaver{}
			p := NewHTMLDocumentGeneratorPlugin(fakeTemplates{"permit": `<p>x</p>`}, files, fakeStaticData{})
			require.NoError(t, p.Execute(htmlDocCtx(nil), json.RawMessage(tc.props)))
			assert.Equal(t, tc.want, files.filename)
		})
	}
}

func TestHTMLDocumentGenerator_Failures(t *testing.T) {
	storeErr := errors.New("bucket unavailable")
	cases := map[string]struct {
		props     string
		templates fakeTemplates
		saveErr   error
		namespace string
		wantIs    error
	}{
		"missing template_id":  {props: `{}`},
		"invalid config":       {props: `[]`},
		"unknown template":     {props: `{"template_id": "nope"}`},
		"missing namespace":    {props: `{"template_id": "permit"}`, namespace: "-"},
		"broken template":      {props: `{"template_id": "permit"}`, templates: fakeTemplates{"permit": `<p>{{ .a </p>`}, wantIs: htmlgen.ErrParseTemplate},
		"unsafe template":      {props: `{"template_id": "permit"}`, templates: fakeTemplates{"permit": `<a href="{{ .u }}`}, wantIs: htmlgen.ErrUnsafeTemplate},
		"render failure":       {props: `{"template_id": "permit"}`, templates: fakeTemplates{"permit": `{{ .application }}`}, wantIs: htmlgen.ErrUnsupportedValue},
		"unsafe value refused": {props: `{"template_id": "permit"}`, templates: fakeTemplates{"permit": `<a href="{{ .link }}">x</a>`}, wantIs: htmlgen.ErrUnsafeValue},
		"storage failure":      {props: `{"template_id": "permit"}`, saveErr: storeErr, wantIs: storeErr},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			templates := tc.templates
			if templates == nil {
				templates = fakeTemplates{"permit": `<p>x</p>`}
			}
			files := &fakeSaver{err: tc.saveErr}
			ctx := htmlDocCtx(map[string]any{"application": map[string]any{"a": "b"}, "link": "javascript:alert(1)"})
			if tc.namespace == "-" {
				ctx.OutputNamespace = ""
			}

			err := NewHTMLDocumentGeneratorPlugin(templates, files, fakeStaticData{}).Execute(ctx, json.RawMessage(tc.props))

			require.Error(t, err)
			if tc.wantIs != nil {
				assert.ErrorIs(t, err, tc.wantIs)
			}
			assert.NotContains(t, ctx.Record.Data, "permit_doc", "nothing is recorded for a document that was not stored")
		})
	}
}

// portStaticData lists ports in the manifest version htmlDocCtx's task runs on.
var portStaticData = fakeStaticData{"ports@2": {"LKCMB": "COLOMBO", "NLRTM": "ROTTERDAM", "123": "NUMERIC"}}

func TestHTMLDocumentGenerator_StaticDataNamesACode(t *testing.T) {
	cases := map[string]struct {
		inputs map[string]any
		want   string
	}{
		"a known code prints its title":   {map[string]any{"port": "NLRTM"}, "<p>ROTTERDAM</p>"},
		"an unknown code prints as it is": {map[string]any{"port": "XXABC"}, "<p>XXABC</p>"},
		"an empty value prints nothing":   {map[string]any{"port": ""}, "<p></p>"},
		"a missing value prints nothing":  {map[string]any{}, "<p></p>"},
		"a JSON null prints nothing":      {map[string]any{"port": nil}, "<p></p>"},
		"a number prints its title":       {map[string]any{"port": 123}, "<p>NUMERIC</p>"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			files := &fakeSaver{}
			p := NewHTMLDocumentGeneratorPlugin(fakeTemplates{"doc": `<p>{{ staticdata "ports" .port }}</p>`}, files, portStaticData)

			require.NoError(t, p.Execute(htmlDocCtx(tc.inputs), json.RawMessage(`{"template_id": "doc"}`)))

			assert.Equal(t, tc.want, string(files.content))
		})
	}
}

// The static data comes from the manifest version the task started on.
func TestHTMLDocumentGenerator_StaticDataOfTheTasksManifestVersion(t *testing.T) {
	staticData := fakeStaticData{
		"ports@1": {"LKCMB": "COLOMBO"},
		"ports@2": {"LKCMB": "PORT OF COLOMBO"},
	}
	for version, want := range map[int]string{1: "<p>COLOMBO</p>", 2: "<p>PORT OF COLOMBO</p>"} {
		files := &fakeSaver{}
		p := NewHTMLDocumentGeneratorPlugin(fakeTemplates{"doc": `<p>{{ staticdata "ports" .port }}</p>`}, files, staticData)
		ctx := htmlDocCtx(map[string]any{"port": "LKCMB"})
		ctx.Record.ManifestVersion = version

		require.NoError(t, p.Execute(ctx, json.RawMessage(`{"template_id": "doc"}`)))

		assert.Equal(t, want, string(files.content), "manifest version %d", version)
	}
}

// A template that names a version between the id and the value, as templates
// in published manifests do, still renders: the version is ignored, and the
// static data comes from the task's manifest version.
func TestHTMLDocumentGenerator_StaticDataVersionArgumentIsIgnored(t *testing.T) {
	for _, tmpl := range []string{
		`<p>{{ staticdata "ports" "1" .port }}</p>`,
		`<p>{{ staticdata "ports" "9" .port }}</p>`,
		`<p>{{ staticdata "ports" "" .port }}</p>`,
	} {
		files := &fakeSaver{}
		p := NewHTMLDocumentGeneratorPlugin(fakeTemplates{"doc": tmpl}, files, portStaticData)

		require.NoError(t, p.Execute(htmlDocCtx(map[string]any{"port": "NLRTM"}), json.RawMessage(`{"template_id": "doc"}`)), tmpl)

		assert.Equal(t, "<p>ROTTERDAM</p>", string(files.content), tmpl)
	}
}

// A title is escaped like any other value.
func TestHTMLDocumentGenerator_StaticDataTitleIsEscaped(t *testing.T) {
	files := &fakeSaver{}
	staticData := fakeStaticData{"ports@2": {"X": "<b>A & B</b>"}}
	p := NewHTMLDocumentGeneratorPlugin(fakeTemplates{"doc": `<p>{{ staticdata "ports" .port }}</p>`}, files, staticData)

	require.NoError(t, p.Execute(htmlDocCtx(map[string]any{"port": "X"}), json.RawMessage(`{"template_id": "doc"}`)))

	assert.Equal(t, `<p>&lt;b&gt;A &amp; B&lt;/b&gt;</p>`, string(files.content))
}

func TestHTMLDocumentGenerator_StaticDataFailures(t *testing.T) {
	cases := map[string]struct {
		template  string
		inputs    map[string]any
		permanent bool
		wantIs    error
	}{
		"unknown static data":               {`{{ staticdata "nope" .port }}`, map[string]any{"port": "LKCMB"}, true, artifact.ErrNotFound},
		"unknown static data, with version": {`{{ staticdata "nope" "1" .port }}`, map[string]any{"port": "LKCMB"}, true, artifact.ErrNotFound},
		"too few arguments":                 {`{{ staticdata .port }}`, map[string]any{"port": "LKCMB"}, true, errStaticDataCall},
		"too many arguments":                {`{{ staticdata "ports" "1" "x" .port }}`, map[string]any{"port": "LKCMB"}, true, errStaticDataCall},
		"an empty id":                       {`{{ staticdata "" .port }}`, map[string]any{"port": "LKCMB"}, true, errStaticDataCall},
		"an id that isn't text":             {`{{ staticdata 5 .port }}`, map[string]any{"port": "LKCMB"}, true, errStaticDataCall},
		"a list as the value":               {`{{ staticdata "ports" .port }}`, map[string]any{"port": []any{"LKCMB"}}, true, errStaticDataCall},
		"a bool as the value":               {`{{ staticdata "ports" .port }}`, map[string]any{"port": true}, true, errStaticDataCall},
		"a bool as the value, with version": {`{{ staticdata "ports" "1" .port }}`, map[string]any{"port": true}, true, errStaticDataCall},
		"static data unreadable":            {`{{ staticdata "down" .port }}`, map[string]any{"port": "LKCMB"}, false, errStaticDataDown},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			files := &fakeSaver{}
			p := NewHTMLDocumentGeneratorPlugin(fakeTemplates{"doc": tc.template}, files, portStaticData)
			ctx := htmlDocCtx(tc.inputs)

			err := p.Execute(ctx, json.RawMessage(`{"template_id": "doc"}`))

			require.Error(t, err)
			assert.ErrorIs(t, err, tc.wantIs)
			assert.Equal(t, tc.permanent, plugins.IsPermanent(err), "permanent")
			assert.Equal(t, 0, files.calls, "nothing is stored")
			assert.NotContains(t, ctx.Record.Data, "permit_doc")
		})
	}
}
