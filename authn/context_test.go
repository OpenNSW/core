package auth

import (
	"context"
	"testing"
)

// TestGetAuthContextFromRequest tests context retrieval.
func TestGetAuthContext_FromRequest(t *testing.T) {
	uc := &UserContext{
		ID:        "persisted-user-id",
		IDPUserID: testUserID,
		Email:     testEmail,
		OUID:      testOUID,
		OUHandle:  testOUHandle,
		Roles:     []string{"exporter"},
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
		ID:          "persisted-user-id",
		IDPUserID:   testUserID,
		Email:       testEmail,
		PhoneNumber: testPhone,
		OUID:        testOUID,
		OUHandle:    testOUHandle,
		Roles:       []string{"exporter"},
	}

	if uc.ID != "persisted-user-id" {
		t.Errorf("got user id %s, want persisted-user-id", uc.ID)
	}
	if uc.IDPUserID != testUserID {
		t.Errorf("got idp user id %s, want %s", uc.IDPUserID, testUserID)
	}
	if uc.Email != testEmail {
		t.Errorf("got email %s, want %s", uc.Email, testEmail)
	}
	if uc.PhoneNumber != testPhone {
		t.Errorf("got phone number %s, want %s", uc.PhoneNumber, testPhone)
	}
	if uc.OUID != testOUID {
		t.Errorf("got ou id %s, want %s", uc.OUID, testOUID)
	}
	if uc.OUHandle != testOUHandle {
		t.Errorf("got ou handle %s, want %s", uc.OUHandle, testOUHandle)
	}
	if len(uc.Roles) != 1 || uc.Roles[0] != "exporter" {
		t.Errorf("got roles %v, want [exporter]", uc.Roles)
	}
}
