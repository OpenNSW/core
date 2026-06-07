package authn

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// MockUserService is a mock implementation of UserProfileService for testing.
type MockUserService struct {
	getOrCreateID    *string
	getOrCreateErr   error
	getOrCreateCalls int
	lastArgs         getOrCreateArgs
}

type getOrCreateArgs struct {
	idpUserID string
	email     string
	phone     string
	ouID      string
	ouHandle  string
}

func (m *MockUserService) GetOrCreateUser(idpUserId, email, phone, ouID, ouHandle string) (*string, error) {
	m.getOrCreateCalls++
	m.lastArgs = getOrCreateArgs{
		idpUserID: idpUserId,
		email:     email,
		phone:     phone,
		ouID:      ouID,
		ouHandle:  ouHandle,
	}
	if m.getOrCreateErr != nil {
		return nil, m.getOrCreateErr
	}
	if m.getOrCreateID != nil {
		return m.getOrCreateID, nil
	}
	userID := "mock-user-id"
	return &userID, nil
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
			UserID:      testUserID,
			Email:       testEmail,
			PhoneNumber: strPtr(testPhone),
			OUID:        testOUID,
			OUHandle:    testOUHandle,
			Roles:       []string{"exporter"},
		},
	}

	authCtx := buildAuthContext(principal)

	if authCtx.User == nil || authCtx.User.IDPUserID != testUserID {
		t.Fatalf("expected idp user id to be set from user principal")
	}
	if authCtx.User.ID != "" {
		t.Fatalf("expected persisted user id to be empty for user principal only")
	}
	if authCtx.User.Email != testEmail {
		t.Fatalf("expected email to be set, got %s", authCtx.User.Email)
	}
	if authCtx.User.PhoneNumber != testPhone {
		t.Fatalf("expected phone number to be set, got %s", authCtx.User.PhoneNumber)
	}
	if authCtx.User.OUID != testOUID {
		t.Fatalf("expected ou id to be set, got %s", authCtx.User.OUID)
	}
	if authCtx.User.OUHandle != testOUHandle {
		t.Fatalf("expected ou handle to be set, got %s", authCtx.User.OUHandle)
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
		if authCtx.User.Email != testEmail || authCtx.User.PhoneNumber != testPhone || authCtx.User.OUID != testOUID || authCtx.User.OUHandle != testOUHandle {
			t.Fatalf("unexpected user details: %+v", authCtx.User)
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
	existingID := "existing-user-id"
	mockUserService := &MockUserService{getOrCreateID: &existingID}

	testHandlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testHandlerCalled = true
		authCtx := GetAuthContext(r.Context())
		if authCtx == nil || authCtx.User == nil {
			t.Fatalf("expected auth context with user")
		}
		if authCtx.User.ID != existingID {
			t.Fatalf("expected persisted user id %s, got %s", existingID, authCtx.User.ID)
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
	if mockUserService.lastArgs.idpUserID != testUserID {
		t.Fatalf("expected GetOrCreateUser to be called with %s, got %s", testUserID, mockUserService.lastArgs.idpUserID)
	}
}

func TestAuthMiddleware_UserPrincipal_CreatesUser(t *testing.T) {
	tokenExtractor, privateKey, cleanup := newTokenExtractor(t)
	defer cleanup()

	signedToken := newUserToken(t, privateKey)
	createdID := "created-user-id"
	mockUserService := &MockUserService{getOrCreateID: &createdID}

	testHandlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testHandlerCalled = true
		authCtx := GetAuthContext(r.Context())
		if authCtx == nil || authCtx.User == nil {
			t.Fatalf("expected auth context with user")
		}
		if authCtx.User.ID != createdID {
			t.Fatalf("expected persisted user id %s, got %s", createdID, authCtx.User.ID)
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
	if mockUserService.lastArgs.idpUserID != testUserID ||
		mockUserService.lastArgs.email != testEmail ||
		mockUserService.lastArgs.phone != testPhone ||
		mockUserService.lastArgs.ouID != testOUID ||
		mockUserService.lastArgs.ouHandle != testOUHandle {
		t.Fatalf("unexpected GetOrCreateUser args: %+v", mockUserService.lastArgs)
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
