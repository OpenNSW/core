package tasktypes

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/OpenNSW/core/taskflow/plugins"
	"github.com/OpenNSW/core/taskflow/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func valueWriterCtx(inputs map[string]any) plugins.PluginContext {
	return plugins.PluginContext{
		Context:         context.Background(),
		Inputs:          inputs,
		Record:          &store.TaskRecord{TaskID: "task-1", Data: map[string]any{}},
		OutputNamespace: "written",
	}
}

func execValueWriter(ctx plugins.PluginContext, props string) error {
	return NewValueWriterPlugin().Execute(ctx, json.RawMessage(props))
}

func written(t *testing.T, ctx plugins.PluginContext) map[string]any {
	t.Helper()
	out, ok := ctx.Record.Data["written"].(map[string]any)
	require.True(t, ok, "expected a document at written, got %T", ctx.Record.Data["written"])
	return out
}

// lineItems is an application with one declaration, whose reference
// every line item carries.
func lineItems() map[string]any {
	return map[string]any{
		"declaration": map[string]any{"registration_no": "90000001", "year": "2026"},
		"userform": map[string]any{
			"line_items": []any{
				map[string]any{"item_no": "26/1911", "declaration_number": "OFC1 2026 E 68735"},
				map[string]any{"item_no": "26/1912"},
			},
			"declaration_draft": map[string]any{"identification": map[string]any{"officeCode": "OFC1"}},
		},
	}
}

const lineItemWrites = `{"writes": [
	{"each": "/userform/line_items", "path": "0/declaration_number",
	 "template": "ACME {year} E {registration_no}",
	 "params": {"year": "/declaration/year", "registration_no": "/declaration/registration_no"}},
	{"path": "/userform/declaration_draft/identification/registrationNumber", "from": "/declaration/registration_no"}
]}`

func TestValueWriter_WritesEveryElementAndOneField(t *testing.T) {
	ctx := valueWriterCtx(lineItems())

	require.NoError(t, execValueWriter(ctx, lineItemWrites))

	form := written(t, ctx)["userform"].(map[string]any)
	for i, item := range form["line_items"].([]any) {
		assert.Equal(t, "ACME 2026 E 90000001", item.(map[string]any)["declaration_number"], "line item %d", i)
	}
	assert.Equal(t, "26/1911", form["line_items"].([]any)[0].(map[string]any)["item_no"], "other fields are carried through")
	identification := form["declaration_draft"].(map[string]any)["identification"].(map[string]any)
	assert.Equal(t, "90000001", identification["registrationNumber"])
	assert.Equal(t, "OFC1", identification["officeCode"])
}

func TestValueWriter_LeavesInputsAlone(t *testing.T) {
	inputs := lineItems()
	ctx := valueWriterCtx(inputs)

	require.NoError(t, execValueWriter(ctx, lineItemWrites))

	first := inputs["userform"].(map[string]any)["line_items"].([]any)[0].(map[string]any)
	assert.Equal(t, "OFC1 2026 E 68735", first["declaration_number"])
}

func TestValueWriter_FromCopiesAnyValue(t *testing.T) {
	ctx := valueWriterCtx(map[string]any{
		"source": map[string]any{"amount": 1200.5, "tags": []any{"a"}},
		"items":  []any{map[string]any{"own": "x"}},
	})

	require.NoError(t, execValueWriter(ctx, `{"writes": [
		{"path": "/copy/amount", "from": "/source/amount"},
		{"each": "/items", "path": "0/tags", "from": "/source/tags"},
		{"each": "/items", "path": "0/mine", "from": "0/own"}
	]}`))

	out := written(t, ctx)
	assert.Equal(t, 1200.5, out["copy"].(map[string]any)["amount"], "a missing parent object is created")
	item := out["items"].([]any)[0].(map[string]any)
	assert.Equal(t, []any{"a"}, item["tags"])
	assert.Equal(t, "x", item["mine"], "a relative from reads the current element")

	item["tags"].([]any)[0] = "changed"
	assert.Equal(t, "a", out["source"].(map[string]any)["tags"].([]any)[0], "a copied value is its own copy")
}

func TestValueWriter_ReadsEveryValueBeforeWriting(t *testing.T) {
	ctx := valueWriterCtx(map[string]any{"a": "1", "b": "2"})

	require.NoError(t, execValueWriter(ctx, `{"writes": [
		{"path": "/a", "from": "/b"},
		{"path": "/b", "from": "/a"}
	]}`))

	assert.Equal(t, map[string]any{"a": "2", "b": "1"}, written(t, ctx))
}

func TestValueWriter_MissingOrEmptyArrayWritesNothing(t *testing.T) {
	for name, items := range map[string]any{"missing": nil, "empty": []any{}} {
		inputs := map[string]any{"ref": "R1"}
		if items != nil {
			inputs["items"] = items
		}
		ctx := valueWriterCtx(inputs)

		require.NoError(t, execValueWriter(ctx, `{"writes": [{"each": "/items", "path": "0/ref", "from": "/ref"}]}`), name)
		assert.Equal(t, inputs, written(t, ctx), name)
	}
}

func TestValueWriter_MissingSourceFails(t *testing.T) {
	for name, props := range map[string]string{
		"from":           `{"writes": [{"path": "/ref", "from": "/declaration/registration_no"}]}`,
		"template param": `{"writes": [{"path": "/ref", "template": "ACME {year}", "params": {"year": "/declaration/year"}}]}`,
	} {
		ctx := valueWriterCtx(map[string]any{"declaration": map[string]any{}})

		err := execValueWriter(ctx, props)

		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "has no value", name)
		assert.True(t, plugins.IsPermanent(err), name)
		assert.NotContains(t, ctx.Record.Data, "written", name)
	}
}

func TestValueWriter_Rejected(t *testing.T) {
	for name, tc := range map[string]struct{ props, want string }{
		"no writes":              {`{"writes": []}`, "at least one"},
		"from and template":      {`{"writes": [{"path": "/a", "from": "/b", "template": "x"}]}`, "not both"},
		"neither":                {`{"writes": [{"path": "/a"}]}`, `needs "from" or "template"`},
		"params with from":       {`{"writes": [{"path": "/a", "from": "/b", "params": {"x": "/c"}}]}`, "only fill"},
		"unknown placeholder":    {`{"writes": [{"path": "/a", "template": "{x}-{y}", "params": {"x": "/c"}}]}`, "{y}"},
		"absolute path in each":  {`{"writes": [{"each": "/items", "path": "/a", "from": "/b"}]}`, "must be relative"},
		"relative path, no each": {`{"writes": [{"path": "0/a", "from": "/b"}]}`, "only an entry with"},
		"overlapping paths":      {`{"writes": [{"path": "/a", "from": "/x"}, {"path": "/a/b", "from": "/y"}]}`, "overlaps"},
	} {
		ctx := valueWriterCtx(map[string]any{"b": "1", "c": "2", "x": "3", "y": "4"})

		err := execValueWriter(ctx, tc.props)

		require.Error(t, err, name)
		assert.Contains(t, err.Error(), tc.want, name)
		assert.True(t, plugins.IsPermanent(err), name)
	}
}

func TestValueWriter_WrongShapesFail(t *testing.T) {
	for name, tc := range map[string]struct {
		inputs map[string]any
		props  string
	}{
		"each is not an array":     {map[string]any{"items": "x", "r": "1"}, `{"writes": [{"each": "/items", "path": "0/a", "from": "/r"}]}`},
		"element is not an object": {map[string]any{"items": []any{"x"}, "r": "1"}, `{"writes": [{"each": "/items", "path": "0/a", "from": "/r"}]}`},
		"param is not a string":    {map[string]any{"n": 1.0}, `{"writes": [{"path": "/a", "template": "{n}", "params": {"n": "/n"}}]}`},
		"write through a value":    {map[string]any{"a": "x", "r": "1"}, `{"writes": [{"path": "/a/b", "from": "/r"}]}`},
	} {
		err := execValueWriter(valueWriterCtx(tc.inputs), tc.props)

		require.Error(t, err, name)
		assert.True(t, plugins.IsPermanent(err), name)
	}
}
