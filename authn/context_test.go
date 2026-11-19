// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package authn

import (
	"context"
	"slices"
	"testing"
)

// TestGetAuthContextFromRequest tests context retrieval.
func TestGetAuthContext_FromRequest(t *testing.T) {
	authCtx := &AuthContext{Principal: Principal{
		Subject:  testUserID,
		ClientID: testClientID,
		Roles:    []string{"exporter"},
		ExtraClaims: ExtraClaims{
			"email":    testEmail,
			"ouId":     testOUID,
			"ouHandle": testOUHandle,
		},
	}}
	ctx := context.WithValue(context.Background(), AuthContextKey, authCtx)

	retrieved := GetAuthContext(ctx)
	if retrieved == nil {
		t.Fatal("expected to retrieve auth context")
	}
	if retrieved.Subject() != testUserID || retrieved.ClientID() != testClientID {
		t.Errorf("got principal %+v", retrieved.Principal)
	}
}

// TestGetAuthContextFromRequest_NoContext tests when context not present.
func TestGetAuthContext_NoContext(t *testing.T) {
	ctx := context.Background()

	retrieved := GetAuthContext(ctx)
	if retrieved != nil {
		t.Error("expected nil auth context")
	}
}

func TestGetAuthContext_WrongType(t *testing.T) {
	ctx := context.WithValue(context.Background(), AuthContextKey, "not-auth-context")

	retrieved := GetAuthContext(ctx)
	if retrieved != nil {
		t.Error("expected nil auth context for wrong type")
	}
}

// TestAuthContext_Accessors locks in that every accessor reads straight
// through to the Principal and is nil-safe.
func TestAuthContext_Accessors(t *testing.T) {
	authCtx := &AuthContext{Principal: Principal{
		Subject:  testUserID,
		ClientID: testClientID,
		Roles:    []string{"exporter"},
		Scopes:   []string{"nsw:task:read"},
		Email:    testEmail,
		OUID:     testOUID,
		OUHandle: testOUHandle,
	}}

	if got := authCtx.Email(); got != testEmail {
		t.Errorf("Email() = %q, want %q", got, testEmail)
	}
	if got := authCtx.OUID(); got != testOUID {
		t.Errorf("OUID() = %q, want %q", got, testOUID)
	}
	if got := authCtx.OUHandle(); got != testOUHandle {
		t.Errorf("OUHandle() = %q, want %q", got, testOUHandle)
	}
	if got := authCtx.Subject(); got != testUserID {
		t.Errorf("Subject() = %q, want %q", got, testUserID)
	}
	if got := authCtx.ClientID(); got != testClientID {
		t.Errorf("ClientID() = %q, want %q", got, testClientID)
	}
	if got := authCtx.Roles(); !slices.Equal(got, []string{"exporter"}) {
		t.Errorf("Roles() = %v, want [exporter]", got)
	}
	if got := authCtx.Scopes(); !slices.Equal(got, []string{"nsw:task:read"}) {
		t.Errorf("Scopes() = %v, want [nsw:task:read]", got)
	}

	var nilCtx *AuthContext
	if nilCtx.Subject() != "" || nilCtx.ClientID() != "" || nilCtx.Roles() != nil || nilCtx.Scopes() != nil ||
		nilCtx.Email() != "" || nilCtx.OUID() != "" || nilCtx.OUHandle() != "" {
		t.Error("expected zero values from a nil AuthContext")
	}
}

// TestAuthContext_ExtraClaims completes the accessor seam: consumers should be
// able to read extra claims without nil-checking two levels.
func TestAuthContext_ExtraClaims(t *testing.T) {
	tests := []struct {
		name string
		ctx  *AuthContext
		want string
	}{
		{"nil receiver", nil, ""},
		{"empty context", &AuthContext{}, ""},
		{"principal with claims", &AuthContext{Principal: Principal{ExtraClaims: ExtraClaims{"email": testEmail}}}, testEmail},
		{"principal with nil claims", &AuthContext{Principal: Principal{Subject: testUserID}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Must not panic on any of these, including the nil receiver.
			if got := tt.ctx.ExtraClaims().String("email"); got != tt.want {
				t.Fatalf("ExtraClaims().String(email) = %q, want %q", got, tt.want)
			}
			if got := tt.ctx.ExtraClaims().Strings("groups"); got != nil {
				t.Fatalf("ExtraClaims().Strings(groups) = %#v, want nil", got)
			}
		})
	}
}
