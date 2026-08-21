// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package zoneview

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/OpenNSW/core/taskflow/store"
	"github.com/OpenNSW/core/uiprojector"
)

// stubTemplates resolves every template id to the same markdown body, so the
// tests below exercise visibility and handle merging rather than projection.
type stubTemplates struct{}

func (stubTemplates) GetTemplate(_ context.Context, _ string) ([]byte, error) {
	return []byte(`{"template":"body"}`), nil
}

func newTestAssembler(t *testing.T) *ZoneViewAssembler {
	t.Helper()
	asm, err := uiprojector.NewAssembler(stubTemplates{}, uiprojector.DefaultProjectors())
	if err != nil {
		t.Fatalf("build uiprojector assembler: %v", err)
	}
	return NewZoneViewAssembler(NewTaskRenderer(asm))
}

// claimGatedConfig is the shape a per-role task template uses: one section per
// role, both legal in the same state, each gated on the claim for its role.
const claimGatedConfig = `{
  "id": "test:render",
  "sections": {
    "status_message": {
      "templateId": "waiting",
      "projector": "MARKDOWN",
      "visibleWhen": { "states": ["PENDING_USER"], "requireClaim": "role:trader" }
    },
    "workspace": {
      "templateId": "form",
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

func decodeView(t *testing.T, zv ZoneView) map[string]EnrichedComponent {
	t.Helper()
	var view map[string]EnrichedComponent
	if err := json.Unmarshal(zv.View, &view); err != nil {
		t.Fatalf("decode view: %v", err)
	}
	return view
}

// A denied claim must hide the section *and* the handles it claims: the handles
// only reach the wire through a slot the projector emitted.
func TestAssemble_ClaimGatingSelectsSectionAndHandles(t *testing.T) {
	a := newTestAssembler(t)

	tests := []struct {
		name        string
		claims      map[string]bool
		wantSlot    string
		wantAbsent  string
		wantHandles int
	}{
		{
			name:        "cha sees the workspace with its submit handle",
			claims:      map[string]bool{"role:trader": false, "role:cha": true},
			wantSlot:    "workspace",
			wantAbsent:  "status_message",
			wantHandles: 1,
		},
		{
			name:        "trader sees only the notice, with no handles",
			claims:      map[string]bool{"role:trader": true, "role:cha": false},
			wantSlot:    "status_message",
			wantAbsent:  "workspace",
			wantHandles: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			zv, err := a.Assemble(context.Background(), pendingRecord(claimGatedConfig), tt.claims)
			if err != nil {
				t.Fatalf("Assemble: %v", err)
			}
			view := decodeView(t, zv)
			if len(view) != 1 {
				t.Fatalf("got %d slots %v, want exactly 1", len(view), view)
			}
			got, ok := view[tt.wantSlot]
			if !ok {
				t.Fatalf("slot %q missing from view %v", tt.wantSlot, view)
			}
			if _, ok := view[tt.wantAbsent]; ok {
				t.Errorf("slot %q must not be rendered for these claims", tt.wantAbsent)
			}
			if len(got.Handles) != tt.wantHandles {
				t.Errorf("got %d handles, want %d", len(got.Handles), tt.wantHandles)
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
	view := decodeView(t, zv)
	if len(view) != 1 || len(view["workspace"].Handles) != 1 {
		t.Fatalf("got view %v, want workspace with 1 handle", view)
	}
	if zv.State != "PENDING_USER" || zv.TaskID != "task-1" {
		t.Errorf("got %+v, want the record's task id and state carried through", zv)
	}
}
