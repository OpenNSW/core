package payloadschema

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// violationsOf validates payload against schema and returns the flattened
// violations.
func violationsOf(t *testing.T, schema, payload string) []Violation {
	t.Helper()
	compiled, err := compile([]byte(schema))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	err = compiled.Validate(decode(t, payload))
	var invalid *jsonschema.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("Validate() = %v; want a validation error", err)
	}
	return violations(invalid)
}

func TestViolations(t *testing.T) {
	const schema = `{
		"type": "object",
		"properties": {
			"outcome": {"type": "string", "oneOf": [{"const": "approve"}, {"const": "needs_more_info"}]},
			"weight": {"type": "number", "exclusiveMinimum": 0},
			"items": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {"qty": {"type": "integer"}},
					"required": ["qty", "unit"]
				}
			},
			"a/b": {"type": "string"},
			"shape": {"oneOf": [{"type": "string"}, {"type": "number"}]}
		},
		"required": ["outcome", "reason"]
	}`

	tests := []struct {
		name    string
		payload string
		want    []Violation
	}{
		{
			name:    "missing properties are reported at each property",
			payload: `{}`,
			want: []Violation{
				{Path: "/outcome", Message: "missing property"},
				{Path: "/reason", Message: "missing property"},
			},
		},
		{
			name:    "a choice list names the allowed values once",
			payload: `{"outcome": "banana", "reason": "x"}`,
			want:    []Violation{{Path: "/outcome", Message: `value must be one of "approve", "needs_more_info"`}},
		},
		{
			name:    "other alternatives keep the library's message",
			payload: `{"outcome": "approve", "reason": "x", "shape": true}`,
			want:    []Violation{{Path: "/shape", Message: "'oneOf' failed, none matched"}},
		},
		{
			name:    "nested values are reported at their path",
			payload: `{"outcome": "approve", "reason": "x", "items": [{"qty": 1.5}]}`,
			want: []Violation{
				{Path: "/items/0/unit", Message: "missing property"},
				{Path: "/items/0/qty", Message: "got number, want integer"},
			},
		},
		{
			name:    "pointer tokens are escaped",
			payload: `{"outcome": "approve", "reason": "x", "a/b": 1}`,
			want:    []Violation{{Path: "/a~1b", Message: "got number, want string"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := violationsOf(t, schema, tt.payload)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("violations =\n  %+v\nwant\n  %+v", got, tt.want)
			}
		})
	}
}

func TestInvalidPayloadError(t *testing.T) {
	err := &InvalidPayloadError{Violations: []Violation{
		{Path: "/outcome", Message: "missing property"},
		{Path: "/weight", Message: "must be > 0"},
	}}
	if !errors.Is(err, ErrInvalidPayload) {
		t.Error("errors.Is(err, ErrInvalidPayload) = false")
	}
	const want = "payload schema: invalid payload: /outcome: missing property; /weight: must be > 0"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q; want %q", got, want)
	}
}

// TestViolations_KeepSubmittedValuesOut checks that no message echoes the
// submitted value: the rules whose library message does are described from the
// schema side, and a rule not known to be safe is named by its keyword alone.
func TestViolations_KeepSubmittedValuesOut(t *testing.T) {
	const schema = `{
		"type": "object",
		"properties": {
			"email": {"type": "string", "format": "email"},
			"nic": {"type": "string", "pattern": "^[0-9]{9}[VX]$"},
			"weight": {"type": "number", "minimum": 0},
			"count": {"type": "number", "maximum": 10},
			"value": {"type": "number", "exclusiveMinimum": 0.5},
			"ceiling": {"type": "number", "exclusiveMaximum": 100},
			"step": {"type": "number", "multipleOf": 5},
			"extras": {"type": "object", "additionalProperties": false}
		}
	}`
	const payload = `{
		"email": "secret-mail@localhost",
		"nic": "SECRET-NIC",
		"weight": -7777,
		"count": 8888,
		"value": 0.25,
		"ceiling": 9999,
		"step": 7,
		"extras": {"SECRET-EXTRA": "x"}
	}`
	want := map[string]string{
		"/email":   "not a valid email",
		"/nic":     `does not match pattern "^[0-9]{9}[VX]$"`,
		"/weight":  "must be >= 0",
		"/count":   "must be <= 10",
		"/value":   "must be > 0.5",
		"/ceiling": "must be < 100",
		"/step":    "must be a multiple of 5",
		// Not on the list of rules known to be safe: named by its keyword alone.
		"/extras": "'additionalProperties' failed",
	}

	got := map[string]string{}
	for _, v := range violationsOf(t, schema, payload) {
		for _, secret := range []string{"secret-mail", "SECRET-NIC", "7777", "8888", "0.25", "9999", "SECRET-EXTRA"} {
			if strings.Contains(v.Message, secret) {
				t.Errorf("%s: message %q echoes the submitted value %q", v.Path, v.Message, secret)
			}
		}
		if _, dup := got[v.Path]; !dup {
			got[v.Path] = v.Message
		}
	}
	for path, msg := range want {
		if got[path] != msg {
			t.Errorf("%s: message = %q; want %q", path, got[path], msg)
		}
	}
}
