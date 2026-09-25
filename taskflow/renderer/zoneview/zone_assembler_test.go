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
	"github.com/OpenNSW/core/taskflow/store"
	"github.com/OpenNSW/core/uiprojector"
)

// stubTemplates resolves every template id to the same markdown body, so the
// tests below exercise visibility, ordering and handle merging rather than
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

func newTestAssembler(t *testing.T) *ZoneViewAssembler {
	t.Helper()
	return NewZoneViewAssembler(newTestRenderer(t))
}

// claimGatedConfig is the shape a per-role task template uses: one section per
// role, both legal in the same state, each gated on the claim for its role.
const claimGatedConfig = `{
  "id": "test:render",
  "sections": {
    "status_message": {
      "templateId": "waiting",
      "title": "Status",
      "projector": "MARKDOWN",
      "visibleWhen": { "states": ["PENDING_USER"], "requireClaim": "role:trader" }
    },
    "workspace": {
      "templateId": "form",
      "title": "Workspace",
      "projector": "MARKDOWN",
      "visibleWhen": { "states": ["PENDING_USER"], "requireClaim": "role:cha" },
      "handles": [{ "command": "submit", "label": "Submit", "element": "primary_action" }]
    }
  },
  "states": { "PENDING_USER": { "actions": [{ "command": "submit" }] } }
}`

func pendingRecord(config string) store.TaskRecord {
	return store.TaskRecord{
		TaskID:       "task-1",
		TaskType:     "APPLICATION",
		State:        "PENDING_USER",
		RenderConfig: json.RawMessage(config),
	}
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

// A denied claim must hide the section *and* the handles it claims: the handles
// only reach the wire through a section the projector emitted.
func TestAssemble_ClaimGatingSelectsSectionAndHandles(t *testing.T) {
	a := newTestAssembler(t)

	tests := []struct {
		name        string
		claims      map[string]bool
		wantTitle   string
		wantHandles int
	}{
		{
			name:        "cha sees the workspace with its submit handle",
			claims:      map[string]bool{"role:trader": false, "role:cha": true},
			wantTitle:   "Workspace",
			wantHandles: 1,
		},
		{
			name:        "trader sees only the notice, with no handles",
			claims:      map[string]bool{"role:trader": true, "role:cha": false},
			wantTitle:   "Status",
			wantHandles: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			zv, err := a.Assemble(context.Background(), pendingRecord(claimGatedConfig), tt.claims)
			if err != nil {
				t.Fatalf("Assemble: %v", err)
			}
			view := decodeView(t, zv.View)
			if len(view) != 1 {
				t.Fatalf("got %d components %v, want exactly 1", len(view), view)
			}
			if view[0].Title != tt.wantTitle {
				t.Errorf("got component %q, want %q", view[0].Title, tt.wantTitle)
			}
			if len(view[0].Handles) != tt.wantHandles {
				t.Errorf("got %d handles, want %d", len(view[0].Handles), tt.wantHandles)
			}
		})
	}
}

// A claim the config references but the caller never resolved is a caller bug,
// not a silent deny — uiprojector fails and the assembler must surface it.
func TestAssemble_ClaimNotResolvedByCallerErrors(t *testing.T) {
	a := newTestAssembler(t)

	for _, claims := range []map[string]bool{nil, {"role:cha": true}} {
		_, err := a.Assemble(context.Background(), pendingRecord(claimGatedConfig), claims)
		if err == nil {
			t.Fatalf("claims %v: want an error, got none", claims)
		}
		if !strings.Contains(err.Error(), "role:trader") {
			t.Errorf("claims %v: error should name the unresolved claim, got %v", claims, err)
		}
	}
}

// Configs that gate nothing on a claim keep working with nil claims.
func TestAssemble_NilClaimsWhenNoneReferenced(t *testing.T) {
	a := newTestAssembler(t)
	const config = `{
	  "id": "test:render",
	  "sections": {
	    "workspace": {
	      "templateId": "form",
	      "title": "Workspace",
	      "projector": "MARKDOWN",
	      "visibleWhen": { "states": ["PENDING_USER"] },
	      "handles": [{ "command": "submit", "label": "Submit" }]
	    }
	  },
	  "states": { "PENDING_USER": { "actions": [{ "command": "submit" }] } }
	}`

	zv, err := a.Assemble(context.Background(), pendingRecord(config), nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	view := decodeView(t, zv.View)
	if len(view) != 1 || len(view[0].Handles) != 1 {
		t.Fatalf("got view %v, want workspace with 1 handle", view)
	}
	if zv.State != "PENDING_USER" || zv.TaskID != "task-1" {
		t.Errorf("got %+v, want the record's task id and state carried through", zv)
	}
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
			raw, err := r.Render(context.Background(), json.RawMessage(layoutConfig), tfrenderer.Facts{State: tt.state})
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
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

	raw, err := r.Render(context.Background(), json.RawMessage(config), tfrenderer.Facts{State: "PENDING_USER"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got, want := titles(decodeView(t, raw)), []string{"C", "A", "B"}; !slices.Equal(got, want) {
		t.Errorf("got order %v, want %v", got, want)
	}
}

// An order that can't be resolved is a config error, not a silent fallback.
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

// Render alone — the path GetTaskRenderInfo takes — carries titles and the
// handles legal in the current state, not just projector output.
func TestRender_CarriesTitleAndLegalHandles(t *testing.T) {
	r := newTestRenderer(t)

	raw, err := r.Render(context.Background(), json.RawMessage(layoutConfig), tfrenderer.Facts{State: "PENDING_USER"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, c := range decodeView(t, raw) {
		wantHandles := 0
		if c.Title == "User Form" {
			wantHandles = 1
		}
		if len(c.Handles) != wantHandles {
			t.Errorf("%q: got %d handles, want %d", c.Title, len(c.Handles), wantHandles)
		}
	}

	// In a state whose actions don't include submit, the handle is dropped.
	raw, err = r.Render(context.Background(), json.RawMessage(layoutConfig), tfrenderer.Facts{State: "QUEUED_EXTERNALLY"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, c := range decodeView(t, raw) {
		if len(c.Handles) != 0 {
			t.Errorf("%q: got %d handles in QUEUED_EXTERNALLY, want 0", c.Title, len(c.Handles))
		}
	}
}

// The wire entry carries no section key.
func TestRender_DropsSectionKey(t *testing.T) {
	r := newTestRenderer(t)

	raw, err := r.Render(context.Background(), json.RawMessage(layoutConfig), tfrenderer.Facts{State: "PENDING_USER"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("decode view: %v", err)
	}
	for _, e := range entries {
		for field := range e {
			if !slices.Contains([]string{"title", "type", "handles", "payload"}, field) {
				t.Errorf("unexpected field %q in view entry %v", field, e)
			}
		}
	}
}
