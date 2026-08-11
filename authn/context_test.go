// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package authn

import (
	"context"
	"testing"
)

// TestGetAuthContextFromRequest tests context retrieval.
func TestGetAuthContext_FromRequest(t *testing.T) {
	uc := &UserContext{
		ID:        "persisted-user-id",
		IDPUserID: testUserID,
		Roles:     []string{"exporter"},
		ExtraClaims: ExtraClaims{
			"email":    testEmail,
			"ouId":     testOUID,
			"ouHandle": testOUHandle,
		},
	}
	authCtx := &AuthContext{User: uc}
	ctx := context.WithValue(context.Background(), AuthContextKey, authCtx)

	retrieved := GetAuthContext(ctx)
	if retrieved == nil {
		t.Error("expected to retrieve auth context")
		return
	}
	if retrieved.User == nil {
		t.Fatalf("expected user context to be set")
	}
	if retrieved.User.ID != "persisted-user-id" || retrieved.User.IDPUserID != testUserID {
		t.Errorf("got user context %v", retrieved.User)
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

// TestUserContext_JSONUnmarshaling tests UserContext structure.
func TestUserContext_Structure(t *testing.T) {
	uc := &UserContext{
		ID:        "persisted-user-id",
		IDPUserID: testUserID,
		Roles:     []string{"exporter"},
		ExtraClaims: ExtraClaims{
			"email":        testEmail,
			"phone_number": testPhone,
			"ouId":         testOUID,
			"ouHandle":     testOUHandle,
		},
	}

	if uc.ID != "persisted-user-id" {
		t.Errorf("got user id %s, want persisted-user-id", uc.ID)
	}
	if uc.IDPUserID != testUserID {
		t.Errorf("got idp user id %s, want %s", uc.IDPUserID, testUserID)
	}
	if uc.ExtraClaims.String("email") != testEmail {
		t.Errorf("got email %s, want %s", uc.ExtraClaims.String("email"), testEmail)
	}
	if uc.ExtraClaims.String("phone_number") != testPhone {
		t.Errorf("got phone number %s, want %s", uc.ExtraClaims.String("phone_number"), testPhone)
	}
	if uc.ExtraClaims.String("ouId") != testOUID {
		t.Errorf("got ou id %s, want %s", uc.ExtraClaims.String("ouId"), testOUID)
	}
	if uc.ExtraClaims.String("ouHandle") != testOUHandle {
		t.Errorf("got ou handle %s, want %s", uc.ExtraClaims.String("ouHandle"), testOUHandle)
	}
	if len(uc.Roles) != 1 || uc.Roles[0] != "exporter" {
		t.Errorf("got roles %v, want [exporter]", uc.Roles)
	}
}

// TestAuthContext_ExtraClaims completes the accessor seam: consumers should be
// able to read extra claims without branching on principal type or nil-checking
// two levels.
func TestAuthContext_ExtraClaims(t *testing.T) {
	tests := []struct {
		name string
		ctx  *AuthContext
		want string
	}{
		{"nil receiver", nil, ""},
		{"empty context", &AuthContext{}, ""},
		{"user principal", &AuthContext{User: &UserContext{ExtraClaims: ExtraClaims{"email": testEmail}}}, testEmail},
		{"client principal", &AuthContext{Client: &ClientContext{ExtraClaims: ExtraClaims{"email": "svc@example.com"}}}, "svc@example.com"},
		{"user with nil claims", &AuthContext{User: &UserContext{}}, ""},
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
