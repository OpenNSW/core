// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package filefields_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/OpenNSW/core/platform/filefields"
)

func parse(t *testing.T, files ...string) []filefields.Path {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"id": "x:render", "files": files})
	if err != nil {
		t.Fatal(err)
	}
	paths, err := filefields.Parse(raw)
	if err != nil {
		t.Fatalf("Parse(%q): %v", files, err)
	}
	return paths
}

func decode(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decode %s: %v", s, err)
	}
	return v
}

// mark replaces a value with "<value>!", recording what it saw.
func mark(seen *[]string) func(string) (string, error) {
	return func(s string) (string, error) {
		*seen = append(*seen, s)
		return s + "!", nil
	}
}

func TestParse(t *testing.T) {
	files := []string{
		"applicantform.invoice_file_url",
		"lab.analysis_certificates[*]",
		"userform.supportingDocuments[*].file",
		"userform.items[*].lines[*].certificate",
		"review.batches[*][*]",
		"Certificate-2.key",
	}
	paths := parse(t, files...)
	got := make([]string, len(paths))
	for i, p := range paths {
		got[i] = p.String()
	}
	if !reflect.DeepEqual(got, files) {
		t.Errorf("Parse = %q, want %q", got, files)
	}
}

func TestParseWithoutFiles(t *testing.T) {
	for _, raw := range []string{"", "null", `{}`, `{"id":"x:render","sections":{}}`, `{"files":[]}`} {
		paths, err := filefields.Parse(json.RawMessage(raw))
		if err != nil || len(paths) != 0 {
			t.Errorf("Parse(%q) = %v, %v; want none", raw, paths, err)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, raw := range []string{
		`{"files":"applicantform.invoice"}`,
		`{"files":[1]}`,
		`[]`,
	} {
		if _, err := filefields.Parse(json.RawMessage(raw)); err == nil {
			t.Errorf("Parse(%s) succeeded", raw)
		}
	}
	for _, path := range []string{
		"",
		".a",
		"a.",
		"a..b",
		"a b",
		"a.b c",
		"[*]",
		"a.[*]",
		"a[0]",
		"a[]",
		"a[*]b",
		"a.**",
		"a.$b",
	} {
		raw, _ := json.Marshal(map[string]any{"files": []string{"ok.path", path}})
		_, err := filefields.Parse(raw)
		if err == nil {
			t.Errorf("Parse accepted file field %q", path)
		} else if !strings.Contains(err.Error(), "file field") {
			t.Errorf("Parse(%q): error %q does not name the file field", path, err)
		}
	}
}

// A field declared twice would be rewritten twice, so it is refused like a
// path that does not parse.
func TestParseRejectsDuplicates(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"files": []string{"form.invoice", "form.docs[*]", "form.invoice"}})
	_, err := filefields.Parse(raw)
	if err == nil || !strings.Contains(err.Error(), `"form.invoice" is declared twice`) {
		t.Fatalf("Parse = %v, want an error naming the duplicate", err)
	}
	raw, _ = json.Marshal(map[string]any{"files": []string{"form.docs", "form.docs[*]"}})
	if _, err := filefields.Parse(raw); err != nil {
		t.Errorf("Parse(form.docs, form.docs[*]) = %v; they are different paths", err)
	}
}

func TestRebase(t *testing.T) {
	cases := []struct {
		path, prefix, want string
		ok                 bool
	}{
		{"applicantform.invoice", "applicantform", "invoice", true},
		{"applicantform.docs[*].file", "applicantform", "docs[*].file", true},
		{"applicantform.invoice", "", "applicantform.invoice", true},
		{"applicantform.invoice", "userform", "", false},
		{"applicantform", "applicantform", "", false},
		{"applicantform[*].file", "applicantform", "", false},
		{"applicantforms.invoice", "applicantform", "", false},
	}
	for _, tc := range cases {
		got, ok := parse(t, tc.path)[0].Rebase(tc.prefix)
		if ok != tc.ok || (ok && got.String() != tc.want) {
			t.Errorf("%q.Rebase(%q) = %q, %v; want %q, %v", tc.path, tc.prefix, got, ok, tc.want, tc.ok)
		}
	}
}

func TestReplace(t *testing.T) {
	data := decode(t, `{
		"applicantform": {"invoice": "k1", "note": "not a file"},
		"lab": {"certs": ["k2", "k3"]},
		"userform": {
			"docs": [{"file": "k4", "name": "a"}, {"file": "k5"}],
			"items": [{"lines": [{"cert": "k6"}, {"cert": "k7"}]}, {"lines": []}]
		},
		"review": {"batches": [["k8"], ["k9", "k10"]]}
	}`)
	var seen []string
	paths := parse(t,
		"applicantform.invoice",
		"lab.certs[*]",
		"userform.docs[*].file",
		"userform.items[*].lines[*].cert",
		"review.batches[*][*]",
	)
	if err := filefields.Replace(data, paths, mark(&seen)); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	want := decode(t, `{
		"applicantform": {"invoice": "k1!", "note": "not a file"},
		"lab": {"certs": ["k2!", "k3!"]},
		"userform": {
			"docs": [{"file": "k4!", "name": "a"}, {"file": "k5!"}],
			"items": [{"lines": [{"cert": "k6!"}, {"cert": "k7!"}]}, {"lines": []}]
		},
		"review": {"batches": [["k8!"], ["k9!", "k10!"]]}
	}`)
	if !reflect.DeepEqual(data, want) {
		t.Errorf("Replace left\n%v\nwant\n%v", data, want)
	}
	if len(seen) != 10 {
		t.Errorf("fn saw %q, want each of the ten files once", seen)
	}
}

// Where there is no file, a path is skipped: a missing value, null, or an
// empty string, at the end of the path or on the way to it.
func TestReplaceSkips(t *testing.T) {
	const doc = `{
		"empty": {"file": "", "files": ["", null]},
		"null": {"file": null, "files": null, "object": null}
	}`
	data := decode(t, doc)
	var seen []string
	paths := parse(t,
		"empty.file", "empty.files[*]",
		"null.file", "null.files[*]", "null.object.file",
		"missing.file", "empty.missing", "null.missing[*].file",
	)
	if err := filefields.Replace(data, paths, mark(&seen)); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if len(seen) != 0 {
		t.Errorf("fn saw %q, want nothing", seen)
	}
	if !reflect.DeepEqual(data, decode(t, doc)) {
		t.Errorf("Replace changed data: %v", data)
	}
}

// Any other value of another shape than the declaration says fails, naming
// where the path met it, rather than leaving a file unprotected.
func TestReplaceRefusesOtherShapes(t *testing.T) {
	const doc = `{
		"form": {
			"invoice": ["k1", "k2"],
			"single": "k3",
			"number": 3,
			"flag": true,
			"object": {"key": "k4"},
			"docs": [{"file": "k5"}, "k6"],
			"text": "k7"
		},
		"scalar": "k8"
	}`
	tests := []struct {
		path, want string
	}{
		{"form.invoice", "form.invoice holds a list, not one file; declare form.invoice[*]"},
		{"form.single[*]", "form.single holds a string, not a list; declare it without [*]"},
		{"form.number", "form.number holds a number, not a file"},
		{"form.flag", "form.flag holds a boolean, not a file"},
		{"form.object", "form.object holds an object, not a file"},
		{"form.object[*]", "form.object holds an object, not a list"},
		{"form.docs[*].file", "form.docs[*] holds a string, not an object"},
		{"form.text.file", "form.text holds a string, not an object"},
		{"scalar.file", "scalar holds a string, not an object"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			var seen []string
			err := filefields.Replace(decode(t, doc), parse(t, tt.path), mark(&seen))
			if !errors.Is(err, filefields.ErrShape) {
				t.Fatalf("err = %v, want ErrShape", err)
			}
			if !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), tt.path) {
				t.Errorf("err = %q, want it to name %q and say %q", err, tt.path, tt.want)
			}
			if tt.path != "form.docs[*].file" && len(seen) != 0 {
				t.Errorf("fn saw %q, want nothing", seen)
			}
		})
	}

	if err := filefields.Replace("k", parse(t, "form.file"), mark(new([]string))); !errors.Is(err, filefields.ErrShape) {
		t.Errorf("data that is not an object: err = %v, want ErrShape", err)
	}
	// A value that was not JSON-decoded is named by its Go type.
	err := filefields.Replace(map[string]any{"form": []string{"k"}}, parse(t, "form[*]"), mark(new([]string)))
	if !errors.Is(err, filefields.ErrShape) || !strings.Contains(err.Error(), "holds a []string") {
		t.Errorf("a Go-typed value: err = %v, want ErrShape naming []string", err)
	}
}

// A task with no data holds no files.
func TestReplaceNoData(t *testing.T) {
	var seen []string
	if err := filefields.Replace(nil, parse(t, "form.invoice"), mark(&seen)); err != nil || len(seen) != 0 {
		t.Errorf("Replace(nil) = %v, fn saw %q; want nothing", err, seen)
	}
}

// A Path that names nothing, such as the zero Path a failed Rebase returns, is
// skipped rather than walked.
func TestReplaceSkipsEmptyPath(t *testing.T) {
	data := decode(t, `{"form": {"invoice": "k1"}}`)
	var seen []string
	if err := filefields.Replace(data, []filefields.Path{{}}, mark(&seen)); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if len(seen) != 0 {
		t.Errorf("fn saw %q, want nothing", seen)
	}
}

func TestReplaceStopsOnError(t *testing.T) {
	data := decode(t, `{"form": {"docs": ["ok", "bad", "later"]}}`)
	refused := errors.New("refused")
	var seen []string
	err := filefields.Replace(data, parse(t, "form.docs[*]"), func(s string) (string, error) {
		seen = append(seen, s)
		if s == "bad" {
			return "", refused
		}
		return s + "!", nil
	})
	if !errors.Is(err, refused) {
		t.Fatalf("err = %v, want it to wrap fn's error", err)
	}
	if !strings.Contains(err.Error(), "form.docs[*]") {
		t.Errorf("err = %q, want it to name the path", err)
	}
	if !reflect.DeepEqual(seen, []string{"ok", "bad"}) {
		t.Errorf("fn saw %q, want it to stop at the error", seen)
	}
}

func TestReplaceRebased(t *testing.T) {
	payload := decode(t, `{"invoice": "k1", "other": "k2"}`)
	var seen []string
	var rebased []filefields.Path
	for _, p := range parse(t, "applicantform.invoice", "userform.other") {
		if r, ok := p.Rebase("applicantform"); ok {
			rebased = append(rebased, r)
		}
	}
	if err := filefields.Replace(payload, rebased, mark(&seen)); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if !reflect.DeepEqual(seen, []string{"k1"}) {
		t.Errorf("fn saw %q, want only the step's own field", seen)
	}
}
