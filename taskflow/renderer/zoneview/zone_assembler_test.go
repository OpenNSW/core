// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package zoneview

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/testutil"
	"github.com/OpenNSW/core/taskflow/renderconfig"
	"github.com/OpenNSW/core/taskflow/store"
	"github.com/OpenNSW/core/uiprojector"
)

func newTestAssembler(t *testing.T, config string) *ZoneViewAssembler {
	t.Helper()
	return NewZoneViewAssembler(newTestRenderer(t), testSet(t, config, 1))
}

// testSet is an artifact set of manifest versions 1 to versions, each listing config as the
// render config "test:render".
func testSet(t *testing.T, config string, versions int) *artifact.Set {
	t.Helper()
	files := testutil.MemLoader{
		"task.json":   []byte(`{"id":"test:task","type":"APPLICATION","workflow_id":"test:flow","render_config_id":"test:render"}`),
		"render.json": []byte(config),
	}
	for v := 1; v <= versions; v++ {
		files[artifact.ManifestPath(v)] = []byte(`{"artifacts":[{"id":"test:task","kind":"task_template","path":"task.json"},{"id":"test:render","kind":"generic_template","path":"render.json"}]}`)
	}
	set, err := artifact.LoadSet(context.Background(), files)
	if err != nil {
		t.Fatalf("LoadSet: %v", err)
	}
	return set
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

func pendingRecord() store.TaskRecord {
	return store.TaskRecord{
		TaskID:          "task-1",
		TaskType:        "APPLICATION",
		State:           "PENDING_USER",
		TaskTemplateID:  "test:task",
		ManifestVersion: 1,
	}
}

// A denied claim must hide the section *and* the handles it claims: the handles
// only reach the wire through a section the projector emitted.
func TestAssemble_ClaimGatingSelectsSectionAndHandles(t *testing.T) {
	a := newTestAssembler(t, claimGatedConfig)

	tests := []struct {
		name        string
		claims      map[string]bool
		wantID      string
		wantHandles int
	}{
		{
			name:        "cha sees the workspace with its submit handle",
			claims:      map[string]bool{"role:trader": false, "role:cha": true},
			wantID:      "workspace",
			wantHandles: 1,
		},
		{
			name:        "trader sees only the notice, with no handles",
			claims:      map[string]bool{"role:trader": true, "role:cha": false},
			wantID:      "status_message",
			wantHandles: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			zv, err := a.Assemble(context.Background(), pendingRecord(), tt.claims)
			if err != nil {
				t.Fatalf("Assemble: %v", err)
			}
			view := decodeView(t, zv.View)
			if len(view) != 1 {
				t.Fatalf("got %d components %v, want exactly 1", len(view), view)
			}
			if view[0].ID != tt.wantID {
				t.Errorf("got component %q, want %q", view[0].ID, tt.wantID)
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
	a := newTestAssembler(t, claimGatedConfig)

	for _, claims := range []map[string]bool{nil, {"role:cha": true}} {
		_, err := a.Assemble(context.Background(), pendingRecord(), claims)
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

	a := newTestAssembler(t, config)
	zv, err := a.Assemble(context.Background(), pendingRecord(), nil)
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

// The view carries the step to act on and the version, and offers no action while the task is
// ADVANCING: the submission was accepted and a second one would only be rejected as stale.
func TestAssemble_CarriesStepAndVersionAndOffersNoActionsWhileAdvancing(t *testing.T) {
	a := newTestAssembler(t, claimGatedConfig)
	claims := map[string]bool{"role:trader": false, "role:cha": true}

	pending := pendingRecord()
	pending.ActiveStepID, pending.Seq = "0a0a0a0a-0000-4000-8000-00000000000a", 4
	zv, err := a.Assemble(context.Background(), pending, claims)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if zv.StepID != pending.ActiveStepID || zv.Version != 4 {
		t.Errorf("view step/version = (%q, %d), want (%q, 4)", zv.StepID, zv.Version, pending.ActiveStepID)
	}

	advancing := pending
	advancing.State, advancing.Seq = store.StateAdvancing, 5
	zv, err = a.Assemble(context.Background(), advancing, claims)
	if err != nil {
		t.Fatalf("Assemble while ADVANCING: %v", err)
	}
	if zv.State != store.StateAdvancing || zv.Version != 5 {
		t.Errorf("view state/version = (%q, %d), want (ADVANCING, 5)", zv.State, zv.Version)
	}
	for _, c := range decodeView(t, zv.View) {
		if len(c.Handles) != 0 {
			t.Errorf("slot %q offers %d handles while ADVANCING, want none", c.ID, len(c.Handles))
		}
	}
}

// The view carries the claim the record holds, and leaves it out of the JSON when there is none.
func TestAssemble_CarriesTheClaim(t *testing.T) {
	a := newTestAssembler(t, claimGatedConfig)
	claims := map[string]bool{"role:trader": false, "role:cha": true}

	zv, err := a.Assemble(context.Background(), pendingRecord(), claims)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	encoded, err := json.Marshal(zv)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"claimed_by"`) || strings.Contains(string(encoded), `"claimed_at"`) {
		t.Errorf("unclaimed view carries a claim: %s", encoded)
	}

	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	claimed := pendingRecord()
	claimed.ClaimedBy, claimed.ClaimedAt = "user-a", &at
	zv, err = a.Assemble(context.Background(), claimed, claims)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if zv.ClaimedBy != "user-a" || zv.ClaimedAt == nil || !zv.ClaimedAt.Equal(at) {
		t.Errorf("view claim = (%q, %v), want (user-a, %v)", zv.ClaimedBy, zv.ClaimedAt, at)
	}
}

// versionProbe records the manifest version on the context of each template fetch.
type versionProbe struct{ seen []int }

func (p *versionProbe) GetTemplate(ctx context.Context, _ string) ([]byte, error) {
	v, _ := artifact.ManifestVersionFrom(ctx)
	p.seen = append(p.seen, v)
	return []byte(`"text"`), nil
}

// The renderer's template provider sees the task's manifest version.
func TestAssemble_ResolvesTemplatesThroughTheTasksManifestVersion(t *testing.T) {
	probe := &versionProbe{}
	asm, err := uiprojector.NewAssembler(probe, uiprojector.DefaultProjectors())
	if err != nil {
		t.Fatal(err)
	}
	record := pendingRecord()
	record.ManifestVersion = 7
	if _, err := NewZoneViewAssembler(NewTaskRenderer(asm), testSet(t, claimGatedConfig, 7)).Assemble(context.Background(), record, map[string]bool{"role:trader": true, "role:cha": true}); err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(probe.seen) == 0 {
		t.Fatal("no template was fetched")
	}
	for _, v := range probe.seen {
		if v != 7 {
			t.Errorf("template fetched with manifest version %d, want 7", v)
		}
	}
}

// A record that names no task template cannot be rendered.
func TestAssemble_RecordWithoutTaskTemplateErrors(t *testing.T) {
	record := pendingRecord()
	record.TaskTemplateID = ""
	if _, err := newTestAssembler(t, claimGatedConfig).Assemble(context.Background(), record, nil); !errors.Is(err, renderconfig.ErrNoTaskTemplate) {
		t.Fatalf("Assemble err = %v, want %v", err, renderconfig.ErrNoTaskTemplate)
	}
}
