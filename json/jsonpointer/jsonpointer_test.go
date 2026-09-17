// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package jsonpointer

import "testing"

func TestValid(t *testing.T) {
	tests := []struct {
		pointer string
		want    bool
	}{
		{"/district", true},
		{"/importer/address/district", true},
		{"/a~1b/c~0d", true},
		{"", false},         // root pointer: nothing to point at for our use case
		{"district", false}, // missing leading slash
		{"no-slash", false},
		{"/a~2b", false}, // '~' followed by anything other than '0'/'1' is invalid RFC 6901 escaping
		{"/a~", false},   // trailing '~' with nothing to escape
	}
	for _, tt := range tests {
		if got := Valid(tt.pointer); got != tt.want {
			t.Errorf("Valid(%q) = %v, want %v", tt.pointer, got, tt.want)
		}
	}
}

func TestGet(t *testing.T) {
	doc := map[string]any{
		"district": "Colombo",
		"importer": map[string]any{
			"address": map[string]any{
				"district": "Gampaha",
			},
		},
		"items": []any{"a", "b"},
		"empty": map[string]any{},
	}

	tests := []struct {
		name    string
		pointer string
		want    any
		wantOK  bool
	}{
		{"top-level", "/district", "Colombo", true},
		{"nested", "/importer/address/district", "Gampaha", true},
		{"missing key", "/nope", nil, false},
		{"missing nested key", "/importer/address/nope", nil, false},
		{"through a non-object leaf", "/district/nope", nil, false},
		{"through an array", "/items/0", nil, false},
		{"array itself", "/items", []any{"a", "b"}, true},
		{"empty intermediate object, nothing to walk into", "/empty/x", nil, false},
		{"malformed pointer, no leading slash", "district", nil, false},
		{"root pointer", "", nil, false},
		{"invalid escape sequence", "/a~2b", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Get(doc, tt.pointer)
			if ok != tt.wantOK {
				t.Fatalf("Get(%q) ok = %v, want %v", tt.pointer, ok, tt.wantOK)
			}
			if ok && !equalAny(got, tt.want) {
				t.Errorf("Get(%q) = %#v, want %#v", tt.pointer, got, tt.want)
			}
		})
	}
}

func TestGet_EscapedSegment(t *testing.T) {
	doc := map[string]any{
		"a/b": "slash-key",
		"c~d": "tilde-key",
	}
	if got, ok := Get(doc, "/a~1b"); !ok || got != "slash-key" {
		t.Errorf("Get(/a~1b) = %v, %v, want \"slash-key\", true", got, ok)
	}
	if got, ok := Get(doc, "/c~0d"); !ok || got != "tilde-key" {
		t.Errorf("Get(/c~0d) = %v, %v, want \"tilde-key\", true", got, ok)
	}
}

func TestSet_CreatesIntermediateObjects(t *testing.T) {
	doc := map[string]any{}
	if ok := Set(doc, "/importer/address/district", "Colombo"); !ok {
		t.Fatal("Set() = false, want true")
	}

	got, ok := Get(doc, "/importer/address/district")
	if !ok || got != "Colombo" {
		t.Errorf("round-trip Get() = %v, %v, want \"Colombo\", true", got, ok)
	}
}

func TestSet_TopLevel(t *testing.T) {
	doc := map[string]any{}
	if ok := Set(doc, "/district", "Colombo"); !ok {
		t.Fatal("Set() = false, want true")
	}
	if doc["district"] != "Colombo" {
		t.Errorf("doc[district] = %v, want Colombo", doc["district"])
	}
}

func TestSet_OverwritesExistingLeaf(t *testing.T) {
	doc := map[string]any{"district": "Old"}
	if ok := Set(doc, "/district", "New"); !ok {
		t.Fatal("Set() = false, want true")
	}
	if doc["district"] != "New" {
		t.Errorf("doc[district] = %v, want New", doc["district"])
	}
}

func TestSet_MergesAlongsideExistingSiblings(t *testing.T) {
	doc := map[string]any{
		"importer": map[string]any{
			"name": "ACME",
		},
	}
	if ok := Set(doc, "/importer/address", "somewhere"); !ok {
		t.Fatal("Set() = false, want true")
	}

	importer, ok := doc["importer"].(map[string]any)
	if !ok {
		t.Fatalf("doc[importer] is not a map: %#v", doc["importer"])
	}
	if importer["name"] != "ACME" {
		t.Errorf("existing sibling key was clobbered: %#v", importer)
	}
	if importer["address"] != "somewhere" {
		t.Errorf("importer[address] = %v, want somewhere", importer["address"])
	}
}

func TestSet_FailsThroughNonObjectIntermediate(t *testing.T) {
	doc := map[string]any{"district": "Colombo"}
	if ok := Set(doc, "/district/nope", "x"); ok {
		t.Error("Set() through a scalar intermediate = true, want false")
	}
	if doc["district"] != "Colombo" {
		t.Errorf("doc mutated on failed Set: %#v", doc)
	}
}

func TestSet_FailsThroughArrayIntermediate(t *testing.T) {
	doc := map[string]any{"items": []any{"a", "b"}}
	if ok := Set(doc, "/items/0", "x"); ok {
		t.Error("Set() through an array intermediate = true, want false")
	}
}

func TestSet_MalformedOrRootPointer(t *testing.T) {
	doc := map[string]any{}
	if ok := Set(doc, "no-slash", "x"); ok {
		t.Error("Set(no-slash) = true, want false")
	}
	if ok := Set(doc, "", "x"); ok {
		t.Error("Set(root) = true, want false")
	}
	if len(doc) != 0 {
		t.Errorf("doc mutated on failed Set: %#v", doc)
	}
}

func equalAny(a, b any) bool {
	as, aok := a.([]any)
	bs, bok := b.([]any)
	if aok || bok {
		if !aok || !bok || len(as) != len(bs) {
			return false
		}
		for i := range as {
			if as[i] != bs[i] {
				return false
			}
		}
		return true
	}
	return a == b
}
