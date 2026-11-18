package payloadschema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/OpenNSW/core/taskflow/store"
)

// reviewForm is a form template as the artifacts store it.
const reviewForm = `{
	"id": "review-form",
	"title": "Review",
	"schema": {
		"type": "object",
		"properties": {
			"outcome": {"type": "string", "oneOf": [{"const": "approve"}, {"const": "needs_more_info"}]},
			"reason": {"type": "string"}
		},
		"required": ["outcome"],
		"additionalProperties": false
	},
	"uiSchema": {"type": "VerticalLayout", "elements": []}
}`

// fakeForms serves forms from a map, as the artifact registry would.
type fakeForms map[string]string

func (f fakeForms) form(_ context.Context, id string) ([]byte, error) {
	form, ok := f[id]
	if !ok {
		return nil, fmt.Errorf("form %q not found", id)
	}
	return []byte(form), nil
}

// namespace returns a NamespaceOf giving every step template the output
// namespace ns.
func namespace(ns string) NamespaceOf {
	return func(context.Context, string) (string, error) { return ns, nil }
}

// naming returns the extension's properties naming form formID.
func naming(formID string) json.RawMessage {
	return json.RawMessage(`{"form": "` + formID + `"}`)
}

func TestExtension_Execute(t *testing.T) {
	ext := NewExtension(fakeForms{
		"review-form":     reviewForm,
		"schemaless-form": `{"id": "schemaless-form", "uiSchema": {}}`,
		"bad-schema-form": `{"id": "bad-schema-form", "schema": {"type": 5}}`,
	}.form, namespace("reviewform"))

	tests := []struct {
		name    string
		props   json.RawMessage
		draft   bool
		payload string
		// wantInvalid: the submission is rejected as not matching its form.
		// wantErr: it is rejected for another reason (misconfiguration).
		wantInvalid bool
		wantErr     bool
	}{
		{name: "valid submission", props: naming("review-form"), payload: `{"outcome": "approve"}`},
		{name: "system keys are not form data", props: naming("review-form"), payload: `{"outcome": "approve", "__command": "submit"}`},
		{name: "invalid submission", props: naming("review-form"), payload: `{"outcome": "banana"}`, wantInvalid: true},
		{name: "submission naming the draft command is still validated", props: naming("review-form"), payload: `{"__command": "save_as_draft"}`, wantInvalid: true},
		{name: "draft is skipped", props: naming("review-form"), draft: true, payload: `{"outcome": "banana"}`},
		{name: "no properties", payload: `{"outcome": "approve"}`, wantErr: true},
		{name: "properties name no form", props: json.RawMessage(`{}`), payload: `{"outcome": "approve"}`, wantErr: true},
		{name: "properties that aren't an object", props: json.RawMessage(`"review-form"`), payload: `{"outcome": "approve"}`, wantErr: true},
		{name: "named form missing", props: naming("absent-form"), payload: `{"outcome": "approve"}`, wantErr: true},
		{name: "form without a schema", props: naming("schemaless-form"), payload: `{"outcome": "approve"}`, wantErr: true},
		{name: "schema that doesn't compile", props: naming("bad-schema-form"), payload: `{"outcome": "approve"}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.draft {
				ctx = WithDraft(ctx)
			}
			record := &store.TaskRecord{TaskID: "task-1", ActiveTaskTemplateID: "review-step"}

			err := ext.Execute(ctx, record, decode(t, tt.payload), tt.props)

			switch {
			case tt.wantInvalid:
				var invalid *InvalidPayloadError
				if !errors.Is(err, ErrInvalidPayload) || !errors.As(err, &invalid) || len(invalid.Violations) == 0 {
					t.Fatalf("Execute() = %v; want an *InvalidPayloadError with violations", err)
				}
			case tt.wantErr:
				if err == nil || errors.Is(err, ErrInvalidPayload) {
					t.Fatalf("Execute() = %v; want a non-validation error", err)
				}
			default:
				if err != nil {
					t.Fatalf("Execute() = %v; want nil", err)
				}
			}
		})
	}
}

func TestExtension_Execute_MatchesErrInvalidPayloadWhenWrapped(t *testing.T) {
	ext := NewExtension(fakeForms{"review-form": reviewForm}.form, namespace("reviewform"))
	err := ext.Execute(context.Background(), &store.TaskRecord{ActiveTaskTemplateID: "review-step"}, decode(t, `{}`), naming("review-form"))
	// The orchestrator wraps extension errors with %w before returning them.
	wrapped := fmt.Errorf("PRE_RESUME extension %q failed: %w", "payload_schema", err)

	var invalid *InvalidPayloadError
	if !errors.Is(wrapped, ErrInvalidPayload) || !errors.As(wrapped, &invalid) {
		t.Fatalf("wrapped error %v doesn't match ErrInvalidPayload", wrapped)
	}
}

func TestDraftMarker(t *testing.T) {
	if IsDraft(context.Background()) {
		t.Error("IsDraft on a plain context = true")
	}
	if !IsDraft(WithDraft(context.Background())) {
		t.Error("IsDraft after WithDraft = false")
	}
}

// certificateForm is a form showing read-only values the step stores: a
// reference, an amount, and a list of results whose rows the user can't add
// or remove.
const certificateForm = `{
	"id": "certificate-form",
	"schema": {
		"type": "object",
		"properties": {
			"reference": {"type": "string", "readOnly": true},
			"amount": {"type": "number", "readOnly": true},
			"decision": {"type": "string", "enum": ["issue", "reject"]},
			"results": {"type": "array", "items": {"type": "object", "properties": {
				"code": {"type": "string", "readOnly": true},
				"include": {"type": "boolean"}
			}}}
		},
		"required": ["decision"]
	},
	"uiSchema": {"type": "VerticalLayout", "elements": [
		{"type": "Control", "scope": "#/properties/decision"},
		{"type": "Control", "scope": "#/properties/results", "options": {"addable": false, "removable": false}}
	]}
}`

// storedCertificate is the task's data, the step's values under its output
// namespace.
const storedCertificate = `{"reviewform": {"reference": "R-1", "amount": 10, "results": [{"code": "C-1"}, {"code": "C-2"}]}}`

// decodeRecord returns a task record holding stored as its data, decoded with
// json.Unmarshal as core's store decodes it.
func decodeRecord(t *testing.T, stored string) *store.TaskRecord {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal([]byte(stored), &data); err != nil {
		t.Fatalf("decode stored data: %v", err)
	}
	return &store.TaskRecord{TaskID: "task-1", ActiveTaskTemplateID: "certificate-step", Data: data}
}

func TestExtension_Execute_ReadOnly(t *testing.T) {
	ext := NewExtension(fakeForms{"certificate-form": certificateForm}.form, namespace("reviewform"))
	const asStored = `"reference": "R-1", "amount": 10, "results": [{"code": "C-1", "include": true}, {"code": "C-2"}]`

	tests := []struct {
		name    string
		draft   bool
		payload string
		want    []Violation
	}{
		{name: "read-only values as stored", payload: `{"decision": "issue", ` + asStored + `}`},
		{name: "a number written differently", payload: `{"decision": "issue", "reference": "R-1", "amount": 10.0, "results": [{"code": "C-1"}, {"code": "C-2"}]}`},
		{name: "a read-only value changed", payload: `{"decision": "issue", "reference": "R-2", "amount": 10, "results": [{"code": "C-1"}, {"code": "C-2"}]}`,
			want: []Violation{{"/reference", msgReadOnlyChanged}}},
		{name: "a row removed", payload: `{"decision": "issue", "reference": "R-1", "amount": 10, "results": [{"code": "C-1"}]}`,
			want: []Violation{{"/results", msgRowsRemoved}}},
		{name: "both kinds of violation, reported together", payload: `{"reference": "R-1", "results": [{"code": "C-1"}, {"code": "C-2"}]}`,
			want: []Violation{{"/decision", "missing property"}, {"/amount", msgReadOnlyMissing}}},
		{name: "a draft skips the form's rules", draft: true, payload: `{` + asStored + `}`},
		{name: "a draft is held to the read-only rules", draft: true, payload: `{"reference": "R-2", "amount": 10, "results": [{"code": "C-1"}, {"code": "C-2"}]}`,
			want: []Violation{{"/reference", msgReadOnlyChanged}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.draft {
				ctx = WithDraft(ctx)
			}
			err := ext.Execute(ctx, decodeRecord(t, storedCertificate), decode(t, tt.payload), naming("certificate-form"))

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
			if fmt.Sprint(invalid.Violations) != fmt.Sprint(tt.want) {
				t.Errorf("violations = %v, want %v", invalid.Violations, tt.want)
			}
		})
	}
}

// The read-only check can't run without the step's stored values, or on a
// form it refuses, so the extension fails closed, on drafts too.
func TestExtension_Execute_ReadOnlyMisconfigured(t *testing.T) {
	forms := fakeForms{
		"certificate-form": certificateForm,
		"conditional-form": `{"schema": {"if": {"properties": {"a": {"const": 1}}}, "then": {"properties": {"b": {"readOnly": true}}}}}`,
	}
	failing := func(context.Context, string) (string, error) { return "", errors.New("no such step template") }
	tests := []struct {
		name        string
		form        string
		namespaceOf NamespaceOf
	}{
		{name: "namespace lookup fails", form: "certificate-form", namespaceOf: failing},
		{name: "step has no output namespace", form: "certificate-form", namespaceOf: namespace("")},
		{name: "readOnly in a conditional part", form: "conditional-form", namespaceOf: namespace("reviewform")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ext := NewExtension(forms.form, tt.namespaceOf)
			for _, ctx := range []context.Context{context.Background(), WithDraft(context.Background())} {
				err := ext.Execute(ctx, decodeRecord(t, storedCertificate), decode(t, `{"decision": "issue"}`), naming(tt.form))
				if err == nil || errors.Is(err, ErrInvalidPayload) {
					t.Fatalf("Execute() = %v; want a non-validation error", err)
				}
			}
		})
	}
}

// A form without read-only values doesn't need the step's stored values, so a
// step without an output namespace is fine.
func TestExtension_Execute_NoReadOnlyNeedsNoNamespace(t *testing.T) {
	ext := NewExtension(fakeForms{"review-form": reviewForm}.form, namespace(""))
	if err := ext.Execute(context.Background(), &store.TaskRecord{}, decode(t, `{"outcome": "approve"}`), naming("review-form")); err != nil {
		t.Fatalf("Execute() = %v; want nil", err)
	}
}

// A form without read-only values is validated as before the read-only check:
// a default beside a draft-07 $ref, which that check refuses in a form with
// read-only values, is fine here.
func TestExtension_Execute_RefDefaultWithoutReadOnly(t *testing.T) {
	const refForm = `{"schema": {
		"definitions": {"outcome": {"type": "string", "enum": ["approve", "reject"]}},
		"properties": {"outcome": {"$ref": "#/definitions/outcome", "default": "approve"}}
	}}`
	ext := NewExtension(fakeForms{"ref-form": refForm}.form, namespace("reviewform"))
	if err := ext.Execute(context.Background(), &store.TaskRecord{}, decode(t, `{"outcome": "reject"}`), naming("ref-form")); err != nil {
		t.Fatalf("Execute() = %v; want nil", err)
	}
}
