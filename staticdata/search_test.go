// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package staticdata

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
)

// values returns the value of key in each item, in order.
func values(t *testing.T, items []json.RawMessage, key string) []string {
	t.Helper()
	out := make([]string, 0, len(items))
	for _, item := range items {
		var fields map[string]any
		if err := json.Unmarshal(item, &fields); err != nil {
			t.Fatalf("item is not an object: %s", item)
		}
		out = append(out, fmt.Sprint(fields[key]))
	}
	return out
}

// searchValues searches the rows in data (a JSON array) and returns key of each match, in
// rank order.
func searchValues(t *testing.T, data, query string, searchKeys []string, key string) []string {
	t.Helper()
	got, err := Search(json.RawMessage(`{"data":`+data+`}`), query, "", searchKeys, 0, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return values(t, got.Items, key)
}

func expectValues(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSearch_PrefixBeforeContains(t *testing.T) {
	data := `[
		{"const":"1","title":"Port of Colombo"},
		{"const":"2","title":"Colombo Port"},
		{"const":"3","title":"Galle"}
	]`
	expectValues(t, searchValues(t, data, "col", nil, "const"), []string{"2", "1"})
}

func TestSearch_ExactTitleBeforePrefix(t *testing.T) {
	data := `[
		{"const":"1","title":"Colombo Port"},
		{"const":"2","title":"Colombo"}
	]`
	expectValues(t, searchValues(t, data, "COLOMBO", nil, "const"), []string{"2", "1"})
}

func TestSearch_ConstPrefixBeforeTitleContains(t *testing.T) {
	data := `[
		{"const":"X","title":"The Colombo Harbor"},
		{"const":"COL","title":"Harbor"}
	]`
	expectValues(t, searchValues(t, data, "col", nil, "const"), []string{"COL", "X"})
}

func TestSearch_WordBoundaryBeforeSubstring(t *testing.T) {
	data := `[
		{"const":"USSJL","title":"SOUTHAMPTON"},
		{"const":"USHTO","title":"EAST HAMPTON"},
		{"const":"USPHF","title":"HAMPTON/HAMPTON ROADS"}
	]`
	expectValues(t, searchValues(t, data, "hampton", nil, "const"), []string{"USPHF", "USHTO", "USSJL"})
}

func TestSearch_WordBoundaryRoads(t *testing.T) {
	data := `[
		{"const":"USUJS","title":"HAMPTON"},
		{"const":"USPHF","title":"HAMPTON/HAMPTON ROADS"}
	]`
	expectValues(t, searchValues(t, data, "roads", nil, "const"), []string{"USPHF"})
}

func TestSearch_ShorterTitleThenConst(t *testing.T) {
	data := `[
		{"const":"USHPN","title":"HAMPTON"},
		{"const":"GBHMP","title":"HAMPTON"},
		{"const":"USHPF","title":"HAMPTON FALLS"}
	]`
	expectValues(t, searchValues(t, data, "hamp", nil, "const"), []string{"GBHMP", "USHPN", "USHPF"})
}

func TestSearch_EqualLengthOrdersByConstNotTitle(t *testing.T) {
	data := `[
		{"const":"Z","title":"PORT A"},
		{"const":"A","title":"PORT B"}
	]`
	expectValues(t, searchValues(t, data, "port", nil, "const"), []string{"A", "Z"})
}

func TestSearch_ExactConst(t *testing.T) {
	data := `[
		{"const":"USUJS","title":"HAMPTON"},
		{"const":"GBHMP","title":"HAMPTON"}
	]`
	expectValues(t, searchValues(t, data, "usujs", nil, "const"), []string{"USUJS"})
}

func TestSearch_EmptyQueryKeepsArtifactOrder(t *testing.T) {
	data := `[
		{"const":"b","title":"Beta"},
		{"const":"a","title":"Alpha"}
	]`
	expectValues(t, searchValues(t, data, "   ", nil, "const"), []string{"b", "a"})
}

func TestSearch_EmptySearchKeysUseTheDefault(t *testing.T) {
	data := `[
		{"const":"LK","title":"Sri Lanka"},
		{"const":"GB","title":"United Kingdom"}
	]`
	expectValues(t, searchValues(t, data, "lk", []string{}, "const"), []string{"LK"})
	expectValues(t, searchValues(t, data, "united", []string{}, "const"), []string{"GB"})
}

func TestSearch_LimitAndOffset(t *testing.T) {
	raw := json.RawMessage(`{"data":[
		{"const":"1","title":"Port of Colombo"},
		{"const":"2","title":"Colombo"},
		{"const":"3","title":"Colombo Port"},
		"not-an-object",
		{"const":"4","title":"Galle"}
	]}`)

	got, err := Search(raw, "colombo", "", nil, 1, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Total != 3 || got.Offset != 1 || got.Limit != 1 {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	expectValues(t, values(t, got.Items, "const"), []string{"3"})
}

func TestSearch_ErrorsWhenNotAnArtifact(t *testing.T) {
	if _, err := Search(json.RawMessage(`not json`), "", "", nil, 0, 20); err == nil {
		t.Fatal("expected an error")
	}
}

func TestSearch_ErrorsOnRowWithoutConstOrTitle(t *testing.T) {
	cases := map[string]string{
		"no const":       `{"title":"Sri Lanka"}`,
		"no title":       `{"const":"LK"}`,
		"an empty title": `{"const":"LK","title":""}`,
		"a null title":   `{"const":"LK","title":null}`,
		"a number const": `{"const":42,"title":"Answer"}`,
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			raw := json.RawMessage(`{"data":[{"const":"GB","title":"United Kingdom"},` + bad + `]}`)
			if _, err := Search(raw, "", "", nil, 0, 20); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestSearch_ReturnsTheWholeRow(t *testing.T) {
	raw := json.RawMessage(`{"data":[
		{"const":"22V4","title":"20x8 6, Mechanical","description":"20x8 6, Mechanical ventilation system","group":"Ventilated","size":20}
	]}`)

	got, err := Search(raw, "22v4", "", nil, 0, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("expected one match, got %d", len(got.Items))
	}
	var row map[string]any
	if err := json.Unmarshal(got.Items[0], &row); err != nil {
		t.Fatalf("item is not an object: %s", got.Items[0])
	}
	want := map[string]any{
		"const":       "22V4",
		"title":       "20x8 6, Mechanical",
		"description": "20x8 6, Mechanical ventilation system",
		"group":       "Ventilated",
		"size":        float64(20),
	}
	if fmt.Sprint(row) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", row, want)
	}
}

func TestSearch_CustomSearchKeys(t *testing.T) {
	data := `[
		{"const":"A1","title":"First","description":"Alpha"},
		{"const":"B2","title":"Second","description":"Beta"},
		{"const":"AL9","title":"Third","description":"Gamma"}
	]`
	// "al" is a prefix of description "Alpha" and of const "AL9". Both rank in the prefix tier,
	// and their first keys (description) have equal length, so the next key (const) decides:
	// "a1" before "al9".
	keys := []string{"description", "const"}
	expectValues(t, searchValues(t, data, "al", keys, "const"), []string{"A1", "AL9"})
	expectValues(t, searchValues(t, data, "beta", keys, "const"), []string{"B2"})
}

func TestSearch_MatchesOnlyTheSearchKeys(t *testing.T) {
	data := `[
		{"const":"22V4","title":"20x8 6, Mechanical","description":"Mechanical ventilation system"}
	]`
	expectValues(t, searchValues(t, data, "ventilation", nil, "const"), []string{})
	expectValues(t, searchValues(t, data, "ventilation", []string{"const"}, "const"), []string{})
	expectValues(t, searchValues(t, data, "ventilation", []string{"title", "description"}, "const"), []string{"22V4"})
}

func TestSearch_FirstSearchKeyLengthBreaksTies(t *testing.T) {
	data := `[
		{"const":"1","title":"One","description":"Colombo Harbour"},
		{"const":"2","title":"Two","description":"Colombo"}
	]`
	// Both are in the prefix tier; the shorter first-key value wins.
	expectValues(t, searchValues(t, data, "col", []string{"description", "const"}, "const"), []string{"2", "1"})
}

func TestSearch_NumbersAndBooleansAreSearchable(t *testing.T) {
	data := `[
		{"const":"A","title":"Answer","code":42},
		{"const":"Y","title":"Yes","code":true},
		{"const":"L","title":"List","code":["42"]},
		{"const":"O","title":"Object","code":{"value":"42"}},
		{"const":"N","title":"Null","code":null}
	]`
	expectValues(t, searchValues(t, data, "42", []string{"code"}, "title"), []string{"Answer"})
	expectValues(t, searchValues(t, data, "true", []string{"code"}, "title"), []string{"Yes"})
}

func TestSearch_ParentScopesPageAndTotal(t *testing.T) {
	raw := json.RawMessage(`{"data":[
		{"const":"a","title":"Tea Plant","parents":["Tea"]},
		{"const":"b","title":"Tea Bush","parents":["Tea","Camellia"]},
		{"const":"c","title":"Coconut","parents":["Palm"]},
		{"const":"d","title":"Unscoped"}
	]}`)

	got, err := Search(raw, "", "Tea", nil, 1, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Total != 2 || got.Offset != 1 || got.Limit != 1 {
		t.Fatalf("expected a scoped total, got %+v", got)
	}
	expectValues(t, values(t, got.Items, "const"), []string{"b"})

	all, err := Search(raw, "", "", nil, 0, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if all.Total != 4 {
		t.Fatalf("expected an empty parent to keep every row, got total %d", all.Total)
	}
}

func TestFind(t *testing.T) {
	ports := json.RawMessage(`{"data":[
		{"const":"LKCMB","title":"COLOMBO"},
		{"const":"NLRTM","title":"ROTTERDAM"},
		{"const":"LKCMB","title":"COLOMBO (DUPLICATE)"},
		"not an object"
	]}`)
	cases := map[string]struct {
		raw       json.RawMessage
		value     string
		want      Option
		wantFound bool
		wantErr   bool
	}{
		"found":                         {ports, "NLRTM", Option{Const: "NLRTM", Title: "ROTTERDAM"}, true, false},
		"not found":                     {ports, "XXABC", Option{}, false, false},
		"the const, never the title":    {ports, "ROTTERDAM", Option{}, false, false},
		"the const's case must match":   {ports, "lkcmb", Option{}, false, false},
		"a duplicate const: first wins": {ports, "LKCMB", Option{Const: "LKCMB", Title: "COLOMBO"}, true, false},
		"a row without a title":         {json.RawMessage(`{"data":[{"const":"LKCMB"}]}`), "LKCMB", Option{}, false, true},
		"not a static data body":        {json.RawMessage(`[]`), "LKCMB", Option{}, false, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, found, err := Find(tc.raw, tc.value)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error: got %v, want error %v", err, tc.wantErr)
			}
			if found != tc.wantFound || got.Const != tc.want.Const || got.Title != tc.want.Title {
				t.Fatalf("got %+v, %v; want %+v, %v", got, found, tc.want, tc.wantFound)
			}
		})
	}
}

func TestSearch_MalformedParentsMatchNoParent(t *testing.T) {
	raw := json.RawMessage(`{"data":[
		{"const":"a","title":"Tea Plant","parents":"Tea"},
		{"const":"b","title":"Tea Bush","parents":["Tea"]}
	]}`)

	got, err := Search(raw, "", "Tea", nil, 0, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectValues(t, values(t, got.Items, "const"), []string{"b"})
}
