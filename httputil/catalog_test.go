// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package httputil

import (
	"net/http"
	"strings"
	"testing"
)

func TestCatalog_ListsEveryKeyWithItsDefault(t *testing.T) {
	got, err := Catalog(errMemberNotFound, errInvalidRequest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := map[string]string{
		"sys.internal":                    ErrInternal.Message.DefaultValue,
		"sys.internal_description":        ErrInternal.Description.DefaultValue,
		"pb.member.not_found":             "Member not found",
		"pb.member.not_found_description": "No member has the ID {{memberId}}",
		"pb.request.invalid":              "Invalid request",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("catalog[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestCatalog_AllowsTheSameErrorListedTwice(t *testing.T) {
	if _, err := Catalog(errMemberNotFound, errMemberNotFound, ErrInternal); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCatalog_AllowsASharedKeyWithTheSameDefault(t *testing.T) {
	other := errInvalidRequest
	other.Code = "PB-1002"
	if _, err := Catalog(errInvalidRequest, other); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCatalog_Rejects(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(e *ErrorDef)
		wantErr string
	}{
		{"duplicate code", func(e *ErrorDef) { e.Code = errMemberNotFound.Code }, "declared twice"},
		{"conflicting key default", func(e *ErrorDef) { e.Message.Key = errMemberNotFound.Message.Key }, "elsewhere"},
		{"malformed code", func(e *ErrorDef) { e.Code = "pb-12" }, "not an upper-case prefix"},
		{"missing category", func(e *ErrorDef) { e.Category = Category{} }, "not a 4xx or 5xx"},
		{"non-error category status", func(e *ErrorDef) { e.Category = NewCategory("ok", http.StatusOK) }, "not a 4xx or 5xx"},
		{"missing message key", func(e *ErrorDef) { e.Message.Key = "" }, "message has no key"},
		{"description without key", func(e *ErrorDef) { e.Description = Message{DefaultValue: "x"} }, "description has no key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := errInvalidRequest
			tt.mutate(&e)
			_, err := Catalog(errMemberNotFound, e)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected an error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}
