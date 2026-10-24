// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package httputil

import (
	"encoding/json"
	"testing"
)

func TestInterpolate(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		params map[string]string
		want   string
	}{
		{"no params", "Hello {{name}}", nil, "Hello {{name}}"},
		{"one param", "Hello {{name}}", map[string]string{"name": "Ana"}, "Hello Ana"},
		{"repeated param", "{{a}} and {{a}}", map[string]string{"a": "x"}, "x and x"},
		{"unknown placeholder kept", "{{a}} {{b}}", map[string]string{"a": "x"}, "x {{b}}"},
		{"unclosed placeholder kept", "{{a}} {{b", map[string]string{"a": "x"}, "x {{b"},
		{"value not expanded again", "{{a}} {{b}}", map[string]string{"a": "{{b}}", "b": "y"}, "{{b}} y"},
		{"no placeholders", "plain", map[string]string{"a": "x"}, "plain"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := interpolate(tt.s, tt.params); got != tt.want {
				t.Fatalf("interpolate(%q) = %q, want %q", tt.s, got, tt.want)
			}
		})
	}
}

func TestMessage_WithParamsKeepsOnlyUsedParams(t *testing.T) {
	m := Message{Key: "k", DefaultValue: "No member has the ID {{memberId}}"}

	got := m.WithParams(map[string]string{"memberId": "m-42", "other": "x"})

	if len(got.Params) != 1 || got.Params["memberId"] != "m-42" {
		t.Fatalf("unexpected params: %v", got.Params)
	}
	if m.Params != nil {
		t.Fatalf("WithParams modified the original: %v", m.Params)
	}
	if none := (Message{Key: "k", DefaultValue: "Member not found"}).WithParams(map[string]string{"memberId": "m-42"}); none.Params != nil {
		t.Fatalf("expected nil params for a text without placeholders, got %v", none.Params)
	}
}

func TestMessage_MarshalJSONInterpolatesDefaultValueAndKeepsParams(t *testing.T) {
	m := Message{Key: "pb.member.not_found_description", DefaultValue: "No member has the ID {{memberId}}"}.
		WithParams(map[string]string{"memberId": "m-42"})

	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"key":"pb.member.not_found_description","defaultValue":"No member has the ID m-42","params":{"memberId":"m-42"}}`
	if string(b) != want {
		t.Fatalf("got %s, want %s", b, want)
	}
}

func TestMessage_MarshalJSONOmitsEmptyParams(t *testing.T) {
	b, err := json.Marshal(Message{Key: "k", DefaultValue: "Member not found"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"key":"k","defaultValue":"Member not found"}`; string(b) != want {
		t.Fatalf("got %s, want %s", b, want)
	}
}
