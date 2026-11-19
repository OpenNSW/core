// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package staticdata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/testutil"
)

func newTestRequest(id, manifestVersion string) *http.Request {
	target := "/api/v1/static-data/" + id
	if manifestVersion != "" {
		target += "?manifestVersion=" + manifestVersion
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.SetPathValue("id", id)
	return req
}

func TestHandler_HandleGet_Success(t *testing.T) {
	reg := newTestRegistry(t, "hs-codes", "1.0.0", "refdata/hs-codes/1.0.0.json", []byte(`{"data":["0101"]}`))
	h := NewHandler(reg)

	w := httptest.NewRecorder()
	h.HandleGet(w, newTestRequest("hs-codes", "1"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %s", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc != cacheControlImmutable {
		t.Fatalf("expected immutable cache-control, got %s", cc)
	}
	if w.Body.String() != `{"data":["0101"]}` {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestHandler_HandleGet_MissingManifestVersion(t *testing.T) {
	reg := newTestRegistry(t, "hs-codes", "1.0.0", "refdata/hs-codes/1.0.0.json", []byte(`{"data":[]}`))
	h := NewHandler(reg)

	w := httptest.NewRecorder()
	h.HandleGet(w, newTestRequest("hs-codes", ""))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_HandleGet_NotFound(t *testing.T) {
	reg := newTestRegistry(t, "hs-codes", "1.0.0", "refdata/hs-codes/1.0.0.json", []byte(`{"data":[]}`))
	h := NewHandler(reg)

	w := httptest.NewRecorder()
	h.HandleGet(w, newTestRequest("missing", "1"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandler_HandleGet_Search(t *testing.T) {
	body := []byte(`{"data":[{"const":"1","title":"Port of Colombo"},{"const":"2","title":"Colombo"},{"const":"3","title":"Galle"}]}`)
	reg := newTestRegistry(t, "ports", "1.0.0", "refdata/ports/1.0.0.json", body)
	h := NewHandler(reg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/static-data/ports?manifestVersion=1&q=colombo&limit=1", nil)
	req.SetPathValue("id", "ports")
	w := httptest.NewRecorder()
	h.HandleGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); cc != cacheControlImmutable {
		t.Fatalf("expected immutable cache-control, got %s", cc)
	}
	var got SearchResult
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if got.Total != 2 || got.Offset != 0 || got.Limit != 1 || len(got.Items) != 1 || values(t, got.Items, "title")[0] != "Colombo" {
		t.Fatalf("unexpected page: %+v", got)
	}
}

func TestHandler_HandleGet_SearchParent(t *testing.T) {
	body := []byte(`{"data":[
		{"const":"a","title":"Tea Plant","parents":["Tea"]},
		{"const":"b","title":"Tea Bush","parents":["Tea"]},
		{"const":"c","title":"Coconut","parents":["Palm"]}
	]}`)
	reg := newTestRegistry(t, "names", "1.0.0", "refdata/names/1.0.0.json", body)
	h := NewHandler(reg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/static-data/names?manifestVersion=1&q=&parent=Tea&limit=1", nil)
	req.SetPathValue("id", "names")
	w := httptest.NewRecorder()
	h.HandleGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var got SearchResult
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if got.Total != 2 || len(got.Items) != 1 || values(t, got.Items, "const")[0] != "a" {
		t.Fatalf("expected parent to scope the page and total, got %+v", got)
	}
}

func TestHandler_HandleGet_SearchOffset(t *testing.T) {
	body := []byte(`{"data":[{"const":"1","title":"Port of Colombo"},{"const":"2","title":"Colombo"}]}`)
	reg := newTestRegistry(t, "ports", "1.0.0", "refdata/ports/1.0.0.json", body)
	h := NewHandler(reg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/static-data/ports?manifestVersion=1&q=colombo&offset=1&limit=20", nil)
	req.SetPathValue("id", "ports")
	w := httptest.NewRecorder()
	h.HandleGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var got SearchResult
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if got.Total != 2 || got.Offset != 1 || len(got.Items) != 1 || values(t, got.Items, "title")[0] != "Port of Colombo" {
		t.Fatalf("unexpected page: %+v", got)
	}
}

func TestHandler_HandleGet_SearchNotFound(t *testing.T) {
	reg := newTestRegistry(t, "ports", "1.0.0", "refdata/ports/1.0.0.json", []byte(`{"data":[]}`))
	h := NewHandler(reg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/static-data/missing?manifestVersion=1&q=colombo", nil)
	req.SetPathValue("id", "missing")
	w := httptest.NewRecorder()
	h.HandleGet(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandler_HandleGet_SearchMissingManifestVersion(t *testing.T) {
	reg := newTestRegistry(t, "ports", "1.0.0", "refdata/ports/1.0.0.json", []byte(`{"data":[]}`))
	h := NewHandler(reg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/static-data/ports?q=colombo", nil)
	req.SetPathValue("id", "ports")
	w := httptest.NewRecorder()
	h.HandleGet(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_HandleGet_InvalidLimit(t *testing.T) {
	reg := newTestRegistry(t, "ports", "1.0.0", "refdata/ports/1.0.0.json", []byte(`{"data":[]}`))
	h := NewHandler(reg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/static-data/ports?manifestVersion=1&limit=0", nil)
	req.SetPathValue("id", "ports")
	w := httptest.NewRecorder()
	h.HandleGet(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_HandleGet_SearchKeys(t *testing.T) {
	body := []byte(`{"data":[
		{"const":"22V4","title":"20x8 6, Mechanical","description":"Mechanical ventilation system"},
		{"const":"22G1","title":"20x8 6, General","description":"General purpose"}
	]}`)
	reg := newTestRegistry(t, "containers", "1", "refdata/containers/1.json", body)
	h := NewHandler(reg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/static-data/containers?manifestVersion=1&q=ventilation&searchKeys=title,description", nil)
	req.SetPathValue("id", "containers")
	w := httptest.NewRecorder()
	h.HandleGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got SearchResult
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if got.Total != 1 || len(got.Items) != 1 {
		t.Fatalf("expected one match, got %+v", got)
	}
	expectValues(t, values(t, got.Items, "const"), []string{"22V4"})
	expectValues(t, values(t, got.Items, "description"), []string{"Mechanical ventilation system"})
}

func TestHandler_HandleGet_SearchRowWithoutTitle(t *testing.T) {
	reg := newTestRegistry(t, "codes", "1", "refdata/codes/1.json", []byte(`{"data":[{"const":"LK"}]}`))
	h := NewHandler(reg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/static-data/codes?manifestVersion=1&q=lk", nil)
	req.SetPathValue("id", "codes")
	w := httptest.NewRecorder()
	h.HandleGet(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestHandler_HandleGet_InvalidSearchKeys(t *testing.T) {
	tooMany := "k0,k1,k2,k3,k4,k5,k6,k7,k8,k9,k10" // one more than maxSearchKeys
	for _, searchKeys := range []string{"boo,foo.bar", tooMany} {
		t.Run(searchKeys, func(t *testing.T) {
			reg := newTestRegistry(t, "codes", "1", "refdata/codes/1.json", []byte(`{"data":[]}`))
			h := NewHandler(reg)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/static-data/codes?manifestVersion=1&q=a&searchKeys="+searchKeys, nil)
			req.SetPathValue("id", "codes")
			w := httptest.NewRecorder()
			h.HandleGet(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d", w.Code)
			}
		})
	}
}

func TestParseSearchKeys_DedupesAndCapsTheCount(t *testing.T) {
	numbered := func(n int) string {
		keys := make([]string, n)
		for i := range keys {
			keys[i] = fmt.Sprintf("key%d", i)
		}
		return strings.Join(keys, ",")
	}

	t.Run("a repeated key is kept once, at its first position", func(t *testing.T) {
		got, err := parseSearchKeys("boo,foo,boo, foo ,boo")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expectValues(t, got, []string{"boo", "foo"})
	})

	t.Run("a long run of one key is one key", func(t *testing.T) {
		got, err := parseSearchKeys(strings.Repeat("boo,", 100000))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expectValues(t, got, []string{"boo"})
	})

	t.Run("the limit is accepted", func(t *testing.T) {
		got, err := parseSearchKeys(numbered(maxSearchKeys))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != maxSearchKeys {
			t.Fatalf("got %d keys, want %d", len(got), maxSearchKeys)
		}
	})

	t.Run("one more than the limit is rejected", func(t *testing.T) {
		if got, err := parseSearchKeys(numbered(maxSearchKeys + 1)); err == nil {
			t.Fatalf("expected an error, got %v", got)
		}
	})
}

func TestParseSearchKeys(t *testing.T) {
	cases := []struct {
		param   string
		want    []string
		wantErr bool
	}{
		{param: "", want: nil},
		{param: "boo,foo", want: []string{"boo", "foo"}},
		{param: " boo , ,foo,", want: []string{"boo", "foo"}},
		{param: "title_2", want: []string{"title_2"}},
		{param: "foo.bar", wantErr: true},
		{param: "boo,{foo}", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.param, func(t *testing.T) {
			got, err := parseSearchKeys(tc.param)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			expectValues(t, got, tc.want)
		})
	}
}

func serve(t *testing.T, h *Handler, id, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/static-data/"+id+"?"+query, nil)
	req.SetPathValue("id", id)
	w := httptest.NewRecorder()
	h.HandleGet(w, req)
	return w
}

func TestHandler_HandleGet_ManifestVersion(t *testing.T) {
	h := NewHandler(newTwoManifestSet(t))

	for query, want := range map[string]string{"manifestVersion=1": `{"data":["CMB"]}`, "manifestVersion=2": `{"data":["CMB","HRI"]}`} {
		w := serve(t, h, "ports", query)
		if w.Code != http.StatusOK || w.Body.String() != want {
			t.Errorf("%s: got %d %s; want 200 %s", query, w.Code, w.Body.String(), want)
		}
		if cc := w.Header().Get("Cache-Control"); cc != cacheControlImmutable {
			t.Errorf("%s: expected immutable cache-control, got %s", query, cc)
		}
	}
}

func TestHandler_HandleGet_ManifestVersionSearch(t *testing.T) {
	set, err := artifact.LoadSet(context.Background(), testutil.MemLoader{
		"ports_1.json":     []byte(`{"data":[{"const":"CMB","title":"Colombo"}]}`),
		"ports_2.json":     []byte(`{"data":[{"const":"CMB","title":"Colombo"},{"const":"HRI","title":"Hambantota"}]}`),
		"manifest_v1.json": []byte(`{"artifacts":[{"id":"ports","kind":"static_data","path":"ports_1.json"}]}`),
		"manifest_v2.json": []byte(`{"artifacts":[{"id":"ports","kind":"static_data","path":"ports_2.json"}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(set)

	for query, want := range map[string]int64{"manifestVersion=1&q=hamb": 0, "manifestVersion=2&q=hamb": 1} {
		w := serve(t, h, "ports", query)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", query, w.Code, w.Body.String())
		}
		var got SearchResult
		if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
			t.Fatalf("%s: failed to decode response: %v", query, err)
		}
		if got.Total != want {
			t.Errorf("%s: expected %d matches, got %+v", query, want, got)
		}
	}
}

func TestHandler_HandleGet_ManifestVersionErrors(t *testing.T) {
	h := NewHandler(newTwoManifestSet(t))
	cases := map[string]int{
		"manifestVersion=3":             http.StatusNotFound,
		"manifestVersion=0":             http.StatusBadRequest,
		"manifestVersion=-1":            http.StatusBadRequest,
		"manifestVersion=abc":           http.StatusBadRequest,
		"manifestVersion=":              http.StatusBadRequest,
		"manifestVersion=3&q=cmb":       http.StatusNotFound,
		"manifestVersion=abc&q=cmb":     http.StatusBadRequest,
		"manifestVersion=1&limit=0&q=c": http.StatusBadRequest,
	}
	for query, want := range cases {
		if w := serve(t, h, "ports", query); w.Code != want {
			t.Errorf("%s: expected %d, got %d", query, want, w.Code)
		}
	}
	if w := serve(t, h, "missing", "manifestVersion=1"); w.Code != http.StatusNotFound {
		t.Errorf("unknown id: expected 404, got %d", w.Code)
	}
}
