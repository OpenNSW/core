package payloadschema

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// compareForm is a form with read-only values of every kind the rules cover:
// single values, one with a default, one inside an object, a list read-only as
// a whole, a list whose rows hold read-only values, with a list inside each
// row, and keys under additionalProperties.
const compareForm = `{
	"type": "object",
	"properties": {
		"reference": {"type": "string", "readOnly": true},
		"result": {"type": "string", "readOnly": true, "default": "PENDING"},
		"note": {"type": "string"},
		"applicant": {"type": "object", "properties": {"tin": {"type": "string", "readOnly": true}, "name": {"type": "string"}}},
		"items": {"type": "array", "readOnly": true, "items": {"type": "object"}},
		"containers": {
			"type": "array",
			"items": {
				"type": "object",
				"properties": {
					"cdn": {"type": "string", "readOnly": true},
					"size": {"type": "string", "readOnly": true},
					"seal": {"type": "string"},
					"marks": {"type": "array", "items": {"type": "object", "properties": {"code": {"type": "string", "readOnly": true}, "text": {"type": "string"}}}}
				}
			}
		},
		"fees": {"type": "object", "additionalProperties": {"type": "number", "readOnly": true}}
	}
}`

// listUI returns a UI schema showing the containers list with the given
// switches; its rows' marks list is generated, so both of its switches are on.
func listUI(addable, removable bool) string {
	b, _ := json.Marshal(map[string]any{
		"type": "VerticalLayout",
		"elements": []any{map[string]any{
			"type": "Control", "scope": "#/properties/containers",
			"options": map[string]any{"addable": addable, "removable": removable},
		}},
	})
	return string(b)
}

// readOnlyViolationsOf runs the read-only comparison of compareForm with uiSchema.
// A stored value of "" stands for no stored data at all.
func readOnlyViolationsOf(t *testing.T, uiSchema, stored, submitted string) []Violation {
	t.Helper()
	compiled, err := compile([]byte(compareForm))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	f, err := newReadOnlyForm(compiled, []byte(compareForm), json.RawMessage(uiSchema))
	if err != nil {
		t.Fatalf("newReadOnlyForm: %v", err)
	}
	var st value
	if stored != "" {
		st = value{decode(t, stored), true}
	}
	return f.readOnlyViolations(st, decode(t, submitted))
}

func TestReadOnlyViolations_SingleValues(t *testing.T) {
	const stored = `{"reference": "REF-1", "applicant": {"tin": "T-1", "name": "A"}, "fees": {"lab": 10}, "items": [{"id": 1}]}`
	tests := []struct {
		name      string
		stored    string
		submitted string
		want      []Violation
	}{
		{name: "everything as stored", stored: stored, submitted: `{"reference": "REF-1", "applicant": {"tin": "T-1", "name": "B"}, "fees": {"lab": 10}, "items": [{"id": 1}], "note": "x"}`},
		{name: "a number as stored, written differently", stored: stored, submitted: `{"reference": "REF-1", "applicant": {"tin": "T-1"}, "fees": {"lab": 10.0}, "items": [{"id": 1}]}`},
		{name: "changed", stored: stored, submitted: `{"reference": "REF-2", "applicant": {"tin": "T-1"}, "fees": {"lab": 10}, "items": [{"id": 1}]}`,
			want: []Violation{{"/reference", msgReadOnlyChanged}}},
		{name: "same text, other type", stored: stored, submitted: `{"reference": "REF-1", "applicant": {"tin": "T-1"}, "fees": {"lab": "10"}, "items": [{"id": 1}]}`,
			want: []Violation{{"/fees/lab", msgReadOnlyChanged}}},
		{name: "left out", stored: stored, submitted: `{"applicant": {"tin": "T-1"}, "fees": {"lab": 10}, "items": [{"id": 1}]}`,
			want: []Violation{{"/reference", msgReadOnlyMissing}}},
		{name: "its object left out", stored: stored, submitted: `{"reference": "REF-1", "fees": {"lab": 10}, "items": [{"id": 1}]}`,
			want: []Violation{{"/applicant/tin", msgReadOnlyMissing}}},
		{name: "a whole read-only list changed", stored: stored, submitted: `{"reference": "REF-1", "applicant": {"tin": "T-1"}, "fees": {"lab": 10}, "items": [{"id": 1}, {"id": 2}]}`,
			want: []Violation{{"/items", msgReadOnlyChanged}}},
		{name: "a key under additionalProperties added", stored: stored, submitted: `{"reference": "REF-1", "applicant": {"tin": "T-1"}, "fees": {"lab": 10, "extra": 5}, "items": [{"id": 1}]}`,
			want: []Violation{{"/fees/extra", msgReadOnlySet}}},
		{name: "nothing stored, left out", stored: `{}`, submitted: `{"note": "x"}`},
		{name: "nothing stored, its default", stored: `{}`, submitted: `{"result": "PENDING"}`},
		{name: "nothing stored, another value", stored: `{}`, submitted: `{"result": "PASS"}`,
			want: []Violation{{"/result", msgReadOnlySet}}},
		{name: "nothing stored, a value without a default", stored: `{}`, submitted: `{"reference": "REF-9"}`,
			want: []Violation{{"/reference", msgReadOnlySet}}},
		{name: "nothing stored, null", stored: `{}`, submitted: `{"reference": null}`,
			want: []Violation{{"/reference", msgReadOnlySet}}},
		{name: "no stored data at all", stored: "", submitted: `{"reference": "REF-9", "result": "PENDING"}`,
			want: []Violation{{"/reference", msgReadOnlySet}}},
		{name: "a stored default left out", stored: `{"result": "PENDING"}`, submitted: `{}`,
			want: []Violation{{"/result", msgReadOnlyMissing}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := readOnlyViolationsOf(t, listUI(true, true), tt.stored, tt.submitted)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("violations = %v, want %v", got, tt.want)
			}
		})
	}
}

// The design's lists: two stored rows whose read-only values are C-01 and
// C-02, under each combination of the list's switches.
func TestReadOnlyViolations_Lists(t *testing.T) {
	const stored = `{"containers": [{"cdn": "C-01", "size": "20"}, {"cdn": "C-02", "size": "40"}]}`
	const (
		asStored      = `{"containers": [{"cdn": "C-01", "size": "20", "seal": "S1"}, {"cdn": "C-02", "size": "40"}]}`
		reordered     = `{"containers": [{"cdn": "C-02", "size": "40"}, {"cdn": "C-01", "size": "20"}]}`
		plusNew       = `{"containers": [{"cdn": "C-01", "size": "20"}, {"cdn": "C-02", "size": "40"}, {"seal": "S3"}]}`
		oneRemoved    = `{"containers": [{"cdn": "C-02", "size": "40"}]}`
		blankedOut    = `{"containers": [{"cdn": "C-01", "size": "20"}, {"seal": "S2"}]}`
		changed       = `{"containers": [{"cdn": "C-01", "size": "20"}, {"cdn": "C-99", "size": "40"}]}`
		partlyChanged = `{"containers": [{"cdn": "C-01", "size": "20"}, {"cdn": "C-02", "size": "20"}]}`
		duplicated    = `{"containers": [{"cdn": "C-01", "size": "20"}, {"cdn": "C-01", "size": "20"}]}`
		newWithValue  = `{"containers": [{"cdn": "C-01", "size": "20"}, {"cdn": "C-02", "size": "40"}, {"cdn": "C-03"}]}`
	)
	added := Violation{"/containers/2", msgRowAdded}
	removed := Violation{"/containers", msgRowsRemoved}
	unknown := func(i string) Violation { return Violation{"/containers/" + i, msgRowUnknown} }

	tests := []struct {
		name               string
		addable, removable bool
		submitted          string
		want               []Violation
	}{
		{"as stored, fixed list", false, false, asStored, nil},
		{"reordered, fixed list", false, false, reordered, nil},
		{"a new row, fixed list", false, false, plusNew, []Violation{added}},
		{"a row removed, fixed list", false, false, oneRemoved, []Violation{removed}},
		{"blanked out, fixed list", false, false, blankedOut, []Violation{{"/containers/1", msgRowAdded}, removed}},

		{"a row removed, removable", false, true, oneRemoved, nil},
		{"a new row, removable", false, true, plusNew, []Violation{added}},
		{"blanked out, removable", false, true, blankedOut, []Violation{{"/containers/1", msgRowAdded}}},

		{"a new row, addable", true, false, plusNew, nil},
		{"a row removed, addable", true, false, oneRemoved, []Violation{removed}},
		{"blanked out, addable", true, false, blankedOut, []Violation{removed}},

		{"a new row, both", true, true, plusNew, nil},
		{"a row removed, both", true, true, oneRemoved, nil},
		{"blanked out, both", true, true, blankedOut, nil},

		{"changed", true, true, changed, []Violation{unknown("1")}},
		{"partly changed", true, true, partlyChanged, []Violation{unknown("1")}},
		{"a row sent twice", true, true, duplicated, []Violation{unknown("1")}},
		{"a new row carrying a read-only value", true, true, newWithValue, []Violation{unknown("2")}},
		{"not a list", true, false, `{"containers": "C-01"}`, []Violation{removed}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := readOnlyViolationsOf(t, listUI(tt.addable, tt.removable), stored, tt.submitted)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("violations = %v, want %v", got, tt.want)
			}
		})
	}
}

// Rows pair with stored rows the same values, so identical stored rows can't
// be told apart, and needn't be.
func TestReadOnlyViolations_IdenticalStoredRows(t *testing.T) {
	const stored = `{"containers": [{"cdn": "C-01"}, {"cdn": "C-01"}]}`
	if got := readOnlyViolationsOf(t, listUI(false, false), stored, `{"containers": [{"cdn": "C-01", "seal": "B"}, {"cdn": "C-01", "seal": "A"}]}`); got != nil {
		t.Errorf("both rows back: violations = %v, want none", got)
	}
	want := []Violation{{"/containers", msgRowsRemoved}}
	if got := readOnlyViolationsOf(t, listUI(false, false), stored, `{"containers": [{"cdn": "C-01"}]}`); !reflect.DeepEqual(got, want) {
		t.Errorf("one row back: violations = %v, want %v", got, want)
	}
}

// A list inside rows follows the same rule within each paired row, and inside
// a new row everything is new.
func TestReadOnlyViolations_ListInsideRows(t *testing.T) {
	const stored = `{"containers": [{"cdn": "C-01", "marks": [{"code": "M1", "text": "a"}]}]}`
	tests := []struct {
		name      string
		submitted string
		want      []Violation
	}{
		{"as stored, its text edited", `{"containers": [{"cdn": "C-01", "marks": [{"code": "M1", "text": "b"}]}]}`, nil},
		{"a mark added", `{"containers": [{"cdn": "C-01", "marks": [{"code": "M1"}, {"text": "new"}]}]}`, nil},
		{"a mark changed", `{"containers": [{"cdn": "C-01", "marks": [{"code": "M9"}]}]}`,
			[]Violation{{"/containers/0/marks/0", msgRowUnknown}}},
		{"a new row with a new mark", `{"containers": [{"cdn": "C-01", "marks": [{"code": "M1"}]}, {"marks": [{"text": "x"}]}]}`, nil},
		{"a new row with a mark the server sent", `{"containers": [{"cdn": "C-01", "marks": [{"code": "M1"}]}, {"marks": [{"code": "M1"}]}]}`,
			[]Violation{{"/containers/1/marks/0", msgRowUnknown}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := readOnlyViolationsOf(t, listUI(true, true), stored, tt.submitted)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("violations = %v, want %v", got, tt.want)
			}
		})
	}
}

// A row the server stored without a read-only value pairs with one sent with
// that value's default, as a single value would.
func TestReadOnlyViolations_RowDefaults(t *testing.T) {
	const form = `{"properties": {"lines": {"type": "array", "items": {"properties": {
		"no": {"type": "string", "readOnly": true},
		"status": {"type": "string", "readOnly": true, "default": "OPEN"}
	}}}}}`
	compiled, err := compile([]byte(form))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	f, err := newReadOnlyForm(compiled, []byte(form), json.RawMessage(`{"type": "Control", "scope": "#/properties/lines", "options": {"addable": false, "removable": false}}`))
	if err != nil {
		t.Fatalf("newReadOnlyForm: %v", err)
	}
	stored := value{decode(t, `{"lines": [{"no": "1"}, {"no": "2", "status": "OPEN"}]}`), true}
	if got := f.readOnlyViolations(stored, decode(t, `{"lines": [{"no": "2", "status": "OPEN"}, {"no": "1", "status": "OPEN"}]}`)); got != nil {
		t.Errorf("violations = %v, want none", got)
	}
}

// Rows pair by their read-only values with defaults filled in, as the browser
// sends them, in any order, and a stored default left out is still reported
// once paired. (A client filling in some defaults of a row and not others can
// be refused where another pairing would pass; the browser fills them all.)
func TestReadOnlyViolations_RowDefaultsInAnyOrder(t *testing.T) {
	const form = `{"properties": {"lines": {"type": "array", "items": {"properties": {
		"no": {"type": "string", "readOnly": true},
		"s1": {"type": "string", "readOnly": true, "default": "D1"},
		"s2": {"type": "string", "readOnly": true, "default": "D2"}
	}}}}}`
	compiled, err := compile([]byte(form))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	f, err := newReadOnlyForm(compiled, []byte(form), json.RawMessage(`{"type": "Control", "scope": "#/properties/lines", "options": {"addable": false, "removable": false}}`))
	if err != nil {
		t.Fatalf("newReadOnlyForm: %v", err)
	}
	stored := value{decode(t, `{"lines": [{"no": "1"}, {"no": "1", "s1": "D1"}]}`), true}
	if got := f.readOnlyViolations(stored, decode(t, `{"lines": [{"no": "1", "s1": "D1", "s2": "D2"}, {"no": "1", "s1": "D1", "s2": "D2"}]}`)); got != nil {
		t.Errorf("defaults filled in: violations = %v, want none", got)
	}
	want := []Violation{{"/lines/1/s1", msgReadOnlyMissing}}
	if got := f.readOnlyViolations(stored, decode(t, `{"lines": [{"no": "1"}, {"no": "1"}]}`)); !reflect.DeepEqual(got, want) {
		t.Errorf("stored default left out: violations = %v, want %v", got, want)
	}
}

func TestReadOnlyViolations_NoReadOnlyValues(t *testing.T) {
	compiled, err := compile([]byte(`{"properties": {"a": {"type": "string"}}}`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	f, err := newReadOnlyForm(compiled, []byte(`{}`), nil)
	if err != nil {
		t.Fatalf("newReadOnlyForm: %v", err)
	}
	if got := f.readOnlyViolations(value{}, decode(t, `{"a": "x"}`)); got != nil {
		t.Errorf("violations = %v, want none", got)
	}
}

// Violations name the rule, never a stored or submitted value.
func TestReadOnlyViolations_KeepValuesOut(t *testing.T) {
	got := readOnlyViolationsOf(t, listUI(false, false),
		`{"reference": "STORED-SECRET", "containers": [{"cdn": "STORED-CDN"}]}`,
		`{"reference": "SENT-SECRET", "containers": [{"cdn": "SENT-CDN"}, {"seal": "SENT-SEAL"}]}`)
	if len(got) == 0 {
		t.Fatal("want violations")
	}
	for _, v := range got {
		for _, secret := range []string{"SECRET", "CDN", "SEAL"} {
			if strings.Contains(v.Path+v.Message, secret) {
				t.Errorf("violation %v carries a value", v)
			}
		}
	}
}
