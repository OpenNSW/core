// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package zoneview

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	tfrenderer "github.com/OpenNSW/core/taskflow/renderer"
	"github.com/OpenNSW/core/uiprojector"
)

// stubTemplates resolves every template id to the same markdown body, so the
// tests exercise visibility, ordering and handle merging rather than
// projection.
type stubTemplates struct{}

func (stubTemplates) GetTemplate(_ context.Context, _ string) ([]byte, error) {
	return []byte(`{"template":"body"}`), nil
}

func newTestRenderer(t *testing.T) *TaskRenderer {
	t.Helper()
	asm, err := uiprojector.NewAssembler(stubTemplates{}, uiprojector.DefaultProjectors())
	if err != nil {
		t.Fatalf("build uiprojector assembler: %v", err)
	}
	return NewTaskRenderer(asm)
}

func decodeView(t *testing.T, raw json.RawMessage) []EnrichedComponent {
	t.Helper()
	var view []EnrichedComponent
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatalf("decode view: %v", err)
	}
	return view
}

func titles(view []EnrichedComponent) []string {
	out := make([]string, len(view))
	for i, c := range view {
		out[i] = c.Title
	}
	return out
}

func render(t *testing.T, r *TaskRenderer, config string, facts tfrenderer.Facts) json.RawMessage {
	t.Helper()
	raw, err := r.Render(context.Background(), json.RawMessage(config), facts)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return raw
}

// layoutConfig shares one layout between two states and leaves a third
// without an order, mirroring how task templates declare layouts.
const layoutConfig = `{
  "id": "test:render",
  "sections": {
    "feedback":  { "templateId": "t", "title": "Feedback",  "projector": "MARKDOWN",
                   "visibleWhen": { "states": ["PENDING_USER"] } },
    "status":    { "templateId": "t", "title": "Status",    "projector": "MARKDOWN",
                   "visibleWhen": { "states": ["QUEUED_EXTERNALLY", "COMPLETED"] } },
    "user_form": { "templateId": "t", "title": "User Form", "projector": "MARKDOWN",
                   "handles": [{ "command": "submit", "label": "Submit", "element": "primary_action" }] },
    "appendix":  { "templateId": "t", "title": "Appendix",  "projector": "MARKDOWN" }
  },
  "layouts": {
    "layout_1": ["feedback", "status", "user_form", "appendix"]
  },
  "states": {
    "PENDING_USER":      { "order": { "$ref": "#/layouts/layout_1" }, "actions": [{ "command": "submit" }] },
    "QUEUED_EXTERNALLY": { "order": { "$ref": "#/layouts/layout_1" } },
    "COMPLETED":         {}
  }
}`

// A state's layout fixes the order of the sections visible in it; a state
// without an order falls back to ordering sections by key.
func TestRender_OrdersSectionsByStateLayout(t *testing.T) {
	r := newTestRenderer(t)

	tests := []struct {
		state      string
		wantTitles []string
	}{
		{"PENDING_USER", []string{"Feedback", "User Form", "Appendix"}},
		{"QUEUED_EXTERNALLY", []string{"Status", "User Form", "Appendix"}},
		// No order: keys sorted — appendix, status, user_form.
		{"COMPLETED", []string{"Appendix", "Status", "User Form"}},
	}

	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			raw := render(t, r, layoutConfig, tfrenderer.Facts{State: tt.state})
			if got := titles(decodeView(t, raw)); !slices.Equal(got, tt.wantTitles) {
				t.Errorf("got order %v, want %v", got, tt.wantTitles)
			}
		})
	}
}

// A projected section the layout doesn't list is still rendered, after the
// listed ones.
func TestRender_SectionsMissingFromLayoutGoLast(t *testing.T) {
	r := newTestRenderer(t)
	const config = `{
	  "id": "test:render",
	  "sections": {
	    "a": { "templateId": "t", "title": "A", "projector": "MARKDOWN" },
	    "b": { "templateId": "t", "title": "B", "projector": "MARKDOWN" },
	    "c": { "templateId": "t", "title": "C", "projector": "MARKDOWN" }
	  },
	  "layouts": { "main": ["c", "a"] },
	  "states": { "PENDING_USER": { "order": { "$ref": "#/layouts/main" } } }
	}`

	raw := render(t, r, config, tfrenderer.Facts{State: "PENDING_USER"})
	if got, want := titles(decodeView(t, raw)), []string{"C", "A", "B"}; !slices.Equal(got, want) {
		t.Errorf("got order %v, want %v", got, want)
	}
}

// An order that can't be resolved(invalid $ref) is a config error, not a silent fallback.
func TestRender_UnresolvableOrderErrors(t *testing.T) {
	r := newTestRenderer(t)

	tests := []struct {
		name    string
		ref     string
		wantErr string
	}{
		{"malformed ref", "layouts/main", "is not of the form"},
		{"undefined layout", "#/layouts/other", `undefined layout "other"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := `{
			  "id": "test:render",
			  "sections": { "a": { "templateId": "t", "projector": "MARKDOWN" } },
			  "layouts": { "main": ["a"] },
			  "states": { "PENDING_USER": { "order": { "$ref": "` + tt.ref + `" } } }
			}`
			_, err := r.Render(context.Background(), json.RawMessage(config), tfrenderer.Facts{State: "PENDING_USER"})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got error %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

// Render alone — carries titles and the
// handles legal in the current state, not just projector output.
func TestRender_CarriesTitleAndLegalHandles(t *testing.T) {
	r := newTestRenderer(t)

	for _, c := range decodeView(t, render(t, r, layoutConfig, tfrenderer.Facts{State: "PENDING_USER"})) {
		wantHandles := 0
		if c.Title == "User Form" {
			wantHandles = 1
		}
		if len(c.Handles) != wantHandles {
			t.Errorf("%q: got %d handles, want %d", c.Title, len(c.Handles), wantHandles)
		}
	}

	// In a state whose actions don't include submit, the handle is dropped.
	for _, c := range decodeView(t, render(t, r, layoutConfig, tfrenderer.Facts{State: "QUEUED_EXTERNALLY"})) {
		if len(c.Handles) != 0 {
			t.Errorf("%q: got %d handles in QUEUED_EXTERNALLY, want 0", c.Title, len(c.Handles))
		}
	}
}

// With no render config there is nothing to show: an empty list
func TestRender_EmptyConfigReturnsEmptyList(t *testing.T) {
	r := newTestRenderer(t)

	raw, err := r.Render(context.Background(), nil, tfrenderer.Facts{State: "PENDING_USER"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(raw) != "[]" {
		t.Errorf("got %s, want []", raw)
	}
}

// MARKDOWN output is wrapped as {"content": …}; other projectors' output is
// passed through as the payload unchanged.
func TestRender_PayloadShapeByProjector(t *testing.T) {
	r := newTestRenderer(t)
	const config = `{
	  "id": "test:render",
	  "sections": {
	    "md":  { "templateId": "t", "title": "Markdown", "projector": "MARKDOWN" },
	    "raw": { "templateId": "t", "title": "Raw", "projector": "RAW", "dataKey": "raw" }
	  }
	}`

	view := decodeView(t, render(t, r, config, tfrenderer.Facts{
		State: "PENDING_USER",
		Data:  map[string]any{"raw": map[string]any{"a": 1}},
	}))
	if len(view) != 2 {
		t.Fatalf("got %d components %v, want 2", len(view), view)
	}

	tests := []struct {
		got         EnrichedComponent
		wantType    string
		wantPayload string
	}{
		{view[0], "MARKDOWN", `{"content":"body"}`},
		{view[1], "RAW", `{"a":1}`},
	}
	for _, tt := range tests {
		if tt.got.Type != tt.wantType {
			t.Errorf("%q: got type %q, want %q", tt.got.Title, tt.got.Type, tt.wantType)
		}
		if string(tt.got.Payload) != tt.wantPayload {
			t.Errorf("%q: got payload %s, want %s", tt.got.Title, tt.got.Payload, tt.wantPayload)
		}
	}
}

// A section without a title renders with no title field.
func TestRender_OmitsEmptyTitle(t *testing.T) {
	r := newTestRenderer(t)
	const config = `{
	  "id": "test:render",
	  "sections": { "a": { "templateId": "t", "projector": "MARKDOWN" } }
	}`

	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(render(t, r, config, tfrenderer.Facts{State: "PENDING_USER"}), &entries); err != nil {
		t.Fatalf("decode view: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if _, ok := entries[0]["title"]; ok {
		t.Errorf("got title field in %v, want it omitted", entries[0])
	}
}

// Legal handles keep the order the section declares them in; illegal ones are
// dropped from wherever they sit.
func TestRender_HandlesKeepDeclarationOrder(t *testing.T) {
	r := newTestRenderer(t)
	const config = `{
	  "id": "test:render",
	  "sections": {
	    "form": {
	      "templateId": "t",
	      "projector": "MARKDOWN",
	      "handles": [
	        { "command": "save_as_draft", "label": "Save as Draft" },
	        { "command": "withdraw", "label": "Withdraw" },
	        { "command": "submit", "label": "Submit" }
	      ]
	    }
	  },
	  "states": {
	    "PENDING_USER": { "actions": [{ "command": "submit" }, { "command": "save_as_draft" }] }
	  }
	}`

	view := decodeView(t, render(t, r, config, tfrenderer.Facts{State: "PENDING_USER"}))
	if len(view) != 1 {
		t.Fatalf("got %d components %v, want 1", len(view), view)
	}
	got := make([]string, len(view[0].Handles))
	for i, h := range view[0].Handles {
		got[i] = h.Command
	}
	if want := []string{"save_as_draft", "submit"}; !slices.Equal(got, want) {
		t.Errorf("got handles %v, want %v", got, want)
	}
}

func TestResolveLayout(t *testing.T) {
	cfg := TaskTemplateConfig{
		Layouts: map[string][]string{"main": {"b", "a"}},
		States: map[string]StateView{
			"ORDERED":   {Order: &LayoutRef{Ref: "#/layouts/main"}},
			"UNORDERED": {},
			"NO_PREFIX": {Order: &LayoutRef{Ref: "main"}},
			"NO_NAME":   {Order: &LayoutRef{Ref: "#/layouts/"}},
			"UNDEFINED": {Order: &LayoutRef{Ref: "#/layouts/other"}},
		},
	}

	tests := []struct {
		state   string
		want    []string
		wantErr string
	}{
		{state: "ORDERED", want: []string{"b", "a"}},
		{state: "UNORDERED", want: nil},
		{state: "NOT_IN_CONFIG", want: nil},
		{state: "NO_PREFIX", wantErr: "is not of the form"},
		{state: "NO_NAME", wantErr: "is not of the form"},
		{state: "UNDEFINED", wantErr: `undefined layout "other"`},
	}

	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			got, err := resolveLayout(cfg, tt.state)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("got error %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveLayout: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOrderSlots(t *testing.T) {
	sections := func(keys ...string) map[string]uiprojector.Section {
		m := make(map[string]uiprojector.Section, len(keys))
		for _, k := range keys {
			m[k] = uiprojector.Section{}
		}
		return m
	}

	tests := []struct {
		name     string
		sections map[string]uiprojector.Section
		layout   []string
		want     []string
	}{
		{"nil layout sorts by key", sections("c", "a", "b"), nil, []string{"a", "b", "c"}},
		{"empty layout sorts by key", sections("c", "a", "b"), []string{}, []string{"a", "b", "c"}},
		{"layout order wins", sections("a", "b", "c"), []string{"c", "a", "b"}, []string{"c", "a", "b"}},
		{"hidden layout entries skipped", sections("a", "c"), []string{"c", "b", "a"}, []string{"c", "a"}},
		{"unlisted sections last, sorted", sections("a", "b", "c", "d"), []string{"c"}, []string{"c", "a", "b", "d"}},
		{"duplicates placed once", sections("a", "b"), []string{"b", "a", "b"}, []string{"b", "a"}},
		{"no sections", sections(), []string{"a"}, []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := orderSlots(tt.sections, tt.layout); !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
