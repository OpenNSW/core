package payloadschema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/OpenNSW/core/taskflow/store"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// FormOf returns the form template with the given id as the artifacts store it:
// a JSON object whose "schema" member is the form's JSON Schema and whose
// "uiSchema" member lays it out.
type FormOf func(ctx context.Context, formID string) ([]byte, error)

// NamespaceOf returns the output namespace of the step template with the
// given id: the key of the task data its step's values are stored under.
type NamespaceOf func(ctx context.Context, stepTemplateID string) (string, error)

// Extension is the PRE_RESUME task extension that validates a submission
// against the form the step names in the extension's properties: its JSON
// Schema, and the values it shows read-only, which must come back as the step
// stored them. It runs on every step that lists it and fails closed: a
// step whose form can't be found or compiled rejects every submission rather
// than letting one through unchecked.
type Extension struct {
	formOf      FormOf
	namespaceOf NamespaceOf
}

// properties is the extension's configuration on a step template:
//
//	{"id": "payload_schema", "phase": "PRE_RESUME", "properties": {"form": "<form id>"}}
type properties struct {
	// Form is the id of the form whose schema a submission must match.
	Form string `json:"form"`
}

// NewExtension builds the extension over the lookups it reads forms and step
// output namespaces through.
func NewExtension(formOf FormOf, namespaceOf NamespaceOf) *Extension {
	return &Extension{formOf: formOf, namespaceOf: namespaceOf}
}

// Execute runs in the PRE_RESUME phase, before the task resumes; a non-nil
// return aborts the step. A payload that doesn't match the form is reported as
// an *InvalidPayloadError, which matches ErrInvalidPayload; any other error is
// a misconfiguration or a failed lookup.
func (e *Extension) Execute(ctx context.Context, record *store.TaskRecord, payload map[string]any, props json.RawMessage) error {
	formID, err := namedForm(props)
	if err != nil {
		return fmt.Errorf("payload schema: step template %q: %w", record.ActiveTaskTemplateID, err)
	}
	f, err := e.formNamed(ctx, formID)
	if err != nil {
		return err
	}
	compiled, err := compile(f.Schema)
	if err != nil {
		return fmt.Errorf("payload schema: form %q: %w", formID, err)
	}
	readOnly, err := newReadOnlyForm(compiled, f.Schema, f.UISchema)
	if err != nil {
		return fmt.Errorf("payload schema: form %q: %w", formID, err)
	}

	data := formData(payload)
	var found []Violation
	// A draft may be incomplete, so it is not held to the form's rules. It is
	// held to the read-only ones, since it overwrites the values they compare
	// against.
	if !IsDraft(ctx) {
		if err := compiled.Validate(data); err != nil {
			var invalid *jsonschema.ValidationError
			if !errors.As(err, &invalid) {
				return fmt.Errorf("payload schema: validate submission for step template %q: %w", record.ActiveTaskTemplateID, err)
			}
			readOnlyAt := func(location []string) bool { return readOnly.readOnlyAt(data, location) }
			if dropReadOnlyFiles(invalid, readOnlyAt) {
				found = append(found, violations(invalid)...)
			}
		}
	}
	if readOnly.root != nil {
		stored, err := e.stored(ctx, record)
		if err != nil {
			return err
		}
		found = append(found, readOnly.readOnlyViolations(stored, data)...)
	}
	if len(found) > 0 {
		return &InvalidPayloadError{Violations: found}
	}
	return nil
}

// stored returns the values the active step holds: the task data under its
// step template's output namespace, which core filled when the step started
// and overwrites with a draft or a submission. A step without one stores
// nothing to compare read-only values with, so it is misconfigured.
func (e *Extension) stored(ctx context.Context, record *store.TaskRecord) (value, error) {
	namespace, err := e.namespaceOf(ctx, record.ActiveTaskTemplateID)
	if err != nil {
		return value{}, fmt.Errorf("payload schema: output namespace of step template %q: %w", record.ActiveTaskTemplateID, err)
	}
	if namespace == "" {
		return value{}, fmt.Errorf("payload schema: step template %q has no output namespace to compare its form's read-only values with", record.ActiveTaskTemplateID)
	}
	v, ok := record.Data[namespace]
	return value{v, ok}, nil
}

// namedForm returns the id of the form the extension's properties name.
func namedForm(props json.RawMessage) (string, error) {
	var p properties
	if len(props) > 0 {
		if err := json.Unmarshal(props, &p); err != nil {
			return "", fmt.Errorf("extension properties: %w", err)
		}
	}
	if p.Form == "" {
		return "", errors.New(`extension properties name no "form"`)
	}
	return p.Form, nil
}

// form is a form template's JSON Schema and UI schema, as written.
type form struct {
	Schema   json.RawMessage `json:"schema"`
	UISchema json.RawMessage `json:"uiSchema"`
}

// formNamed returns form formID. A form without a schema is misconfigured; one
// without a UI schema is laid out by the renderer.
func (e *Extension) formNamed(ctx context.Context, formID string) (form, error) {
	raw, err := e.formOf(ctx, formID)
	if err != nil {
		return form{}, fmt.Errorf("payload schema: form %q: %w", formID, err)
	}
	var f form
	if err := json.Unmarshal(raw, &f); err != nil {
		return form{}, fmt.Errorf("payload schema: form %q: %w", formID, err)
	}
	if len(f.Schema) == 0 || string(f.Schema) == "null" {
		return form{}, fmt.Errorf("payload schema: form %q has no schema", formID)
	}
	return f, nil
}

// formData returns payload without core's system keys (those prefixed "__",
// such as "__command"). They are not form data, and core strips the same keys
// before it stores a submission.
func formData(payload map[string]any) map[string]any {
	data := make(map[string]any, len(payload))
	for k, v := range payload {
		if !strings.HasPrefix(k, "__") {
			data[k] = v
		}
	}
	return data
}
