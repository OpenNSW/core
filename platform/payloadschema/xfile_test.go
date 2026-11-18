package payloadschema

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/OpenNSW/core/taskflow/store"
)

// filesForm has a file field for each way x-file is written, one without it,
// one in a list's rows, and one in a conditional part. Its values are stored
// file values, which end with the file's extension.
const filesForm = `{"schema": {
	"type": "object",
	"properties": {
		"invoice": {"type": "string", "format": "file", "x-file": {"accept": ".pdf"}},
		"photos": {"type": "array", "items": {"type": "string", "format": "file"}, "x-file": {"accept": ".png,.jpg", "maxFiles": 2}},
		"typed": {"type": "string", "format": "file", "x-file": {"accept": "image/*,application/pdf"}},
		"anything": {"type": "string", "format": "file", "x-file": {"accept": "*/*"}},
		"unlimited": {"type": "string", "format": "file"},
		"rows": {"type": "array", "items": {"type": "object", "properties": {
			"document": {"type": "string", "format": "file", "x-file": {"accept": ".pdf"}}
		}}},
		"kind": {"type": "string"}
	},
	"if": {"properties": {"kind": {"const": "export"}}, "required": ["kind"]},
	"then": {"properties": {"permit": {"type": "string", "format": "file", "x-file": {"accept": ".pdf"}}}}
}}`

func TestExtension_Execute_XFile(t *testing.T) {
	notAcceptedPDF := `file type not accepted (accept: ".pdf")`
	tests := []struct {
		name    string
		draft   bool
		payload string
		want    []Violation
	}{
		{name: "accepted files", payload: `{"invoice": "invoice.pdf", "photos": ["photo.png"], "anything": "sheet.xlsx", "rows": [{"document": "invoice.pdf"}]}`},
		{name: "an extension in another case", payload: `{"invoice": "scan.PDF"}`},
		{name: "an extension not accepted", payload: `{"invoice": "sheet.xlsx"}`,
			want: []Violation{{"/invoice", notAcceptedPDF}}},
		{name: "a value without an extension", payload: `{"invoice": "invoice"}`,
			want: []Violation{{"/invoice", notAcceptedPDF}}},
		{name: "a list's file not accepted", payload: `{"photos": ["photo.png", "invoice.pdf"]}`,
			want: []Violation{{"/photos/1", `file type not accepted (accept: ".png,.jpg")`}}},
		{name: "type entries match nothing", payload: `{"typed": "photo.png"}`,
			want: []Violation{{"/typed", `file type not accepted (accept: "image/*,application/pdf")`}}},
		{name: "more files than allowed", payload: `{"photos": ["photo.png", "photo.png", "photo.png"]}`,
			want: []Violation{{"/photos", "more than 2 files"}}},
		{name: "an empty value names no file", payload: `{"invoice": "", "photos": [""]}`,
			want: []Violation{{"/invoice", "empty value instead of a file"}, {"/photos/0", "empty value instead of a file"}}},
		{name: "a field without x-file is not checked", payload: `{"unlimited": ""}`},
		{name: "a file in a list's row", payload: `{"rows": [{"document": "invoice.pdf"}, {"document": "photo.png"}]}`,
			want: []Violation{{"/rows/1/document", notAcceptedPDF}}},
		{name: "a conditional part that applies", payload: `{"kind": "export", "permit": "photo.png"}`,
			want: []Violation{{"/permit", notAcceptedPDF}}},
		{name: "a conditional part that doesn't apply", payload: `{"kind": "import", "permit": "photo.png"}`},
		{name: "a draft is not checked", draft: true, payload: `{"invoice": "sheet.xlsx", "photos": [""]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.draft {
				ctx = WithDraft(ctx)
			}
			ext := NewExtension(fakeForms{"files-form": filesForm}.form, namespace("form"))
			err := ext.Execute(ctx, &store.TaskRecord{}, decode(t, tt.payload), naming("files-form"))

			if tt.want == nil {
				if err != nil {
					t.Fatalf("Execute() = %v; want nil", err)
				}
				return
			}
			var invalid *InvalidPayloadError
			if !errors.As(err, &invalid) {
				t.Fatalf("Execute() = %v; want an *InvalidPayloadError", err)
			}
			if !sameViolations(invalid.Violations, tt.want) {
				t.Errorf("violations = %v, want %v", invalid.Violations, tt.want)
			}
		})
	}
}

// readOnlyFilesForm shows files the user can't change: a read-only field, a
// field in a read-only object, and one read-only through the definition it
// refers to; beside an editable field under the same rule.
const readOnlyFilesForm = `{"schema": {
	"type": "object",
	"definitions": {"serverOwned": {"readOnly": true}},
	"properties": {
		"reports": {"type": "array", "items": {"type": "string", "format": "file"}, "readOnly": true, "x-file": {"accept": ".pdf", "maxFiles": 1}},
		"application": {"type": "object", "readOnly": true, "properties": {
			"permit": {"type": "string", "format": "file", "x-file": {"accept": ".pdf"}}
		}},
		"certificate": {"type": "string", "format": "file", "allOf": [{"$ref": "#/definitions/serverOwned"}], "x-file": {"accept": ".pdf"}},
		"letter": {"type": "string", "format": "file", "x-file": {"accept": ".pdf"}}
	}
}}`

// Values the read-only check holds are not judged by x-file, which still
// applies to the values the user sets.
func TestExtension_Execute_XFileSkipsReadOnly(t *testing.T) {
	const stored = `{"reports": ["report.png", "report.jpg"], "application": {"permit": "permit.jpg"}, "certificate": "certificate.png"}`
	record := &store.TaskRecord{Data: map[string]any{"form": decode(t, stored)}}
	ext := NewExtension(fakeForms{"files-form": readOnlyFilesForm}.form, namespace("form"))

	payload := decode(t, stored)
	payload["letter"] = "letter.pdf"
	if err := ext.Execute(context.Background(), record, payload, naming("files-form")); err != nil {
		t.Fatalf("Execute() = %v; want nil", err)
	}

	payload["letter"] = "letter.png"
	err := ext.Execute(context.Background(), record, payload, naming("files-form"))
	var invalid *InvalidPayloadError
	if !errors.As(err, &invalid) {
		t.Fatalf("Execute() = %v; want an *InvalidPayloadError", err)
	}
	if want := []Violation{{"/letter", `file type not accepted (accept: ".pdf")`}}; !sameViolations(invalid.Violations, want) {
		t.Errorf("violations = %v, want %v", invalid.Violations, want)
	}
}

// Rules of the wrong type mean what they mean to the file control, and x-file
// on a field that isn't a file field is not read.
func TestExtension_Execute_XFileRulesOfTheWrongType(t *testing.T) {
	const form = `{"schema": {"type": "object", "properties": {
		"count_as_text": {"type": "array", "items": {"type": "string", "format": "file"}, "x-file": {"maxFiles": " 1 "}},
		"count_not_a_number": {"type": "array", "items": {"type": "string", "format": "file"}, "x-file": {"maxFiles": "ten"}},
		"accept_as_list": {"type": "string", "format": "file", "x-file": {"accept": [".pdf"]}},
		"note": {"type": "string", "x-file": {"accept": ".pdf"}}
	}}}`
	ext := NewExtension(fakeForms{"form": form}.form, namespace("form"))
	payload := `{"count_as_text": ["photo.png", "photo.png"], "count_not_a_number": ["photo.png", "photo.png"], "accept_as_list": "invoice.pdf", "note": "photo.png"}`
	err := ext.Execute(context.Background(), &store.TaskRecord{}, decode(t, payload), naming("form"))

	want := []Violation{
		{"/accept_as_list", "file type not accepted (accept is not text)"},
		{"/count_as_text", "more than 1 file"},
	}
	var invalid *InvalidPayloadError
	if !errors.As(err, &invalid) {
		t.Fatalf("Execute() = %v; want an *InvalidPayloadError", err)
	}
	if !sameViolations(invalid.Violations, want) {
		t.Errorf("violations = %v, want %v", invalid.Violations, want)
	}
}

// sameViolations reports whether got and want hold the same violations, in any
// order: a form's properties are checked in no set order.
func sameViolations(got, want []Violation) bool {
	byPath := func(a, b Violation) int { return strings.Compare(a.Path+a.Message, b.Path+b.Message) }
	got, want = slices.Clone(got), slices.Clone(want)
	slices.SortFunc(got, byPath)
	slices.SortFunc(want, byPath)
	return slices.Equal(got, want)
}

func TestMaxFilesOf(t *testing.T) {
	tests := []struct {
		maxFiles any
		want     *int
	}{
		{nil, nil},
		{json.Number("10"), ptr(10)},
		{json.Number("2.5"), ptr(3)},
		{json.Number("-1"), ptr(0)},
		{"4", ptr(4)},
		{"", ptr(0)},
		{"ten", nil},
		{"Infinity", nil},
		{"NaN", nil},
		{"-Infinity", ptr(0)},
		{true, nil},
	}
	for _, tt := range tests {
		got := maxFilesOf(tt.maxFiles)
		if (got == nil) != (tt.want == nil) || (got != nil && *got != *tt.want) {
			t.Errorf("maxFilesOf(%#v) = %v, want %v", tt.maxFiles, deref(got), deref(tt.want))
		}
	}
}

func ptr(n int) *int { return &n }

func deref(n *int) any {
	if n == nil {
		return nil
	}
	return *n
}

func TestAccepts(t *testing.T) {
	tests := []struct {
		accept, value string
		want          bool
	}{
		{"*", "invoice.pdf", true},
		{"*/*", "invoice", true},
		{".pdf", "invoice.pdf", true},
		{".PDF", "invoice.pdf", true},
		{".pdf", "invoice.PDF", true},
		{".png", "invoice.pdf", false},
		{".pdf", "invoice.pdf.exe", false},
		{".pdf", "invoice", false},
		{" .png , .pdf ", "invoice.pdf", true},
		{"application/pdf", "invoice.pdf", false},
		{"image/*", "photo.png", false},
		{"", "invoice.pdf", false},
		{"pdf", "invoice.pdf", false},
	}
	for _, tt := range tests {
		if got := accepts(tt.accept, tt.value); got != tt.want {
			t.Errorf("accepts(%q, %q) = %v, want %v", tt.accept, tt.value, got, tt.want)
		}
	}
}
