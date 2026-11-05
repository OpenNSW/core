// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A form submitted again without a field it had the first time: the field is gone from the
// workflow's variables, not kept from the first submission.
func TestTaskOutputReplacesTheObjectAtItsDestination(t *testing.T) {
	g := &graphInterpreter{}
	vars := map[string]any{
		"userform": map[string]any{
			"traders": map[string]any{"exporter": "LK1", "declarant": "D1"},
			"items":   []any{"a"},
		},
	}
	mapping := map[string]string{"traders?": "userform.traders"}

	err := g.mapTaskOutputs(vars, mapping, map[string]any{"traders": map[string]any{"exporter": "LK2"}})
	require.NoError(t, err)

	require.Equal(t, map[string]any{
		"traders": map[string]any{"exporter": "LK2"},
		"items":   []any{"a"},
	}, vars["userform"], "the cleared declarant is gone; the sibling items are untouched")
}

func TestTaskOutputReplacesAWholeNamespace(t *testing.T) {
	g := &graphInterpreter{}
	vars := map[string]any{"submission": map[string]any{"a": 1, "b": 2}}

	err := g.mapTaskOutputs(vars, map[string]string{"submission": "submission"},
		map[string]any{"submission": map[string]any{"a": 3}})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"a": 3}, vars["submission"])
}

// One output's destination inside another's: whatever the task keys' order, the outer object is
// written first, so the inner value survives.
func TestTaskOutputInsideAnotherOutputSurvives(t *testing.T) {
	g := &graphInterpreter{}
	vars := map[string]any{}
	mapping := map[string]string{
		"a_note": "form.note", // sorts first by task key
		"z_form": "form",
	}

	err := g.mapTaskOutputs(vars, mapping, map[string]any{
		"a_note": "hello",
		"z_form": map[string]any{"name": "Acme"},
	})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"name": "Acme", "note": "hello"}, vars["form"])
}

// Two outputs to one destination: the later by task key wins, as it did before.
func TestTaskOutputsToOneDestinationLastTaskKeyWins(t *testing.T) {
	g := &graphInterpreter{}
	vars := map[string]any{}
	mapping := map[string]string{"a": "outcome", "b": "outcome"}

	err := g.mapTaskOutputs(vars, mapping, map[string]any{"a": "first", "b": "second"})
	require.NoError(t, err)
	require.Equal(t, "second", vars["outcome"])
}

// The variables share no maps with the result or with what the variables held before.
func TestTaskOutputSharesNoMaps(t *testing.T) {
	g := &graphInterpreter{}
	before := map[string]any{"traders": map[string]any{"exporter": "LK1"}}
	vars := map[string]any{"userform": before}
	result := map[string]any{"traders": map[string]any{"exporter": "LK2"}}

	err := g.mapTaskOutputs(vars, map[string]string{"traders": "userform.traders"}, result)
	require.NoError(t, err)

	result["traders"].(map[string]any)["exporter"] = "changed"
	require.Equal(t, "LK2", vars["userform"].(map[string]any)["traders"].(map[string]any)["exporter"])
	require.Equal(t, "LK1", before["traders"].(map[string]any)["exporter"], "the old object is not written to")
}

func TestTaskOutputCreatesTheObjectsAlongItsPath(t *testing.T) {
	g := &graphInterpreter{}
	vars := map[string]any{"verify": "not an object"}

	err := g.mapTaskOutputs(vars, map[string]string{"verified": "verify.result.ok"}, map[string]any{"verified": true})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"result": map[string]any{"ok": true}}, vars["verify"])
}

// Input mappings still merge, so a whole object and a field mapped into the same task input
// combine.
func TestTaskInputsStillMergeIntoOneObject(t *testing.T) {
	g := &graphInterpreter{instance: &WorkflowInstance{WorkflowVariables: map[string]any{
		"userform":         map[string]any{"name": "Acme"},
		"reference_number": "R-1",
	}}}

	inputs, err := g.mapTaskInputs(map[string]string{
		"userform":         "submission",
		"reference_number": "submission.reference_number",
	})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"name": "Acme", "reference_number": "R-1"}, inputs["submission"])
}
