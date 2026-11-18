package payloadschema

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// testSchema exercises what the forms use: required fields, types, an enum
// written as oneOf/const, a conditional requirement, and formats, including
// "file", which neither library defines.
const testSchema = `{
	"type": "object",
	"properties": {
		"outcome": {"type": "string", "oneOf": [{"const": "approve"}, {"const": "needs_more_info"}]},
		"reason": {"type": "string"},
		"weight": {"type": "number", "exclusiveMinimum": 0},
		"inspection_date": {"type": "string", "format": "date"},
		"inspection_time": {"type": "string", "format": "time"},
		"email": {"type": "string", "format": "email"},
		"invoice": {"type": "string", "format": "file"},
		"courier": {"type": "string"},
		"tracking_number": {"type": "string"}
	},
	"required": ["outcome"],
	"if": {"properties": {"outcome": {"const": "needs_more_info"}}},
	"then": {"required": ["reason"]},
	"dependencies": {"courier": ["tracking_number"]}
}`

// decode decodes payload as the task handler does, with encoding/json, so
// numbers arrive as float64.
func decode(t *testing.T, payload string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return m
}

func TestCompile_Validate(t *testing.T) {
	compiled, err := compile([]byte(testSchema))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	tests := []struct {
		name    string
		payload string
		wantErr bool
	}{
		{name: "valid", payload: `{"outcome": "approve", "weight": 1.5}`},
		{name: "missing required field", payload: `{"weight": 1.5}`, wantErr: true},
		{name: "empty string satisfies required", payload: `{"outcome": "approve", "reason": ""}`},
		{name: "wrong type", payload: `{"outcome": "approve", "weight": "heavy"}`, wantErr: true},
		{name: "value outside oneOf", payload: `{"outcome": "banana"}`, wantErr: true},
		{name: "number out of range", payload: `{"outcome": "approve", "weight": 0}`, wantErr: true},
		{name: "conditional requirement met", payload: `{"outcome": "needs_more_info", "reason": "x"}`},
		{name: "conditional requirement missing", payload: `{"outcome": "needs_more_info"}`, wantErr: true},
		{name: "undeclared field accepted", payload: `{"outcome": "approve", "extra": 1}`},
		{name: "valid date", payload: `{"outcome": "approve", "inspection_date": "2026-02-28"}`},
		{name: "impossible date", payload: `{"outcome": "approve", "inspection_date": "2026-02-30"}`, wantErr: true},
		{name: "time without timezone, as the trader app sends", payload: `{"outcome": "approve", "inspection_time": "10:00:00"}`},
		{name: "time with timezone", payload: `{"outcome": "approve", "inspection_time": "10:00:00+05:30"}`},
		{name: "time out of range", payload: `{"outcome": "approve", "inspection_time": "24:00:00"}`, wantErr: true},
		{name: "time without seconds", payload: `{"outcome": "approve", "inspection_time": "10:00"}`, wantErr: true},
		{name: "valid email", payload: `{"outcome": "approve", "email": "trader@example.lk"}`},
		{name: "email without a dot in the domain", payload: `{"outcome": "approve", "email": "trader@localhost"}`, wantErr: true},
		{name: "unknown format ignored", payload: `{"outcome": "approve", "invoice": "anything"}`},
		{name: "draft-07 dependencies applied", payload: `{"outcome": "approve", "courier": "DHL"}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := compiled.Validate(decode(t, tt.payload))
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCompile_RejectsBadSchemas(t *testing.T) {
	tests := []struct {
		name   string
		schema string
	}{
		{name: "not JSON", schema: `{"type": `},
		{name: "not a valid schema", schema: `{"type": 5}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := compile([]byte(tt.schema)); err == nil {
				t.Fatal("compile() succeeded; want an error")
			}
		})
	}
}

func TestAjvFormats_IgnoreNonStrings(t *testing.T) {
	for _, f := range ajvFormats {
		if err := f.Validate(float64(10)); err != nil {
			t.Errorf("%s: Validate(10) = %v; want nil, formats apply to strings only", f.Name, err)
		}
	}
}

func TestValidateTime(t *testing.T) {
	tests := []struct {
		value string
		valid bool
	}{
		{"00:00:00", true},
		{"23:59:59", true},
		{"23:59:60", true},
		{"10:00:00.123", true},
		{"10:00:00Z", true},
		{"10:00:00z", true},
		{"10:00:00-0530", true},
		{"22:59:60", false},
		{"10:60:00", false},
		{"1:00:00", false},
		{"10:00:00+5", false},
		{"noon", false},
	}
	for _, tt := range tests {
		if err := validateTime(tt.value); (err == nil) != tt.valid {
			t.Errorf("validateTime(%q) = %v; want valid %v", tt.value, err, tt.valid)
		}
	}
}

func TestValidateEmail(t *testing.T) {
	tests := []struct {
		value string
		valid bool
	}{
		{"trader@example.lk", true},
		{"first.last+tag@mail.example.com", true},
		{"TRADER@EXAMPLE.LK", true},
		{"trader@localhost", false},
		{"trader@-example.lk", false},
		{".trader@example.lk", false},
		{"trader@@example.lk", false},
		{"trader", false},
	}
	for _, tt := range tests {
		if err := validateEmail(tt.value); (err == nil) != tt.valid {
			t.Errorf("validateEmail(%q) = %v; want valid %v", tt.value, err, tt.valid)
		}
	}
}

// dateLimitSchema keeps "start", and a leg's "depart", from being later than
// a sibling date.
const dateLimitSchema = `{
	"type": "object",
	"properties": {
		"start": {"type": "string", "format": "date", "formatMaximum": {"$data": "1/end"}},
		"end": {"type": "string", "format": "date"},
		"legs": {"type": "array", "items": {"type": "object", "properties": {
			"depart": {"format": "date", "formatMaximum": {"$data": "1/arrive"}},
			"arrive": {}
		}}}
	}
}`

// The cases match what the trader app's Ajv (JSON Forms' createAjv, $data on)
// accepts and refuses.
func TestAjvDateLimit(t *testing.T) {
	compiled, err := compile([]byte(dateLimitSchema))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	tests := []struct {
		name    string
		payload string
		wantErr bool
	}{
		{name: "before", payload: `{"start": "2026-06-09", "end": "2026-06-10"}`},
		{name: "the same day", payload: `{"start": "2026-06-10", "end": "2026-06-10"}`},
		{name: "after", payload: `{"start": "2026-06-12", "end": "2026-06-10"}`, wantErr: true},
		{name: "no limit", payload: `{"start": "2026-06-12"}`},
		{name: "no date", payload: `{"end": "2026-06-10"}`},
		{name: "a limit that isn't a string", payload: `{"legs": [{"depart": "2026-06-12", "arrive": 20260610}]}`, wantErr: true},
		{name: "a null limit", payload: `{"legs": [{"depart": "2026-06-12", "arrive": null}]}`, wantErr: true},
		{name: "a date that isn't a string", payload: `{"legs": [{"depart": 20260612, "arrive": "2026-06-10"}]}`},
		{name: "in list rows", payload: `{"legs": [{"depart": "2026-06-01", "arrive": "2026-06-02"}, {"depart": "2026-06-05", "arrive": "2026-06-04"}]}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := compiled.Validate(decode(t, tt.payload)); (err != nil) != tt.wantErr {
				t.Errorf("Validate() = %v; want error %v", err, tt.wantErr)
			}
		})
	}
}

// A violation is reported at the later date and names the sibling, not a value.
// Each keyword compares the date with its sibling its own way.
func TestAjvDateLimit_Keywords(t *testing.T) {
	const schema = `{"properties": {
		"max": {"format": "date", "formatMaximum": {"$data": "1/limit"}},
		"xmax": {"format": "date", "formatExclusiveMaximum": {"$data": "1/limit"}},
		"min": {"format": "date", "formatMinimum": {"$data": "1/limit"}},
		"xmin": {"format": "date", "formatExclusiveMinimum": {"$data": "1/limit"}},
		"limit": {}
	}}`
	const limit = `"limit": "2026-06-10"`
	tests := []struct {
		date string
		want []Violation
	}{
		{"2026-06-09", []Violation{
			{"/min", "must not be earlier than limit"},
			{"/xmin", "must be later than limit"},
		}},
		{"2026-06-10", []Violation{
			{"/xmax", "must be earlier than limit"},
			{"/xmin", "must be later than limit"},
		}},
		{"2026-06-11", []Violation{
			{"/max", "must not be later than limit"},
			{"/xmax", "must be earlier than limit"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.date, func(t *testing.T) {
			d := `"` + tt.date + `"`
			got := violationsOf(t, schema, `{"max": `+d+`, "xmax": `+d+`, "min": `+d+`, "xmin": `+d+`, `+limit+`}`)
			slices.SortFunc(got, func(a, b Violation) int { return strings.Compare(a.Path, b.Path) })
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("violations = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAjvDateLimit_Violation(t *testing.T) {
	got := violationsOf(t, dateLimitSchema, `{"legs": [{"depart": "2026-06-05", "arrive": "2026-06-04"}]}`)
	want := []Violation{{Path: "/legs/0/depart", Message: "must not be later than arrive"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("violations = %v, want %v", got, want)
	}
}
