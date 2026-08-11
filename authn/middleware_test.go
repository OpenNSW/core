// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package authn

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// MockUserService is a mock implementation of UserProfileService for testing.
type MockUserService struct {
	getOrCreateID    string
	getOrCreateErr   error
	getOrCreateCalls int
	lastPrincipal    *UserPrincipal
}

func (m *MockUserService) GetOrCreateUser(ctx context.Context, principal *UserPrincipal) (string, error) {
	m.getOrCreateCalls++
	m.lastPrincipal = principal
	if m.getOrCreateErr != nil {
		return "", m.getOrCreateErr
	}
	if m.getOrCreateID != "" {
		return m.getOrCreateID, nil
	}
	return "mock-user-id", nil
}

// TestAuthMiddleware_NoToken tests middleware when no auth header provided
func TestAuthMiddleware_NoToken(t *testing.T) {
	// Create a test handler that checks for auth context
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authCtx := GetAuthContext(r.Context())
		if authCtx != nil {
			t.Error("expected no auth context when no token provided")
		}
		w.WriteHeader(http.StatusOK)
	})

	// Create middleware with nil dependencies
	// This is acceptable for this test case since no token means the middleware
	// won't attempt to use user service or TokenExtractor
	middleware := Middleware(nil, nil)
	handlerWithMiddleware := middleware(testHandler)

	// Make a test request without Authorization header
	req := httptest.NewRequest("GET", "http://example.com/test", nil)
	recorder := httptest.NewRecorder()

	handlerWithMiddleware.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", recorder.Code)
	}
}

// TestAuthMiddleware_UninitializedTokenExtractor tests middleware returns 500 when tokenExtractor is nil
func TestAuthMiddleware_UninitializedDependencies(t *testing.T) {
	testHandlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testHandlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	// With nil tokenExtractor, middleware should return 500
	middleware := Middleware(nil, nil)
	handlerWithMiddleware := middleware(testHandler)

	req := httptest.NewRequest("GET", "http://example.com/test", nil)
	req.Header.Set("Authorization", "Bearer invalid.jwt.token")
	recorder := httptest.NewRecorder()

	handlerWithMiddleware.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", recorder.Code)
	}
	if testHandlerCalled {
		t.Error("expected handler not to be called when tokenExtractor is nil")
	}
}

// TestAuthMiddleware_InvalidToken tests middleware returns 401 for invalid auth token
func TestAuthMiddleware_InvalidToken(t *testing.T) {
	testHandlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testHandlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	tokenExtractor, _, cleanup := newTokenExtractor(t)
	defer cleanup()
	// Use mock user service to ensure this test validates token behavior
	mockUserService := &MockUserService{}
	middleware := Middleware(mockUserService, tokenExtractor)
	handlerWithMiddleware := middleware(testHandler)

	req := httptest.NewRequest("GET", "http://example.com/test", nil)
	req.Header.Set("Authorization", "Bearer invalid.jwt.token")
	recorder := httptest.NewRecorder()

	handlerWithMiddleware.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", recorder.Code)
	}
	if testHandlerCalled {
		t.Error("expected handler not to be called for invalid token")
	}
}

func TestBuildAuthContext_UserPrincipalOnly(t *testing.T) {
	principal := &Principal{
		Type: UserPrincipalType,
		UserPrincipal: &UserPrincipal{
			Subject: testUserID,
			Roles:   []string{"exporter"},
			ExtraClaims: ExtraClaims{
				"email":        testEmail,
				"phone_number": testPhone,
				"ouId":         testOUID,
				"ouHandle":     testOUHandle,
			},
		},
	}

	authCtx := buildAuthContext(principal)

	if authCtx.User == nil || authCtx.User.IDPUserID != testUserID {
		t.Fatalf("expected idp user id to be set from user principal")
	}
	if authCtx.User.ID != "" {
		t.Fatalf("expected persisted user id to be empty for user principal only")
	}
	if authCtx.User.ExtraClaims.String("email") != testEmail {
		t.Fatalf("expected email to be set, got %s", authCtx.User.ExtraClaims.String("email"))
	}
	if authCtx.User.ExtraClaims.String("phone_number") != testPhone {
		t.Fatalf("expected phone number to be set, got %s", authCtx.User.ExtraClaims.String("phone_number"))
	}
	if authCtx.User.ExtraClaims.String("ouId") != testOUID {
		t.Fatalf("expected ou id to be set, got %s", authCtx.User.ExtraClaims.String("ouId"))
	}
	if authCtx.User.ExtraClaims.String("ouHandle") != testOUHandle {
		t.Fatalf("expected ou handle to be set, got %s", authCtx.User.ExtraClaims.String("ouHandle"))
	}
	if authCtx.Client != nil {
		t.Fatalf("expected client id to be nil when client principal is absent")
	}
	if len(authCtx.User.Roles) != 1 || authCtx.User.Roles[0] != "exporter" {
		t.Fatalf("expected roles to be set, got %v", authCtx.User.Roles)
	}
}

func TestBuildAuthContext_ClientPrincipalOnly(t *testing.T) {
	principal := &Principal{
		Type:            ClientPrincipalType,
		ClientPrincipal: &ClientPrincipal{ClientID: "CLIENT-001"},
	}

	authCtx := buildAuthContext(principal)

	if authCtx.Client == nil || authCtx.Client.ClientID != "CLIENT-001" {
		t.Fatalf("expected client id to be set from client principal")
	}
	if authCtx.User != nil {
		t.Fatalf("expected user fields to be nil when user principal is absent")
	}
}

func TestBuildAuthContext_NilPrincipal(t *testing.T) {
	authCtx := buildAuthContext(nil)
	if authCtx == nil {
		t.Fatalf("expected auth context")
		return
	}
	if authCtx.User != nil || authCtx.Client != nil {
		t.Fatalf("expected empty auth context, got %+v", authCtx)
	}
}

func TestBuildAuthContext_UnknownType(t *testing.T) {
	principal := &Principal{Type: PrincipalType("unknown")}
	authCtx := buildAuthContext(principal)
	if authCtx == nil {
		t.Fatalf("expected auth context")
		return
	}
	if authCtx.User != nil || authCtx.Client != nil {
		t.Fatalf("expected empty auth context, got %+v", authCtx)
	}
}

func TestAuthMiddleware_ValidClientCredentialsToken(t *testing.T) {
	tokenExtractor, privateKey, cleanup := newTokenExtractor(t)
	defer cleanup()

	signedToken := newClientToken(t, privateKey)

	testHandlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testHandlerCalled = true
		authCtx := GetAuthContext(r.Context())
		if authCtx == nil {
			t.Fatalf("expected auth context")
			return
		}
		if authCtx.Client == nil || authCtx.Client.ClientID != "TRADER_PORTAL_APP" {
			t.Fatalf("expected client id TRADER_PORTAL_APP, got %v", authCtx.Client)
		}
		if authCtx.User != nil {
			t.Fatalf("expected user id to be nil for client principal")
		}
		w.WriteHeader(http.StatusOK)
	})

	handlerWithMiddleware := Middleware(&MockUserService{}, tokenExtractor)(testHandler)
	req := httptest.NewRequest(http.MethodGet, "http://example.com/test", nil)
	req.Header.Set("Authorization", "Bearer "+signedToken)
	recorder := httptest.NewRecorder()

	handlerWithMiddleware.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	if !testHandlerCalled {
		t.Fatalf("expected handler to be called for valid token")
	}
}

func TestAuthMiddleware_UserPrincipal_NoUserProfileService(t *testing.T) {
	tokenExtractor, privateKey, cleanup := newTokenExtractor(t)
	defer cleanup()

	signedToken := newUserToken(t, privateKey)

	testHandlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testHandlerCalled = true
		authCtx := GetAuthContext(r.Context())
		if authCtx == nil || authCtx.User == nil {
			t.Fatalf("expected auth context with user")
		}
		if authCtx.User.IDPUserID != testUserID {
			t.Fatalf("expected idp user id %s, got %s", testUserID, authCtx.User.IDPUserID)
		}
		if authCtx.User.ID != "" {
			t.Fatalf("expected persisted user id to be empty, got %s", authCtx.User.ID)
		}
		if len(authCtx.User.Roles) != 1 || authCtx.User.Roles[0] != "exporter" {
			t.Fatalf("expected roles [exporter], got %v", authCtx.User.Roles)
		}
		// email/phone_number/ouId/ouHandle are no longer part of the fixed
		// schema. This extractor declared no extra claims, so none of them
		// should be surfaced even though the signed token carries them.
		if len(authCtx.User.ExtraClaims) != 0 {
			t.Fatalf("expected no extra claims to be extracted, got %#v", authCtx.User.ExtraClaims)
		}
		w.WriteHeader(http.StatusOK)
	})

	handlerWithMiddleware := Middleware(nil, tokenExtractor)(testHandler)
	req := httptest.NewRequest(http.MethodGet, "http://example.com/test", nil)
	req.Header.Set("Authorization", "Bearer "+signedToken)
	recorder := httptest.NewRecorder()

	handlerWithMiddleware.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	if !testHandlerCalled {
		t.Fatalf("expected handler to be called for valid token")
	}
}

func TestAuthMiddleware_UserPrincipal_GetOrCreateUser(t *testing.T) {
	tokenExtractor, privateKey, cleanup := newTokenExtractor(t)
	defer cleanup()

	signedToken := newUserToken(t, privateKey)
	mockUserService := &MockUserService{getOrCreateID: "existing-user-id"}

	testHandlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testHandlerCalled = true
		authCtx := GetAuthContext(r.Context())
		if authCtx == nil || authCtx.User == nil {
			t.Fatalf("expected auth context with user")
		}
		if authCtx.User.ID != "existing-user-id" {
			t.Fatalf("expected persisted user id existing-user-id, got %s", authCtx.User.ID)
		}
		w.WriteHeader(http.StatusOK)
	})

	handlerWithMiddleware := Middleware(mockUserService, tokenExtractor)(testHandler)
	req := httptest.NewRequest(http.MethodGet, "http://example.com/test", nil)
	req.Header.Set("Authorization", "Bearer "+signedToken)
	recorder := httptest.NewRecorder()

	handlerWithMiddleware.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	if !testHandlerCalled {
		t.Fatalf("expected handler to be called for valid token")
	}
	if mockUserService.getOrCreateCalls != 1 {
		t.Fatalf("expected GetOrCreateUser to be called once, got %d", mockUserService.getOrCreateCalls)
	}
	if mockUserService.lastPrincipal.Subject != testUserID {
		t.Fatalf("expected GetOrCreateUser to be called with %s, got %s", testUserID, mockUserService.lastPrincipal.Subject)
	}
}

func TestAuthMiddleware_UserPrincipal_CreatesUser(t *testing.T) {
	// This extractor declares email/phone_number/ouId/ouHandle as optional
	// extra claims, so the principal's ExtraClaims are populated from the
	// signed token as expected.
	tokenExtractor, privateKey, cleanup := newTokenExtractorWithOptions(t,
		WithUserClaims(ClaimSpec{Optional: []string{"email", "phone_number", "ouId", "ouHandle"}}))
	defer cleanup()

	signedToken := newUserToken(t, privateKey)
	mockUserService := &MockUserService{getOrCreateID: "created-user-id"}

	testHandlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testHandlerCalled = true
		authCtx := GetAuthContext(r.Context())
		if authCtx == nil || authCtx.User == nil {
			t.Fatalf("expected auth context with user")
		}
		if authCtx.User.ID != "created-user-id" {
			t.Fatalf("expected persisted user id created-user-id, got %s", authCtx.User.ID)
		}
		w.WriteHeader(http.StatusOK)
	})

	handlerWithMiddleware := Middleware(mockUserService, tokenExtractor)(testHandler)
	req := httptest.NewRequest(http.MethodGet, "http://example.com/test", nil)
	req.Header.Set("Authorization", "Bearer "+signedToken)
	recorder := httptest.NewRecorder()

	handlerWithMiddleware.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	if !testHandlerCalled {
		t.Fatalf("expected handler to be called for valid token")
	}
	if mockUserService.getOrCreateCalls != 1 {
		t.Fatalf("expected GetOrCreateUser to be called once, got %d", mockUserService.getOrCreateCalls)
	}
	got := mockUserService.lastPrincipal
	if got.Subject != testUserID ||
		got.ExtraClaims.String("email") != testEmail ||
		got.ExtraClaims.String("phone_number") != testPhone ||
		got.ExtraClaims.String("ouId") != testOUID ||
		got.ExtraClaims.String("ouHandle") != testOUHandle {
		t.Fatalf("unexpected GetOrCreateUser principal: %+v", got)
	}
}

// TestAuthMiddleware_UserPrincipal_GetOrCreateUser_ExtraClaimsNotDeclared is a
// regression test for a real footgun: a UserProfileService implementation that
// depends on org/OU-like extra claims silently receives an empty ExtraClaims
// map if the consumer never declared them via WithUserClaims — even though the
// signed JWT carries them. This documents that behavior explicitly as
// intentional, rather than leaving it as an unwritten trap.
func TestAuthMiddleware_UserPrincipal_GetOrCreateUser_ExtraClaimsNotDeclared(t *testing.T) {
	tokenExtractor, privateKey, cleanup := newTokenExtractor(t)
	defer cleanup()

	signedToken := newUserToken(t, privateKey)
	mockUserService := &MockUserService{getOrCreateID: "created-user-id"}

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handlerWithMiddleware := Middleware(mockUserService, tokenExtractor)(testHandler)
	req := httptest.NewRequest(http.MethodGet, "http://example.com/test", nil)
	req.Header.Set("Authorization", "Bearer "+signedToken)
	recorder := httptest.NewRecorder()

	handlerWithMiddleware.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	if len(mockUserService.lastPrincipal.ExtraClaims) != 0 {
		t.Fatalf("expected empty extra claims when none were declared, got %#v", mockUserService.lastPrincipal.ExtraClaims)
	}
}

func TestAuthMiddleware_UserPrincipal_GetOrCreateUserError(t *testing.T) {
	tokenExtractor, privateKey, cleanup := newTokenExtractor(t)
	defer cleanup()

	signedToken := newUserToken(t, privateKey)
	mockUserService := &MockUserService{getOrCreateErr: errors.New("db down")}

	testHandlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testHandlerCalled = true
		authCtx := GetAuthContext(r.Context())
		if authCtx == nil || authCtx.User == nil {
			t.Fatalf("expected auth context with user")
		}
		if authCtx.User.ID != "" {
			t.Fatalf("expected persisted user id to be empty, got %s", authCtx.User.ID)
		}
		w.WriteHeader(http.StatusOK)
	})

	handlerWithMiddleware := Middleware(mockUserService, tokenExtractor)(testHandler)
	req := httptest.NewRequest(http.MethodGet, "http://example.com/test", nil)
	req.Header.Set("Authorization", "Bearer "+signedToken)
	recorder := httptest.NewRecorder()

	handlerWithMiddleware.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	if !testHandlerCalled {
		t.Fatalf("expected handler to be called for valid token")
	}
	if mockUserService.getOrCreateCalls != 1 {
		t.Fatalf("expected GetOrCreateUser to be called once, got %d", mockUserService.getOrCreateCalls)
	}
}

func TestRequireAuth_UnauthenticatedRequest(t *testing.T) {
	handlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	tokenExtractor, _, cleanup := newTokenExtractor(t)
	defer cleanup()
	protected := RequireAuth(&MockUserService{}, tokenExtractor)(testHandler)
	req := httptest.NewRequest(http.MethodGet, "http://example.com/protected", nil)
	recorder := httptest.NewRecorder()

	protected.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", recorder.Code)
	}
	if handlerCalled {
		t.Fatalf("expected protected handler not to be called")
	}
}

func TestRequireAuth_ValidClientCredentialsToken(t *testing.T) {
	tokenExtractor, privateKey, cleanup := newTokenExtractor(t)
	defer cleanup()

	signedToken := newClientToken(t, privateKey)

	handlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	protected := RequireAuth(&MockUserService{}, tokenExtractor)(testHandler)
	req := httptest.NewRequest(http.MethodGet, "http://example.com/protected", nil)
	req.Header.Set("Authorization", "Bearer "+signedToken)
	recorder := httptest.NewRecorder()

	protected.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	if !handlerCalled {
		t.Fatalf("expected protected handler to be called")
	}
}
