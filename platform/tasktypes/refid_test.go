// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package tasktypes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/OpenNSW/core/refid"
	"github.com/OpenNSW/core/taskflow/plugins"
	"github.com/OpenNSW/core/taskflow/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// refIDCall is one Generate call seen by fakeRefIDs.
type refIDCall struct {
	issuer, idType string
	params         map[string]string
}

// fakeRefIDs returns "<issuer>/<idType>/<n>", counting per (issuer, idType),
// and records every call in order. failOn makes the n-th call (1-based) fail.
type fakeRefIDs struct {
	calls    []refIDCall
	counters map[string]int
	failOn   int
	err      error
}

func (f *fakeRefIDs) Generate(_ context.Context, issuer, idType string, params map[string]string) (string, error) {
	f.calls = append(f.calls, refIDCall{issuer: issuer, idType: idType, params: params})
	if f.failOn == len(f.calls) {
		return "", f.err
	}
	if f.counters == nil {
		f.counters = map[string]int{}
	}
	key := issuer + "/" + idType
	f.counters[key]++
	return fmt.Sprintf("%s/%d", key, f.counters[key]), nil
}

// memSequences is an in-memory refid.SequenceStore.
type memSequences struct {
	mu       sync.Mutex
	counters map[string]int64
}

func (m *memSequences) Next(_ context.Context, scopeKey string, maxValue int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.counters[scopeKey] >= maxValue {
		return 0, refid.ErrCounterOverflow
	}
	m.counters[scopeKey]++
	return m.counters[scopeKey], nil
}

const orderProps = `{"ids": [
	{"path": "/order/order_ref", "issuer": "ACME", "id_type": "order"},
	{"each": "/order/lines", "path": "0/line_no", "issuer": "ACME", "id_type": "order_line"}]}`

// refIDCtx builds a context whose record also holds the inputs, under the same
// keys and as the same values. Sharing the values is deliberate: a test that
// sees the record change knows the plugin wrote into its inputs.
func refIDCtx(inputs map[string]any) plugins.PluginContext {
	data := map[string]any{}
	for k, v := range inputs {
		data[k] = v
	}
	return plugins.PluginContext{
		Context:         context.Background(),
		Inputs:          inputs,
		Record:          &store.TaskRecord{TaskID: "task-1", Data: data},
		OutputNamespace: "refid",
	}
}

// order builds the inputs {"order": {"lines": [...]}} for the given lines.
func order(lines ...map[string]any) map[string]any {
	items := make([]any, len(lines))
	for i, l := range lines {
		items[i] = l
	}
	return map[string]any{"order": map[string]any{"lines": items}}
}

func line(i int) map[string]any { return map[string]any{"sku": fmt.Sprintf("SKU-%d", i)} }

func execRefIDs(t *testing.T, reg refid.Registry, ctx plugins.PluginContext, props string) error {
	t.Helper()
	return NewRefIDGeneratorPlugin(reg).Execute(ctx, json.RawMessage(props))
}

// output returns the document the plugin wrote to its namespace.
func output(t *testing.T, ctx plugins.PluginContext) map[string]any {
	t.Helper()
	out, ok := ctx.Record.Data["refid"].(map[string]any)
	require.True(t, ok, "expected a document at refid, got %T", ctx.Record.Data["refid"])
	return out
}

func outOrder(t *testing.T, ctx plugins.PluginContext) map[string]any {
	t.Helper()
	return output(t, ctx)["order"].(map[string]any)
}

func outLines(t *testing.T, ctx plugins.PluginContext) []any {
	t.Helper()
	return outOrder(t, ctx)["lines"].([]any)
}

func TestParseRefIDPointer(t *testing.T) {
	cases := []struct {
		in            string
		allowRelative bool
		wantPtr       string
		wantRelative  bool
		wantErr       bool
	}{
		{in: "/a", wantPtr: "/a"},
		{in: "/a~1b/c", wantPtr: "/a~1b/c"},
		{in: "/a", allowRelative: true, wantPtr: "/a"},
		{in: "0/a", allowRelative: true, wantPtr: "/a", wantRelative: true},
		{in: "0/a/b", allowRelative: true, wantPtr: "/a/b", wantRelative: true},
		{in: "0/a", wantErr: true}, // relative without a current element
		{in: "a", allowRelative: true, wantErr: true},
		{in: "", allowRelative: true, wantErr: true},
		{in: "0", allowRelative: true, wantErr: true},
		{in: "0#", allowRelative: true, wantErr: true},
		{in: "1/a", allowRelative: true, wantErr: true},
		{in: "2/x", allowRelative: true, wantErr: true},
		{in: "/a~2", wantErr: true},
		{in: "0/a~2", allowRelative: true, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%q relative=%v", tc.in, tc.allowRelative), func(t *testing.T) {
			p, err := parseRefIDPointer(tc.in, tc.allowRelative)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantPtr, p.ptr)
			assert.Equal(t, tc.wantRelative, p.relative)
		})
	}
}

func TestRefIDGenerator_SingleID(t *testing.T) {
	ctx := refIDCtx(nil)

	require.NoError(t, execRefIDs(t, &fakeRefIDs{}, ctx,
		`{"ids": [{"path": "/reference_id", "issuer": "ACME", "id_type": "case_ref"}]}`))

	assert.Equal(t, map[string]any{"reference_id": "ACME/case_ref/1"}, ctx.Record.Data["refid"])
}

func TestRefIDGenerator_FillsRootAndEachElement(t *testing.T) {
	reg := &fakeRefIDs{}
	ctx := refIDCtx(order(line(1), line(2), line(3)))

	require.NoError(t, execRefIDs(t, reg, ctx, orderProps))

	assert.Equal(t, "ACME/order/1", outOrder(t, ctx)["order_ref"])
	for i, item := range outLines(t, ctx) {
		m := item.(map[string]any)
		assert.Equal(t, fmt.Sprintf("ACME/order_line/%d", i+1), m["line_no"], "element %d", i)
		assert.Equal(t, fmt.Sprintf("SKU-%d", i+1), m["sku"], "other fields are carried through")
	}
	require.Len(t, reg.calls, 4)
	assert.Equal(t, "order", reg.calls[0].idType, "entries are filled in the order listed")
}

func TestRefIDGenerator_LeavesInputsAndRecordAlone(t *testing.T) {
	inputs := order(line(1))
	before, err := json.Marshal(inputs)
	require.NoError(t, err)
	ctx := refIDCtx(inputs)
	ctx.Record.Data["other"] = "untouched"

	require.NoError(t, execRefIDs(t, &fakeRefIDs{}, ctx, orderProps))

	after, err := json.Marshal(inputs)
	require.NoError(t, err)
	assert.JSONEq(t, string(before), string(after), "inputs must not be modified")
	assert.NotContains(t, ctx.Record.Data["order"], "order_ref", "the record's other keys must not change")
	assert.Equal(t, "untouched", ctx.Record.Data["other"])
}

func TestRefIDGenerator_NestedPathCreatesObjects(t *testing.T) {
	ctx := refIDCtx(map[string]any{"order": map[string]any{}})

	require.NoError(t, execRefIDs(t, &fakeRefIDs{}, ctx,
		`{"ids": [{"path": "/order/meta/ref", "issuer": "ACME", "id_type": "x"}]}`))

	assert.Equal(t, map[string]any{"ref": "ACME/x/1"}, outOrder(t, ctx)["meta"])
}

func TestRefIDGenerator_MissingOrEmptyArrayHasNoTargets(t *testing.T) {
	for name, o := range map[string]map[string]any{
		"missing": {},
		"empty":   {"lines": []any{}},
		"null":    {"lines": nil},
	} {
		t.Run(name, func(t *testing.T) {
			reg := &fakeRefIDs{}
			require.NoError(t, execRefIDs(t, reg, refIDCtx(map[string]any{"order": o}), orderProps))
			assert.Len(t, reg.calls, 1, "only order_ref is generated")
		})
	}
}

func TestRefIDGenerator_KeepsExistingIDs(t *testing.T) {
	reg := &fakeRefIDs{}
	inputs := order(map[string]any{"line_no": "L-1"}, map[string]any{"line_no": ""}, map[string]any{"line_no": nil}, map[string]any{})
	inputs["order"].(map[string]any)["order_ref"] = "ORD-1"
	ctx := refIDCtx(inputs)

	require.NoError(t, execRefIDs(t, reg, ctx, orderProps))

	lines := outLines(t, ctx)
	assert.Equal(t, "ORD-1", outOrder(t, ctx)["order_ref"])
	assert.Equal(t, "L-1", lines[0].(map[string]any)["line_no"])
	assert.Equal(t, "ACME/order_line/1", lines[1].(map[string]any)["line_no"], "an empty string is filled")
	assert.Equal(t, "ACME/order_line/2", lines[2].(map[string]any)["line_no"], "null is filled")
	assert.Equal(t, "ACME/order_line/3", lines[3].(map[string]any)["line_no"])
	assert.Len(t, reg.calls, 3)
}

func TestRefIDGenerator_OverwriteReplacesExistingIDs(t *testing.T) {
	reg := &fakeRefIDs{}
	inputs := order(map[string]any{"line_no": "client-1"}, map[string]any{"line_no": "client-2"})
	inputs["order"].(map[string]any)["order_ref"] = "ORD-1"
	ctx := refIDCtx(inputs)

	require.NoError(t, execRefIDs(t, reg, ctx, `{"ids": [
		{"path": "/order/order_ref", "issuer": "ACME", "id_type": "order"},
		{"each": "/order/lines", "path": "0/line_no", "issuer": "ACME", "id_type": "order_line", "overwrite": true}]}`))

	lines := outLines(t, ctx)
	assert.Equal(t, "ORD-1", outOrder(t, ctx)["order_ref"], "an entry without overwrite keeps its value")
	assert.Equal(t, "ACME/order_line/1", lines[0].(map[string]any)["line_no"])
	assert.Equal(t, "ACME/order_line/2", lines[1].(map[string]any)["line_no"])
	assert.Len(t, reg.calls, 2)
}

func TestRefIDGenerator_ParamsFromRootAndElement(t *testing.T) {
	reg := &fakeRefIDs{}
	inputs := order(map[string]any{"warehouse_code": "W1"}, map[string]any{"warehouse_code": "W2"}, map[string]any{})
	inputs["order"].(map[string]any)["region"] = "WEST"
	inputs["unmapped"] = "never a param"
	ctx := refIDCtx(inputs)

	require.NoError(t, execRefIDs(t, reg, ctx, `{"ids": [
		{"path": "/order/order_ref", "issuer": "ACME", "id_type": "order", "params": {"region": "/order/region"}},
		{"each": "/order/lines", "path": "0/line_no", "issuer": "ACME", "id_type": "order_line",
		 "params": {"warehouse": "0/warehouse_code", "region": "/order/region", "missing": "/order/nope"}}]}`))

	require.Len(t, reg.calls, 4)
	assert.Equal(t, map[string]string{"region": "WEST"}, reg.calls[0].params)
	assert.Equal(t, map[string]string{"region": "WEST", "warehouse": "W1"}, reg.calls[1].params)
	assert.Equal(t, map[string]string{"region": "WEST", "warehouse": "W2"}, reg.calls[2].params)
	assert.Equal(t, map[string]string{"region": "WEST"}, reg.calls[3].params, "a param with no value is omitted")
}

func TestRefIDGenerator_NoParamsWithoutParamsMap(t *testing.T) {
	reg := &fakeRefIDs{}
	ctx := refIDCtx(map[string]any{"portCode": "CMB"})

	require.NoError(t, execRefIDs(t, reg, ctx,
		`{"ids": [{"path": "/reference_id", "issuer": "ACME", "id_type": "case_ref"}]}`))

	assert.Empty(t, reg.calls[0].params, "inputs are never passed as params implicitly")
}

// Params see the document as it was before the step, not IDs generated
// earlier in the same run.
func TestRefIDGenerator_ParamsReadBeforeGenerating(t *testing.T) {
	reg := &fakeRefIDs{}
	ctx := refIDCtx(order(line(1)))

	require.NoError(t, execRefIDs(t, reg, ctx, `{"ids": [
		{"path": "/order/order_ref", "issuer": "ACME", "id_type": "order"},
		{"each": "/order/lines", "path": "0/line_no", "issuer": "ACME", "id_type": "order_line",
		 "params": {"order": "/order/order_ref"}}]}`))

	assert.Empty(t, reg.calls[1].params)
}

func TestRefIDGenerator_Rejected(t *testing.T) {
	const ok = `{"ids": [{"path": "/ref", "issuer": "ACME", "id_type": "x"}]}`
	cases := []struct {
		name   string
		props  string
		inputs map[string]any
		mut    func(*plugins.PluginContext)
	}{
		{name: "malformed properties", props: `{`},
		{name: "no ids", props: `{"ids": []}`},
		{name: "blank issuer", props: `{"ids": [{"path": "/ref", "issuer": " ", "id_type": "x"}]}`},
		{name: "missing id_type", props: `{"ids": [{"path": "/ref", "issuer": "ACME"}]}`},
		{name: "path without leading slash", props: `{"ids": [{"path": "ref", "issuer": "ACME", "id_type": "x"}]}`},
		{name: "empty path", props: `{"ids": [{"path": "", "issuer": "ACME", "id_type": "x"}]}`},
		{name: "bad escape in path", props: `{"ids": [{"path": "/a~2", "issuer": "ACME", "id_type": "x"}]}`},
		{name: "relative path without each", props: `{"ids": [{"path": "0/ref", "issuer": "ACME", "id_type": "x"}]}`},
		{name: "absolute path with each", props: `{"ids": [{"each": "/order/lines", "path": "/line_no", "issuer": "ACME", "id_type": "x"}]}`},
		{name: "relative each", props: `{"ids": [{"each": "0/lines", "path": "0/line_no", "issuer": "ACME", "id_type": "x"}]}`},
		{name: "parent-relative path", props: `{"ids": [{"each": "/order/lines", "path": "1/line_no", "issuer": "ACME", "id_type": "x"}]}`},
		{name: "relative param without each", props: `{"ids": [{"path": "/ref", "issuer": "ACME", "id_type": "x", "params": {"p": "0/x"}}]}`},
		{name: "invalid param pointer", props: `{"ids": [{"path": "/ref", "issuer": "ACME", "id_type": "x", "params": {"p": "x"}}]}`},
		{name: "empty param name", props: `{"ids": [{"path": "/ref", "issuer": "ACME", "id_type": "x", "params": {" ": "/x"}}]}`},
		{name: "duplicate target", props: `{"ids": [
			{"path": "/ref", "issuer": "ACME", "id_type": "x"},
			{"path": "/ref", "issuer": "ACME", "id_type": "y"}]}`},
		{name: "root path inside another", props: `{"ids": [
			{"path": "/ref", "issuer": "ACME", "id_type": "x"},
			{"path": "/ref/child", "issuer": "ACME", "id_type": "y"}]}`},
		{name: "root path around another", props: `{"ids": [
			{"path": "/ref/child", "issuer": "ACME", "id_type": "x"},
			{"path": "/ref", "issuer": "ACME", "id_type": "y"}]}`},
		{name: "element path inside another", props: `{"ids": [
			{"each": "/order/lines", "path": "0/no", "issuer": "ACME", "id_type": "x"},
			{"each": "/order/lines", "path": "0/no/child", "issuer": "ACME", "id_type": "y"}]}`,
			inputs: order(line(1))},
		{name: "each is not an array", props: orderProps,
			inputs: map[string]any{"order": map[string]any{"lines": map[string]any{}}}},
		{name: "element is not an object", props: orderProps,
			inputs: map[string]any{"order": map[string]any{"lines": []any{"x"}}}},
		{name: "path through a non-object", props: `{"ids": [{"path": "/ref/x", "issuer": "ACME", "id_type": "x"}]}`,
			inputs: map[string]any{"ref": "a string"}},
		{name: "target holds a non-string", props: ok, inputs: map[string]any{"ref": float64(7)}},
		{name: "param is not a string", props: `{"ids": [{"path": "/ref", "issuer": "ACME", "id_type": "x", "params": {"p": "/n"}}]}`,
			inputs: map[string]any{"n": float64(3)}},
		{name: "no output namespace", props: ok, mut: func(c *plugins.PluginContext) { c.OutputNamespace = "" }},
		{name: "nil record", props: ok, mut: func(c *plugins.PluginContext) { c.Record = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := &fakeRefIDs{}
			ctx := refIDCtx(tc.inputs)
			if tc.mut != nil {
				tc.mut(&ctx)
			}
			require.Error(t, execRefIDs(t, reg, ctx, tc.props))
			assert.Empty(t, reg.calls, "nothing may be generated for a rejected step")
			if ctx.Record != nil {
				assert.NotContains(t, ctx.Record.Data, "refid")
			}
		})
	}
}

// Overlap is checked per scope: sibling fields, the same relative path on two
// different arrays, and a root field named like an element field all pass.
func TestRefIDGenerator_NonOverlappingPathsAllowed(t *testing.T) {
	reg := &fakeRefIDs{}
	inputs := order(line(1))
	inputs["other"] = []any{map[string]any{}}
	ctx := refIDCtx(inputs)

	require.NoError(t, execRefIDs(t, reg, ctx, `{"ids": [
		{"path": "/order/a/ref", "issuer": "ACME", "id_type": "x"},
		{"path": "/order/b/ref", "issuer": "ACME", "id_type": "x"},
		{"path": "/no", "issuer": "ACME", "id_type": "x"},
		{"each": "/order/lines", "path": "0/no", "issuer": "ACME", "id_type": "y"},
		{"each": "/other", "path": "0/no", "issuer": "ACME", "id_type": "y"}]}`))

	assert.Len(t, reg.calls, 5)
}

// A shape error in a later element must stop the step before any ID is issued.
func TestRefIDGenerator_ResolvesAllTargetsBeforeGenerating(t *testing.T) {
	reg := &fakeRefIDs{}
	ctx := refIDCtx(map[string]any{"order": map[string]any{"lines": []any{
		map[string]any{}, map[string]any{"line_no": float64(2)},
	}}})

	require.Error(t, execRefIDs(t, reg, ctx, orderProps))
	assert.Empty(t, reg.calls)
}

func TestRefIDGenerator_GenerateErrorKeepsSentinel(t *testing.T) {
	reg := &fakeRefIDs{failOn: 2, err: fmt.Errorf("%w: unknown list value", refid.ErrInvalidParam)}
	ctx := refIDCtx(order(line(1), line(2)))

	err := execRefIDs(t, reg, ctx, orderProps)

	require.ErrorIs(t, err, refid.ErrInvalidParam)
	assert.Contains(t, err.Error(), "/order/lines/0/line_no")
	assert.NotContains(t, ctx.Record.Data, "refid", "a failed step writes no output")
}

func TestNewRefIDGeneratorPlugin_NilRegistryPanics(t *testing.T) {
	assert.Panics(t, func() { NewRefIDGeneratorPlugin(nil) })
}

// Two formats from one step against a real registry, with a list segment
// whose param comes from each element, then a second run that feeds the
// output back in with one more line.
func TestRefIDGenerator_RealRegistry(t *testing.T) {
	reg, err := refid.NewRegistry(refid.Config{
		Issuers: []refid.IssuerConfig{{
			Issuer: "ACME",
			Formats: []refid.FormatConfig{
				{IDType: "order", Segments: []refid.SegmentConfig{
					{Type: refid.SegmentTypeLiteral, Value: "ORD-"},
					{Type: refid.SegmentTypeSequence, Sequence: &refid.SequenceSegmentConfig{ScopeKey: "{issuer}:{idType}", Padding: 4}},
				}},
				{IDType: "order_line", Segments: []refid.SegmentConfig{
					{Type: refid.SegmentTypeList, List: "warehouse", Param: "warehouse"},
					{Type: refid.SegmentTypeLiteral, Value: "-"},
					{Type: refid.SegmentTypeSequence, Sequence: &refid.SequenceSegmentConfig{ScopeKey: "{issuer}:{idType}:{warehouse}", Padding: 4}},
				}},
			},
		}},
		Lists: map[string][]string{"warehouse": {"W1", "W2"}},
	}, refid.WithSequenceStore(&memSequences{counters: map[string]int64{}}))
	require.NoError(t, err)

	const props = `{"ids": [
		{"path": "/order/order_ref", "issuer": "ACME", "id_type": "order"},
		{"each": "/order/lines", "path": "0/line_no", "issuer": "ACME", "id_type": "order_line",
		 "params": {"warehouse": "0/warehouse_code"}}]}`
	first := refIDCtx(order(
		map[string]any{"warehouse_code": "W1"}, map[string]any{"warehouse_code": "W2"}, map[string]any{"warehouse_code": "W1"}))
	require.NoError(t, execRefIDs(t, reg, first, props))

	o := outOrder(t, first)
	lines := outLines(t, first)
	assert.Equal(t, "ORD-0001", o["order_ref"])
	assert.Equal(t, "W1-0001", lines[0].(map[string]any)["line_no"])
	assert.Equal(t, "W2-0001", lines[1].(map[string]any)["line_no"], "each warehouse counts on its own")
	assert.Equal(t, "W1-0002", lines[2].(map[string]any)["line_no"])

	o["lines"] = append(lines, map[string]any{"warehouse_code": "W2"})
	again := refIDCtx(map[string]any{"order": o})
	require.NoError(t, execRefIDs(t, reg, again, props))

	assert.Equal(t, "ORD-0001", outOrder(t, again)["order_ref"])
	assert.Equal(t, "W1-0001", outLines(t, again)[0].(map[string]any)["line_no"])
	assert.Equal(t, "W2-0002", outLines(t, again)[3].(map[string]any)["line_no"], "only the new line gets a number")
}

// A param segment embeds a step input as is: the holder number and a serial
// drawn by an earlier step land in the ID unchanged, each checked against its
// format's pattern before any ID is generated.
func TestRefIDGenerator_ParamSegments(t *testing.T) {
	reg, err := refid.NewRegistry(refid.Config{
		Issuers: []refid.IssuerConfig{{
			Issuer: "ACME",
			Formats: []refid.FormatConfig{{IDType: "certificate", Segments: []refid.SegmentConfig{
				{Type: refid.SegmentTypeLiteral, Value: "CERT-"},
				{Type: refid.SegmentTypeParam, Param: "holder", Pattern: "[0-9]{4}"},
				{Type: refid.SegmentTypeLiteral, Value: "-"},
				{Type: refid.SegmentTypeParam, Param: "serial", Pattern: "[0-9]{4}/[0-9]{2}"},
			}}},
		}},
	})
	require.NoError(t, err)
	const props = `{"ids": [{"path": "/certificate_no", "issuer": "ACME", "id_type": "certificate",
		"params": {"holder": "/holder_no", "serial": "/serial"}}]}`

	ctx := refIDCtx(map[string]any{"holder_no": "0042", "serial": "1205/26"})
	require.NoError(t, execRefIDs(t, reg, ctx, props))
	assert.Equal(t, "CERT-0042-1205/26", output(t, ctx)["certificate_no"])

	rejected := refIDCtx(map[string]any{"holder_no": "42", "serial": "1205/26"})
	require.ErrorIs(t, execRefIDs(t, reg, rejected, props), refid.ErrInvalidParam)
	assert.NotContains(t, rejected.Record.Data, "refid", "a rejected param writes no output")
}

func TestRefIDError(t *testing.T) {
	for _, tc := range []struct {
		err       error
		permanent bool
	}{
		{refid.ErrUnknownIssuer, true},
		{refid.ErrUnknownIDType, true},
		{refid.ErrCounterOverflow, true},
		{refid.ErrInvalidParam, true},
		{refid.ErrRandomCollision, false},
		{refid.ErrRandomExhausted, false},
		{errors.New("database unavailable"), false},
	} {
		got := refIDError(fmt.Errorf("generate: %w", tc.err))
		assert.Equal(t, tc.permanent, plugins.IsPermanent(got), "%v", tc.err)
	}
}
