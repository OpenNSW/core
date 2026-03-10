package auth

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
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TestTokenExtractor tests the token extraction logic
func TestTokenExtractor_ExtractClaimsFromHeader(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate rsa key: %v", err)
	}

	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"keys": []map[string]interface{}{
				{
					"kid": "test-kid",
					"kty": "RSA",
					"alg": "RS256",
					"use": "sig",
					"n":   base64.RawURLEncoding.EncodeToString(privateKey.N.Bytes()),
					"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(privateKey.PublicKey.E)).Bytes()),
				},
			},
		})
	}))
	defer jwksServer.Close()

	extractor, err := NewTokenExtractor(jwksServer.URL, "https://localhost:8090/oauth2/token", "TRADER_PORTAL_APP", "TRADER_PORTAL_APP")
	if err != nil {
		t.Fatalf("failed to create token extractor: %v", err)
	}

	mintToken := func(subject string, issuer string, audience string, clientID string, ouHandle string, notBefore time.Time, expiresAt time.Time) string {
		claims := jwt.RegisteredClaims{
			Subject:   subject,
			Issuer:    issuer,
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-1 * time.Minute)),
			NotBefore: jwt.NewNumericDate(notBefore),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		}
		tokenClaims := jwt.MapClaims{
			"sub":       claims.Subject,
			"iss":       claims.Issuer,
			"aud":       audience,
			"client_id": clientID,
			"iat":       claims.IssuedAt.Unix(),
			"nbf":       claims.NotBefore.Unix(),
			"exp":       claims.ExpiresAt.Unix(),
		}
		if strings.TrimSpace(ouHandle) != "" {
			tokenClaims["ouHandle"] = ouHandle
		}

		token := jwt.NewWithClaims(jwt.SigningMethodRS256, tokenClaims)
		token.Header["kid"] = "test-kid"
		signedToken, signErr := token.SignedString(privateKey)
		if signErr != nil {
			t.Fatalf("failed to sign token: %v", signErr)
		}
		return signedToken
	}

	validToken := mintToken(
		"TRADER-001",
		"https://localhost:8090/oauth2/token",
		"TRADER_PORTAL_APP",
		"TRADER_PORTAL_APP",
		"traders",
		time.Now().Add(-1*time.Minute),
		time.Now().Add(10*time.Minute),
	)

	expiredToken := mintToken(
		"TRADER-001",
		"https://localhost:8090/oauth2/token",
		"TRADER_PORTAL_APP",
		"TRADER_PORTAL_APP",
		"traders",
		time.Now().Add(-10*time.Minute),
		time.Now().Add(-1*time.Minute),
	)

	missingSubToken := mintToken(
		"",
		"https://localhost:8090/oauth2/token",
		"TRADER_PORTAL_APP",
		"TRADER_PORTAL_APP",
		"traders",
		time.Now().Add(-1*time.Minute),
		time.Now().Add(10*time.Minute),
	)

	wrongIssuerToken := mintToken(
		"TRADER-001",
		"https://wrong-issuer.example.com",
		"TRADER_PORTAL_APP",
		"TRADER_PORTAL_APP",
		"traders",
		time.Now().Add(-1*time.Minute),
		time.Now().Add(10*time.Minute),
	)

	wrongAudienceToken := mintToken(
		"TRADER-001",
		"https://localhost:8090/oauth2/token",
		"OTHER_AUDIENCE",
		"TRADER_PORTAL_APP",
		"traders",
		time.Now().Add(-1*time.Minute),
		time.Now().Add(10*time.Minute),
	)

	wrongClientIDToken := mintToken(
		"TRADER-001",
		"https://localhost:8090/oauth2/token",
		"TRADER_PORTAL_APP",
		"OTHER_CLIENT",
		"traders",
		time.Now().Add(-1*time.Minute),
		time.Now().Add(10*time.Minute),
	)

	missingOUHandleToken := mintToken(
		"TRADER-001",
		"https://localhost:8090/oauth2/token",
		"TRADER_PORTAL_APP",
		"TRADER_PORTAL_APP",
		"",
		time.Now().Add(-1*time.Minute),
		time.Now().Add(10*time.Minute),
	)

	tests := []struct {
		name       string
		authHeader string
		want       string
		wantErr    bool
	}{
		{
			name:       "valid bearer jwt token",
			authHeader: "Bearer " + validToken,
			want:       "TRADER-001",
			wantErr:    false,
		},
		{
			name:       "valid bearer jwt token with spaces",
			authHeader: "   Bearer " + validToken + "   ",
			want:       "TRADER-001",
			wantErr:    false,
		},
		{
			name:       "empty auth header",
			authHeader: "",
			want:       "",
			wantErr:    true,
		},
		{
			name:       "invalid format - missing bearer prefix",
			authHeader: "TRADER-001",
			want:       "",
			wantErr:    true,
		},
		{
			name:       "invalid bearer format - no token",
			authHeader: "Bearer",
			want:       "",
			wantErr:    true,
		},
		{
			name:       "invalid jwt token",
			authHeader: "Bearer invalid.jwt.token",
			want:       "",
			wantErr:    true,
		},
		{
			name:       "expired jwt token",
			authHeader: "Bearer " + expiredToken,
			want:       "",
			wantErr:    true,
		},
		{
			name:       "missing sub claim",
			authHeader: "Bearer " + missingSubToken,
			want:       "",
			wantErr:    true,
		},
		{
			name:       "invalid issuer",
			authHeader: "Bearer " + wrongIssuerToken,
			want:       "",
			wantErr:    true,
		},
		{
			name:       "invalid audience",
			authHeader: "Bearer " + wrongAudienceToken,
			want:       "",
			wantErr:    true,
		},
		{
			name:       "invalid client_id",
			authHeader: "Bearer " + wrongClientIDToken,
			want:       "",
			wantErr:    true,
		},
		{
			name:       "valid token without ouHandle",
			authHeader: "Bearer " + missingOUHandleToken,
			want:       "TRADER-001",
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims, err := extractor.ExtractClaimsFromHeader(tt.authHeader)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExtractClaimsFromHeader() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			got := ""
			if claims != nil {
				got = claims.TraderID
			}
			if got != tt.want {
				t.Errorf("ExtractClaimsFromHeader() got TraderID = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestTraderContextModel tests the TraderContext model structure
func TestTraderContextModel(t *testing.T) {
	tests := []struct {
		name      string
		traderID  string
		context   map[string]interface{}
		wantTable string
	}{
		{
			name:     "valid trader context",
			traderID: "TRADER-001",
			context: map[string]interface{}{
				"company": "Acme Inc",
				"role":    "exporter",
			},
			wantTable: "trader_contexts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contextJSON, err := json.Marshal(tt.context)
			if err != nil {
				t.Fatalf("failed to marshal test context: %v", err)
			}
			traderCtx := &TraderContext{
				TraderID:      tt.traderID,
				TraderContext: contextJSON,
			}

			got := traderCtx.TableName()
			if got != tt.wantTable {
				t.Errorf("TableName() got = %v, want %v", got, tt.wantTable)
			}
		})
	}
}

// TestAuthContextCreation tests AuthContext creation
func TestAuthContextCreation(t *testing.T) {
	contextJSON := json.RawMessage(`{"company": "Test Corp"}`)
	tc := &TraderContext{
		TraderID:      "TRADER-TEST",
		TraderContext: contextJSON,
	}

	authCtx := &AuthContext{
		TraderContext: tc,
	}

	if authCtx.TraderID != "TRADER-TEST" {
		t.Errorf("AuthContext.TraderID got = %v, want TRADER-TEST", authCtx.TraderID)
	}

	if string(authCtx.TraderContext.TraderContext) != `{"company": "Test Corp"}` {
		t.Errorf("AuthContext.TraderContext not preserved")
	}
}

// Example benchmark for token extraction
func BenchmarkTokenExtraction(b *testing.B) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		b.Fatalf("failed to generate rsa key: %v", err)
	}

	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"keys": []map[string]interface{}{
				{
					"kid": "bench-kid",
					"kty": "RSA",
					"alg": "RS256",
					"use": "sig",
					"n":   base64.RawURLEncoding.EncodeToString(privateKey.N.Bytes()),
					"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(privateKey.PublicKey.E)).Bytes()),
				},
			},
		})
	}))
	defer jwksServer.Close()

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub":       "TRADER-001",
		"iss":       "https://localhost:8090/oauth2/token",
		"aud":       "TRADER_PORTAL_APP",
		"client_id": "TRADER_PORTAL_APP",
		"iat":       time.Now().Add(-1 * time.Minute).Unix(),
		"nbf":       time.Now().Add(-1 * time.Minute).Unix(),
		"exp":       time.Now().Add(10 * time.Minute).Unix(),
	})
	token.Header["kid"] = "bench-kid"
	signedToken, err := token.SignedString(privateKey)
	if err != nil {
		b.Fatalf("failed to sign token: %v", err)
	}

	extractor, err := NewTokenExtractor(jwksServer.URL, "https://localhost:8090/oauth2/token", "TRADER_PORTAL_APP", "TRADER_PORTAL_APP")
	if err != nil {
		b.Fatalf("failed to create token extractor: %v", err)
	}
	authHeader := "Bearer " + signedToken

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		extractor.ExtractClaimsFromHeader(authHeader)
	}
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
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"keys": []map[string]interface{}{
				{
					"kid": "cache-kid",
					"kty": "RSA",
					"alg": "RS256",
					"use": "sig",
					"n":   base64.RawURLEncoding.EncodeToString(privateKey.N.Bytes()),
					"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(privateKey.PublicKey.E)).Bytes()),
				},
			},
		})
	}))
	defer jwksServer.Close()

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub":       "TRADER-001",
		"iss":       "https://localhost:8090/oauth2/token",
		"aud":       "TRADER_PORTAL_APP",
		"client_id": "TRADER_PORTAL_APP",
		"iat":       time.Now().Add(-1 * time.Minute).Unix(),
		"nbf":       time.Now().Add(-1 * time.Minute).Unix(),
		"exp":       time.Now().Add(10 * time.Minute).Unix(),
	})
	token.Header["kid"] = "cache-kid"
	signedToken, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	extractor, err := NewTokenExtractor(jwksServer.URL, "https://localhost:8090/oauth2/token", "TRADER_PORTAL_APP", "TRADER_PORTAL_APP")
	if err != nil {
		t.Fatalf("failed to create token extractor: %v", err)
	}
	if _, err := extractor.ExtractClaimsFromHeader("Bearer " + signedToken); err != nil {
		t.Fatalf("first extract failed: %v", err)
	}
	if _, err := extractor.ExtractClaimsFromHeader("Bearer " + signedToken); err != nil {
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

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"keys": []map[string]interface{}{
				{
					"kid": kid,
					"kty": "RSA",
					"alg": "RS256",
					"use": "sig",
					"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
					"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
				},
			},
		})
	}))
	defer jwksServer.Close()

	oldToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub":       "TRADER-001",
		"iss":       "https://localhost:8090/oauth2/token",
		"aud":       "TRADER_PORTAL_APP",
		"client_id": "TRADER_PORTAL_APP",
		"iat":       time.Now().Add(-1 * time.Minute).Unix(),
		"nbf":       time.Now().Add(-1 * time.Minute).Unix(),
		"exp":       time.Now().Add(10 * time.Minute).Unix(),
	})
	oldToken.Header["kid"] = "old-kid"
	oldSignedToken, err := oldToken.SignedString(privateKeyA)
	if err != nil {
		t.Fatalf("failed to sign old token: %v", err)
	}

	newToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub":       "TRADER-001",
		"iss":       "https://localhost:8090/oauth2/token",
		"aud":       "TRADER_PORTAL_APP",
		"client_id": "TRADER_PORTAL_APP",
		"iat":       time.Now().Add(-1 * time.Minute).Unix(),
		"nbf":       time.Now().Add(-1 * time.Minute).Unix(),
		"exp":       time.Now().Add(10 * time.Minute).Unix(),
	})
	newToken.Header["kid"] = "new-kid"
	newSignedToken, err := newToken.SignedString(privateKeyB)
	if err != nil {
		t.Fatalf("failed to sign new token: %v", err)
	}

	extractor, err := NewTokenExtractor(jwksServer.URL, "https://localhost:8090/oauth2/token", "TRADER_PORTAL_APP", "TRADER_PORTAL_APP")
	if err != nil {
		t.Fatalf("failed to create token extractor: %v", err)
	}
	if _, err := extractor.ExtractClaimsFromHeader("Bearer " + oldSignedToken); err != nil {
		t.Fatalf("old token extract failed: %v", err)
	}

	atomic.StoreInt32(&serveNewKey, 1)
	if _, err := extractor.ExtractClaimsFromHeader("Bearer " + newSignedToken); err != nil {
		t.Fatalf("new token extract failed after refresh: %v", err)
	}

	if got := atomic.LoadInt32(&fetchCount); got != 2 {
		t.Fatalf("expected JWKS fetches to be 2 (initial + refresh on unknown kid), got %d", got)
	}
}

func TestNewTokenExtractor_InvalidConfig(t *testing.T) {
	tests := []struct {
		name             string
		jwksURL          string
		issuer           string
		audience         string
		expectedClientID string
	}{
		{name: "missing jwks url", issuer: "iss", audience: "aud", expectedClientID: "client"},
		{name: "missing issuer", jwksURL: "https://localhost/jwks", audience: "aud", expectedClientID: "client"},
		{name: "missing audience", jwksURL: "https://localhost/jwks", issuer: "iss", expectedClientID: "client"},
		{name: "missing client id", jwksURL: "https://localhost/jwks", issuer: "iss", audience: "aud"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extractor, err := NewTokenExtractor(tt.jwksURL, tt.issuer, tt.audience, tt.expectedClientID)
			if err == nil {
				t.Fatalf("expected constructor error, got extractor: %#v", extractor)
			}
		})
	}
}
