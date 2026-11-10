// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A node that runs again replaces what its earlier run wrote. A field the new result leaves out of
// an object is gone, not kept by a merge, whether the entry is required or optional.
func TestMapTaskOutputsReplacesWhatAnEarlierRunWrote(t *testing.T) {
	for _, key := range []string{"form", "form?"} {
		t.Run(key, func(t *testing.T) {
			vars := map[string]any{}
			g := newMappingTestInterpreter(vars)
			mapping := map[string]string{key: "app"}

			require.NoError(t, g.mapTaskOutputs(vars, mapping, map[string]any{"form": map[string]any{"name": "Acme", "note": "fragile"}}))
			require.NoError(t, g.mapTaskOutputs(vars, mapping, map[string]any{"form": map[string]any{"name": "Acme Ltd"}}))

			require.Equal(t, map[string]any{"app": map[string]any{"name": "Acme Ltd"}}, vars)
		})
	}
}

// Replacing a destination leaves its siblings alone: only the path the mapping names is replaced.
func TestMapTaskOutputsLeavesSiblingsOfADestination(t *testing.T) {
	vars := map[string]any{"userform": map[string]any{
		"cusdec_draft":    map[string]any{"bankCode": "B1", "bankReference": "TYPO"},
		"blendsheet_data": []any{"row"},
	}}
	g := newMappingTestInterpreter(vars)

	require.NoError(t, g.mapTaskOutputs(vars,
		map[string]string{"cusdec_draft?": "userform.cusdec_draft"},
		map[string]any{"cusdec_draft": map[string]any{"bankCode": "B1"}},
	))

	require.Equal(t, map[string]any{"userform": map[string]any{
		"cusdec_draft":    map[string]any{"bankCode": "B1"},
		"blendsheet_data": []any{"row"},
	}}, vars)
}

// Entries of one mapping whose destinations overlap still combine, whichever of them is written
// first, and nothing an earlier run left under them survives.
func TestMapTaskOutputsCombinesOverlappingDestinations(t *testing.T) {
	tests := []struct {
		name    string
		mapping map[string]string
		result  map[string]any
	}{
		{
			name:    "parent written first",
			mapping: map[string]string{"a_profile": "user", "z_ref": "user.ref"},
			result:  map[string]any{"a_profile": map[string]any{"name": "Nimal"}, "z_ref": "R-2"},
		},
		{
			name:    "child written first",
			mapping: map[string]string{"z_profile": "user", "a_ref": "user.ref"},
			result:  map[string]any{"z_profile": map[string]any{"name": "Nimal"}, "a_ref": "R-2"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			vars := map[string]any{"user": map[string]any{"stale": true, "ref": "R-1"}}
			g := newMappingTestInterpreter(vars)

			require.NoError(t, g.mapTaskOutputs(vars, test.mapping, test.result))

			require.Equal(t, map[string]any{"user": map[string]any{"name": "Nimal", "ref": "R-2"}}, vars)
		})
	}
}

// An optional entry whose source is absent writes nothing, so its destination keeps its value. An
// officer who accepts the trader's date, rather than counter-proposing one, leaves it in place.
func TestMapTaskOutputsAbsentOptionalKeepsDestination(t *testing.T) {
	vars := map[string]any{"scheduling": map[string]any{"proposed_date": "2026-11-20"}}
	g := newMappingTestInterpreter(vars)

	require.NoError(t, g.mapTaskOutputs(vars,
		map[string]string{"counter_date?": "scheduling.proposed_date", "outcome": "review.outcome"},
		map[string]any{"outcome": "accept"},
	))

	require.Equal(t, map[string]any{
		"scheduling": map[string]any{"proposed_date": "2026-11-20"},
		"review":     map[string]any{"outcome": "accept"},
	}, vars)
}

// An "x?!" entry whose source is absent removes its destination: a form field the user removed
// before resubmitting is gone, while the other fields of the form are left alone. A present "x?!"
// entry writes like any other.
func TestMapTaskOutputsAbsentClearRemovesDestination(t *testing.T) {
	vars := map[string]any{"userform": map[string]any{
		"containerCount": 2,
		"financial":      map[string]any{"bankCode": "B1", "bankReference": "TYPO"},
		"traders":        map[string]any{"declarant": "D1"},
	}}
	g := newMappingTestInterpreter(vars)

	require.NoError(t, g.mapTaskOutputs(vars,
		map[string]string{"containerCount?!": "userform.containerCount", "financial?!": "userform.financial"},
		map[string]any{"financial": map[string]any{"bankCode": "B1"}},
	))

	require.Equal(t, map[string]any{"userform": map[string]any{
		"financial": map[string]any{"bankCode": "B1"},
		"traders":   map[string]any{"declarant": "D1"},
	}}, vars)
}

// An absent "x?!" entry removes nothing when the mapping fails: a missing required source leaves
// the workflow variables exactly as they were.
func TestMapTaskOutputsAbsentClearIsAllOrNothing(t *testing.T) {
	vars := map[string]any{"userform": map[string]any{"containerCount": 2}}
	g := newMappingTestInterpreter(vars)

	err := g.mapTaskOutputs(vars,
		map[string]string{"containerCount?!": "userform.containerCount", "outcome": "review.outcome"},
		map[string]any{},
	)

	require.Error(t, err)
	require.Equal(t, ParkCategoryOutputMapping, categoryOf(err))
	require.Equal(t, map[string]any{"userform": map[string]any{"containerCount": 2}}, vars)
}

// Alternatives mapped to one destination: the one that is present sets it, whichever way the
// sources sort, and the absent "x?!" alternative does not remove what it wrote.
func TestMapTaskOutputsPresentAlternativeBeatsAbsentClear(t *testing.T) {
	mapping := map[string]string{"payment.status?!": "outcome", "review.outcome?!": "outcome"}
	for name, result := range map[string]map[string]any{
		"first alternative present":  {"payment": map[string]any{"status": "paid"}},
		"second alternative present": {"review": map[string]any{"outcome": "paid"}},
	} {
		t.Run(name, func(t *testing.T) {
			vars := map[string]any{"outcome": "stale"}
			g := newMappingTestInterpreter(vars)

			require.NoError(t, g.mapTaskOutputs(vars, mapping, result))

			require.Equal(t, map[string]any{"outcome": "paid"}, vars)
		})
	}
}

// An input mapping writes into inputs built for this run, so "x?!" there acts like "x?": an absent
// source is skipped, not reported.
func TestMapTaskInputsTreatsClearAsOptional(t *testing.T) {
	g := newMappingTestInterpreter(map[string]any{"present": "x"})

	inputs, err := g.mapTaskInputs(map[string]string{"present?!": "a", "absent?!": "b"})

	require.NoError(t, err)
	require.Equal(t, map[string]any{"a": "x"}, inputs)
}

// verifyLoopWorkflowJSON is a form that can be checked before it is submitted, the shape of the
// CusDec submission: the form starts from what was last submitted, "verify" checks it and comes
// back to the form, and "submit" ends.
//
//	start -> applicant -> command_gw -- verify --> verify -> applicant
//	                                 \- submit --> end
const verifyLoopWorkflowJSON = `
{
  "workflow_id": "verify-loop-v1",
  "name": "Form with a verify loop",
  "version": 1,
  "edges": [
    { "id": "e_start", "source_id": "start", "target_id": "applicant" },
    { "id": "e_command", "source_id": "applicant", "target_id": "command_gw" },
    { "id": "e_verify", "source_id": "command_gw", "target_id": "verify", "condition": "command == 'verify'" },
    { "id": "e_verified", "source_id": "verify", "target_id": "applicant" },
    { "id": "e_submit", "source_id": "command_gw", "target_id": "end", "condition": "command == 'submit'" }
  ],
  "nodes": [
    { "id": "start", "type": "START" },
    { "id": "applicant", "type": "TASK", "task_template_id": "USER_FORM",
      "input_mapping": { "userform?": "userform" },
      "output_mapping": { "__command": "command", "financial?": "userform.financial", "containerCount?!": "userform.containerCount" } },
    { "id": "command_gw", "type": "GATEWAY", "gateway_type": "EXCLUSIVE_SPLIT" },
    { "id": "verify", "type": "TASK", "task_template_id": "VERIFY",
      "input_mapping": { "userform": "payload" },
      "output_mapping": { "verify.verified": "verify.verified" } },
    { "id": "end", "type": "END" }
  ]
}`

// Values the user removes from the form before verifying again are gone: the second verify checks
// the form without them, and the form the user comes back to does not show them. That holds for a
// field inside a section ("financial?"), which a merge into what the first verify left kept, and
// for a field mapped on its own ("containerCount?!"), which an "x?" entry would have kept.
func TestFormFieldRemovedBeforeReverifyIsGone(t *testing.T) {
	results := []map[string]any{
		{"__command": "verify", "financial": map[string]any{"bankCode": "B1", "bankReference": "TYPO"}, "containerCount": 2},
		{"verify": map[string]any{"verified": false}},
		{"__command": "verify", "financial": map[string]any{"bankCode": "B1"}}, // the reference and the count removed
		{"verify": map[string]any{"verified": true}},
		{"__command": "submit", "financial": map[string]any{"bankCode": "B1"}},
	}
	seen, instance, _ := runRecordingSteps(t, verifyLoopWorkflowJSON, func(_ string, call int) map[string]any {
		return results[call-1]
	})

	require.Len(t, seen, len(results))
	corrected := map[string]any{"financial": map[string]any{"bankCode": "B1"}}

	require.Equal(t, "VERIFY", seen[3].template)
	require.Equal(t, corrected, seen[3].inputs["payload"], "the second verify checks the corrected form")

	require.Equal(t, "USER_FORM", seen[4].template)
	require.Equal(t, corrected, seen[4].inputs["userform"], "the form comes back without the removed value")

	require.Equal(t, corrected, instance.WorkflowVariables["userform"])
}
