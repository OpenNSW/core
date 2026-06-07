package authn

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func TestTokenExtractor_ExtractPrincipalFromHeader(t *testing.T) {
	extractor, privateKey, cleanup := newTokenExtractor(t)
	defer cleanup()

	validUserToken := newUserToken(t, privateKey)
	validClientToken := newClientToken(t, privateKey)

	tests := []struct {
		name       string
		authHeader string
		want       string
		wantErr    bool
	}{
		{name: "valid user token", authHeader: "Bearer " + validUserToken, want: testUserID},
		{name: "valid client token", authHeader: "Bearer " + validClientToken, want: testClientID},
		{name: "missing header", authHeader: "", wantErr: true},
		{name: "invalid token", authHeader: "Bearer invalid.jwt.token", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			principal, err := extractor.ExtractPrincipalFromHeader(tt.authHeader)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ExtractPrincipalFromHeader() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			got := ""
			if principal != nil && principal.UserPrincipal != nil {
				got = principal.UserPrincipal.UserID
			}
			if principal != nil && principal.ClientPrincipal != nil {
				got = principal.ClientPrincipal.ClientID
			}
			if got != tt.want {
				t.Fatalf("unexpected principal id: got %q want %q", got, tt.want)
			}
		})
	}
}

func TestTokenExtractor_ExtractPrincipalFromHeader_InvalidHeaderFormats(t *testing.T) {
	extractor, _, cleanup := newTokenExtractor(t)
	defer cleanup()

	invalidHeaders := []string{
		"Bearer",
		"Bearer ",
		"Bearer\t",
		"Basic abc.def",
		"Token abc.def",
	}

	for _, header := range invalidHeaders {
		if _, err := extractor.ExtractPrincipalFromHeader(header); err == nil {
			t.Fatalf("expected error for header %q", header)
		}
	}
}

func TestTokenExtractor_ExtractPrincipalFromHeader_MissingClaims(t *testing.T) {
	extractor, privateKey, cleanup := newTokenExtractor(t)
	defer cleanup()

	baseClaims := func() jwt.MapClaims {
		claims := newBaseClaims(AuthorizationCodeGrant)
		claims["sub"] = testUserID
		claims["email"] = testEmail
		claims["phone_number"] = testPhone
		claims["ouId"] = testOUID
		claims["ouHandle"] = testOUHandle
		claims["roles"] = []string{"exporter"}
		return claims
	}

	tests := []struct {
		name      string
		mutate    func(jwt.MapClaims)
		errSubstr string
	}{
		{
			name: "missing exp",
			mutate: func(claims jwt.MapClaims) {
				delete(claims, "exp")
			},
			errSubstr: "jwt missing exp claim",
		},
		{
			name: "missing client_id",
			mutate: func(claims jwt.MapClaims) {
				delete(claims, "client_id")
			},
			errSubstr: "jwt missing client_id claim",
		},
		{
			name: "unexpected client_id",
			mutate: func(claims jwt.MapClaims) {
				claims["client_id"] = "OTHER"
			},
			errSubstr: "unexpected client_id claim",
		},
		{
			name: "unsupported grant_type",
			mutate: func(claims jwt.MapClaims) {
				claims["grant_type"] = "password"
			},
			errSubstr: "unsupported grant type",
		},
		{
			name: "missing sub",
			mutate: func(claims jwt.MapClaims) {
				delete(claims, "sub")
			},
			errSubstr: "jwt missing sub claim for user principal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := baseClaims()
			tt.mutate(claims)
			token := signToken(t, privateKey, claims)
			_, err := extractor.ExtractPrincipalFromHeader("Bearer " + token)
			if err == nil || !strings.Contains(err.Error(), tt.errSubstr) {
				t.Fatalf("expected error containing %q, got %v", tt.errSubstr, err)
			}
		})
	}
}

func TestUserPrincipalFromClaims(t *testing.T) {
	claims := &tokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: testUserID},
		Email:            strPtr(testEmail),
		PhoneNumber:      strPtr(testPhone),
		OUID:             strPtr(testOUID),
		OUHandle:         strPtr(testOUHandle),
		Roles:            []string{"exporter"},
	}

	principal, err := (&TokenExtractor{}).userPrincipalFromClaims(claims)
	if err != nil {
		t.Fatalf("userPrincipalFromClaims() error = %v", err)
	}
	if principal.UserID != testUserID || principal.Email != testEmail || principal.PhoneNumber == nil || *principal.PhoneNumber != testPhone {
		t.Fatalf("unexpected principal: %#v", principal)
	}
	if principal.OUID != testOUID || principal.OUHandle != testOUHandle || len(principal.Roles) != 1 || principal.Roles[0] != "exporter" {
		t.Fatalf("unexpected claims mapping: %#v", principal)
	}

	missingClaims := []struct {
		name   string
		claims *tokenClaims
	}{
		{name: "missing email", claims: &tokenClaims{RegisteredClaims: jwt.RegisteredClaims{Subject: testUserID}, OUID: strPtr(testOUID), OUHandle: strPtr(testOUHandle)}},
		{name: "missing ou id", claims: &tokenClaims{RegisteredClaims: jwt.RegisteredClaims{Subject: testUserID}, Email: strPtr(testEmail), OUHandle: strPtr(testOUHandle)}},
		{name: "missing ou handle", claims: &tokenClaims{RegisteredClaims: jwt.RegisteredClaims{Subject: testUserID}, Email: strPtr(testEmail), OUID: strPtr(testOUID)}},
	}

	for _, tt := range missingClaims {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := (&TokenExtractor{}).userPrincipalFromClaims(tt.claims); err == nil {
				t.Fatalf("expected error for %s", tt.name)
			}
		})
	}

	// Test optional phone_number field
	t.Run("optional phone_number", func(t *testing.T) {
		claims := &tokenClaims{
			RegisteredClaims: jwt.RegisteredClaims{Subject: testUserID},
			Email:            strPtr(testEmail),
			OUID:             strPtr(testOUID),
			OUHandle:         strPtr(testOUHandle),
		}
		principal, err := (&TokenExtractor{}).userPrincipalFromClaims(claims)
		if err != nil {
			t.Fatalf("userPrincipalFromClaims() error = %v", err)
		}
		if principal.PhoneNumber != nil {
			t.Fatalf("expected nil phone_number, got %v", *principal.PhoneNumber)
		}
	})
}

func TestTokenExtractor_JWKSIsCached(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate rsa key: %v", err)
	}

	var fetchCount int32
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fetchCount, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kid": "cache-kid",
				"kty": "RSA",
				"alg": "RS256",
				"use": "sig",
				"n":   base64.RawURLEncoding.EncodeToString(privateKey.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(privateKey.PublicKey.E)).Bytes()),
			}},
		})
	}))
	defer jwksServer.Close()

	claims := newBaseClaims(AuthorizationCodeGrant)
	claims["sub"] = testUserID
	claims["email"] = testEmail
	claims["phone_number"] = testPhone
	claims["ouId"] = testOUID
	claims["ouHandle"] = testOUHandle
	claims["roles"] = []string{"exporter"}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "cache-kid"
	signedToken, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	extractor, err := NewTokenExtractor(jwksServer.URL, testIssuer, testClientID, []string{testClientID})
	if err != nil {
		t.Fatalf("failed to create token extractor: %v", err)
	}

	if _, err := extractor.ExtractPrincipalFromHeader("Bearer " + signedToken); err != nil {
		t.Fatalf("first extract failed: %v", err)
	}
	if _, err := extractor.ExtractPrincipalFromHeader("Bearer " + signedToken); err != nil {
		t.Fatalf("second extract failed: %v", err)
	}

	if got := atomic.LoadInt32(&fetchCount); got != 1 {
		t.Fatalf("expected JWKS to be fetched once, got %d", got)
	}
}

func TestTokenExtractor_RefreshesJWKSOnUnknownKid(t *testing.T) {
	privateKeyA, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate first rsa key: %v", err)
	}
	privateKeyB, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate second rsa key: %v", err)
	}

	var fetchCount int32
	var serveNewKey int32
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fetchCount, 1)
		w.Header().Set("Content-Type", "application/json")

		key := privateKeyA.PublicKey
		kid := "old-kid"
		if atomic.LoadInt32(&serveNewKey) == 1 {
			key = privateKeyB.PublicKey
			kid = "new-kid"
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kid": kid,
				"kty": "RSA",
				"alg": "RS256",
				"use": "sig",
				"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			}},
		})
	}))
	defer jwksServer.Close()

	oldClaims := newBaseClaims(AuthorizationCodeGrant)
	oldClaims["sub"] = testUserID
	oldClaims["email"] = testEmail
	oldClaims["phone_number"] = testPhone
	oldClaims["ouId"] = testOUID
	oldClaims["ouHandle"] = testOUHandle
	oldClaims["roles"] = []string{"exporter"}

	oldToken := jwt.NewWithClaims(jwt.SigningMethodRS256, oldClaims)
	oldToken.Header["kid"] = "old-kid"
	oldSignedToken, err := oldToken.SignedString(privateKeyA)
	if err != nil {
		t.Fatalf("failed to sign old token: %v", err)
	}

	newClaims := newBaseClaims(AuthorizationCodeGrant)
	newClaims["sub"] = testUserID
	newClaims["email"] = testEmail
	newClaims["phone_number"] = testPhone
	newClaims["ouId"] = testOUID
	newClaims["ouHandle"] = testOUHandle
	newClaims["roles"] = []string{"exporter"}

	newToken := jwt.NewWithClaims(jwt.SigningMethodRS256, newClaims)
	newToken.Header["kid"] = "new-kid"
	newSignedToken, err := newToken.SignedString(privateKeyB)
	if err != nil {
		t.Fatalf("failed to sign new token: %v", err)
	}

	extractor, err := NewTokenExtractor(jwksServer.URL, testIssuer, testClientID, []string{testClientID})
	if err != nil {
		t.Fatalf("failed to create token extractor: %v", err)
	}
	if _, err := extractor.ExtractPrincipalFromHeader("Bearer " + oldSignedToken); err != nil {
		t.Fatalf("old token extract failed: %v", err)
	}

	atomic.StoreInt32(&serveNewKey, 1)
	if _, err := extractor.ExtractPrincipalFromHeader("Bearer " + newSignedToken); err != nil {
		t.Fatalf("new token extract failed after refresh: %v", err)
	}

	if got := atomic.LoadInt32(&fetchCount); got != 2 {
		t.Fatalf("expected JWKS fetches to be 2 (initial + refresh on unknown kid), got %d", got)
	}
}

func TestTokenExtractor_UnknownKidAfterRefresh(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate rsa key: %v", err)
	}

	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kid": "other-kid",
				"kty": "RSA",
				"alg": "RS256",
				"use": "sig",
				"n":   base64.RawURLEncoding.EncodeToString(privateKey.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(privateKey.PublicKey.E)).Bytes()),
			}},
		})
	}))
	defer jwksServer.Close()

	claims := newBaseClaims(AuthorizationCodeGrant)
	claims["sub"] = testUserID
	claims["email"] = testEmail
	claims["phone_number"] = testPhone
	claims["ouId"] = testOUID
	claims["ouHandle"] = testOUHandle
	claims["roles"] = []string{"exporter"}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "missing-kid"
	signedToken, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	extractor, err := NewTokenExtractor(jwksServer.URL, testIssuer, testClientID, []string{testClientID})
	if err != nil {
		t.Fatalf("failed to create token extractor: %v", err)
	}

	if _, err := extractor.ExtractPrincipalFromHeader("Bearer " + signedToken); err == nil || !strings.Contains(err.Error(), "no jwk found for kid") {
		t.Fatalf("expected missing kid error, got %v", err)
	}
}

func TestTokenExtractor_JWKSFetchError(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate rsa key: %v", err)
	}

	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer jwksServer.Close()

	claims := newBaseClaims(AuthorizationCodeGrant)
	claims["sub"] = testUserID
	claims["email"] = testEmail
	claims["phone_number"] = testPhone
	claims["ouId"] = testOUID
	claims["ouHandle"] = testOUHandle
	claims["roles"] = []string{"exporter"}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = testKid
	signedToken, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	extractor, err := NewTokenExtractor(jwksServer.URL, testIssuer, testClientID, []string{testClientID})
	if err != nil {
		t.Fatalf("failed to create token extractor: %v", err)
	}

	if _, err := extractor.ExtractPrincipalFromHeader("Bearer " + signedToken); err == nil || !strings.Contains(err.Error(), "jwks endpoint returned status") {
		t.Fatalf("expected jwks status error, got %v", err)
	}
}

func TestTokenExtractor_ValidateConfig_MissingHTTPClient(t *testing.T) {
	extractor := &TokenExtractor{
		jwksURL:      "https://localhost/jwks",
		expIssuer:    testIssuer,
		expAudience:  testClientID,
		expClientIDs: []string{testClientID},
		httpClient:   nil,
	}

	if err := extractor.validateConfig(); err == nil {
		t.Fatalf("expected error for missing http client")
	}
}

func TestParseRSAPublicKey_InvalidData(t *testing.T) {
	validN := base64.RawURLEncoding.EncodeToString([]byte{1})

	tests := []struct {
		name      string
		key       jwk
		errSubstr string
	}{
		{
			name:      "invalid key type",
			key:       jwk{Kty: "EC"},
			errSubstr: "unsupported jwk key type",
		},
		{
			name:      "invalid modulus",
			key:       jwk{Kty: "RSA", N: "@@", E: "AQAB"},
			errSubstr: "failed to decode jwk modulus",
		},
		{
			name:      "invalid exponent",
			key:       jwk{Kty: "RSA", N: validN, E: "@@"},
			errSubstr: "failed to decode jwk exponent",
		},
		{
			name:      "empty key data",
			key:       jwk{Kty: "RSA", N: "", E: "AQAB"},
			errSubstr: "invalid jwk key data",
		},
		{
			name:      "invalid exponent value",
			key:       jwk{Kty: "RSA", N: validN, E: "AA"},
			errSubstr: "invalid jwk exponent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseRSAPublicKey(tt.key); err == nil || !strings.Contains(err.Error(), tt.errSubstr) {
				t.Fatalf("expected error containing %q, got %v", tt.errSubstr, err)
			}
		})
	}
}
