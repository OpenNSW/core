package payloadschema

import (
	"encoding/json"
	"testing"
)

func TestListSwitches(t *testing.T) {
	both := listSwitch{addable: true, removable: true}
	neither := listSwitch{}

	tests := []struct {
		name     string
		uiSchema string
		path     string
		want     listSwitch
	}{
		{
			name:     "switches default to on",
			uiSchema: `{"type": "VerticalLayout", "elements": [{"type": "Control", "scope": "#/properties/containers"}]}`,
			path:     "/containers",
			want:     both,
		},
		{
			name: "switches set off",
			uiSchema: `{"type": "VerticalLayout", "elements": [
				{"type": "Control", "scope": "#/properties/containers", "options": {"addable": false, "removable": false}}
			]}`,
			path: "/containers",
			want: neither,
		},
		{
			name: "one switch off",
			uiSchema: `{"type": "Group", "elements": [
				{"type": "Control", "scope": "#/properties/containers", "options": {"removable": false}}
			]}`,
			path: "/containers",
			want: listSwitch{addable: true},
		},
		{
			name: "a readonly option is display only",
			uiSchema: `{"type": "VerticalLayout", "elements": [
				{"type": "Control", "scope": "#/properties/containers", "options": {"readonly": true}}
			]}`,
			path: "/containers",
			want: both,
		},
		{
			name:     "a list no control shows",
			uiSchema: `{"type": "VerticalLayout", "elements": [{"type": "Control", "scope": "#/properties/note"}]}`,
			path:     "/containers",
			want:     neither,
		},
		{
			name: "several controls allow what any allows",
			uiSchema: `{"type": "Categorization", "elements": [
				{"type": "Category", "elements": [
					{"type": "Control", "scope": "#/properties/containers", "options": {"addable": false, "removable": false}}
				]},
				{"type": "Category", "elements": [
					{"type": "Control", "scope": "#/properties/containers", "options": {"addable": false}}
				]}
			]}`,
			path: "/containers",
			want: listSwitch{removable: true},
		},
		{
			name:     "a nested scope",
			uiSchema: `{"type": "VerticalLayout", "elements": [{"type": "Control", "scope": "#/properties/order/properties/lines", "options": {"addable": false}}]}`,
			path:     "/order/lines",
			want:     listSwitch{removable: true},
		},
		{
			name: "a list in a detail, scoped from the row",
			uiSchema: `{"type": "VerticalLayout", "elements": [
				{"type": "Control", "scope": "#/properties/containers", "options": {"detail": {"type": "VerticalLayout", "elements": [
					{"type": "Control", "scope": "#/properties/seals", "options": {"removable": false}}
				]}}}
			]}`,
			path: "/containers/[]/seals",
			want: listSwitch{addable: true},
		},
		{
			name: "a list in a detail no control shows",
			uiSchema: `{"type": "VerticalLayout", "elements": [
				{"type": "Control", "scope": "#/properties/containers", "options": {"detail": {"type": "VerticalLayout", "elements": [
					{"type": "Control", "scope": "#/properties/number"}
				]}}}
			]}`,
			path: "/containers/[]/seals",
			want: neither,
		},
		{
			name:     "a list in a row with a generated layout",
			uiSchema: `{"type": "VerticalLayout", "elements": [{"type": "Control", "scope": "#/properties/containers", "options": {"addable": false}}]}`,
			path:     "/containers/[]/seals",
			want:     both,
		},
		{
			name:     "a list deeper in generated rows",
			uiSchema: `{"type": "VerticalLayout", "elements": [{"type": "Control", "scope": "#/properties/containers"}]}`,
			path:     "/containers/[]/seals/[]/marks",
			want:     both,
		},
		{
			name:     "a list of lists",
			uiSchema: `{"type": "VerticalLayout", "elements": [{"type": "Control", "scope": "#/properties/grid", "options": {"addable": false}}]}`,
			path:     "/grid/[]",
			want:     both,
		},
		{
			name:     "a list in the rows of a list of lists",
			uiSchema: `{"type": "VerticalLayout", "elements": [{"type": "Control", "scope": "#/properties/grid"}]}`,
			path:     "/grid/[]/[]/marks",
			want:     both,
		},
		{
			name:     "a list in an object in a generated row",
			uiSchema: `{"type": "VerticalLayout", "elements": [{"type": "Control", "scope": "#/properties/containers"}]}`,
			path:     "/containers/[]/owner/phones",
			want:     neither,
		},
		{
			name:     "a list behind an object control",
			uiSchema: `{"type": "VerticalLayout", "elements": [{"type": "Control", "scope": "#/properties/owner"}]}`,
			path:     "/owner/phones",
			want:     neither,
		},
		{
			name:     "a detail that isn't a layout",
			uiSchema: `{"type": "VerticalLayout", "elements": [{"type": "Control", "scope": "#/properties/containers", "options": {"detail": "GENERATED"}}]}`,
			path:     "/containers/[]/seals",
			want:     neither,
		},
		{
			name:     "a scope the check doesn't follow",
			uiSchema: `{"type": "VerticalLayout", "elements": [{"type": "Control", "scope": "#/definitions/x"}]}`,
			path:     "/definitions/x",
			want:     neither,
		},
		{
			name:     "a scope through items",
			uiSchema: `{"type": "VerticalLayout", "elements": [{"type": "Control", "scope": "#/properties/containers/items/properties/seals", "options": {"addable": false}}]}`,
			path:     "/containers/[]/seals",
			want:     listSwitch{removable: true},
		},
		{
			name:     "no UI schema shows every list",
			uiSchema: `null`,
			path:     "/order/lines",
			want:     both,
		},
		{
			name:     "no UI schema, a list in a row",
			uiSchema: ``,
			path:     "/order/lines/[]/marks",
			want:     both,
		},
		{
			name:     "no UI schema, a list in an object in a row",
			uiSchema: ``,
			path:     "/order/lines/[]/owner/phones",
			want:     neither,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, err := parseListSwitches(json.RawMessage(tt.uiSchema))
			if err != nil {
				t.Fatalf("parseListSwitches: %v", err)
			}
			if got := l.at(tt.path); got != tt.want {
				t.Errorf("at(%q) = %+v, want %+v", tt.path, got, tt.want)
			}
		})
	}
}

func TestScopePath(t *testing.T) {
	tests := []struct {
		base, scope string
		want        string
		ok          bool
	}{
		{"", "#", "", true},
		{"/a/[]", "#", "/a/[]", true},
		{"", "#/properties/a", "/a", true},
		{"", "#/properties/a/properties/b~1c", "/a/b~1c", true},
		{"/a/[]", "#/properties/b", "/a/[]/b", true},
		{"", "#/properties/a/items/properties/b", "/a/[]/b", true},
		{"", "#/properties", "", false},
		{"", "#/definitions/a", "", false},
		{"", "properties/a", "", false},
	}
	for _, tt := range tests {
		got, ok := scopePath(tt.base, tt.scope)
		if got != tt.want || ok != tt.ok {
			t.Errorf("scopePath(%q, %q) = %q, %v; want %q, %v", tt.base, tt.scope, got, ok, tt.want, tt.ok)
		}
	}
}

// A property's path escapes its key as a scope does, so a key with "/" is
// found by its control.
func TestReadOnlyForm_SwitchesByEscapedKey(t *testing.T) {
	const schema = `{"properties": {"a/b": {"type": "array", "items": {"readOnly": true}}}}`
	compiled, err := compile([]byte(schema))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	f, err := newReadOnlyForm(compiled, []byte(schema), json.RawMessage(`{"type": "Control", "scope": "#/properties/a~1b", "options": {"addable": false}}`))
	if err != nil {
		t.Fatalf("newReadOnlyForm: %v", err)
	}
	n := f.root.property("a/b")
	if n == nil {
		t.Fatal(`property("a/b") = nil`)
	}
	if got, want := n.switches(), (listSwitch{removable: true}); got != want {
		t.Errorf("switches() = %+v, want %+v", got, want)
	}
}
