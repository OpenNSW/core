package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testIssuer   = "https://localhost:8090/oauth2/token"
	testClientID = "TRADER_PORTAL_APP"
	testKid      = "test-kid"

	testUserID   = "TRADER-001"
	testEmail    = "trader@example.com"
	testPhone    = "+61400111222"
	testOUID     = "OU-001"
	testOUHandle = "ou-001"
)

func newTokenExtractor(t *testing.T) (*TokenExtractor, *rsa.PrivateKey, func()) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate rsa key: %v", err)
	}

	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kid": testKid,
				"kty": "RSA",
				"alg": "RS256",
				"use": "sig",
				"n":   base64.RawURLEncoding.EncodeToString(privateKey.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(privateKey.PublicKey.E)).Bytes()),
			}},
		})
	}))

	extractor, err := NewTokenExtractor(jwksServer.URL, testIssuer, testClientID, []string{testClientID})
	if err != nil {
		jwksServer.Close()
		t.Fatalf("failed to create token extractor: %v", err)
	}

	return extractor, privateKey, jwksServer.Close
}

func newBaseClaims(grantType AllowedGrantType) jwt.MapClaims {
	now := time.Now()
	return jwt.MapClaims{
		"iss":        testIssuer,
		"aud":        testClientID,
		"client_id":  testClientID,
		"grant_type": grantType,
		"iat":        now.Add(-1 * time.Minute).Unix(),
		"nbf":        now.Add(-1 * time.Minute).Unix(),
		"exp":        now.Add(10 * time.Minute).Unix(),
	}
}

func newUserToken(t *testing.T, privateKey *rsa.PrivateKey) string {
	t.Helper()
	claims := newBaseClaims(AuthorizationCodeGrant)
	claims["sub"] = testUserID
	claims["email"] = testEmail
	claims["phone_number"] = testPhone
	claims["ouId"] = testOUID
	claims["ouHandle"] = testOUHandle
	claims["roles"] = []string{"exporter"}
	return signToken(t, privateKey, claims)
}

func newClientToken(t *testing.T, privateKey *rsa.PrivateKey) string {
	t.Helper()
	claims := newBaseClaims(ClientCredentialsGrant)
	claims["sub"] = "FCAU_TO_NSW"
	return signToken(t, privateKey, claims)
}

func signToken(t *testing.T, privateKey *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = testKid
	signedToken, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}
	return signedToken
}

func strPtr(value string) *string { return &value }
