package auth

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type tokenClaims struct {
	jwt.RegisteredClaims
	ClientID string `json:"client_id"`
	OUHandle string `json:"ouHandle"`
}

type ExtractedClaims struct {
	TraderID string `json:"traderID"`
	OUHandle string `json:"ouHandle"`
}

type jwksResponse struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

const defaultJWKSCacheTTL = 5 * time.Minute

// TokenExtractor handles token extraction and parsing from HTTP headers.
// It validates JWT signatures using JWKS and maps the `sub` claim to TraderID.
type TokenExtractor struct {
	jwksURL          string
	issuer           string
	audience         string
	expectedClientID string
	httpClient       *http.Client

	cacheMu       sync.RWMutex
	cachedJWKS    *jwksResponse
	lastJWKSFetch time.Time
	jwksCacheTTL  time.Duration
}

func NewTokenExtractor(jwksURL, issuer, audience, expectedClientID string) (*TokenExtractor, error) {
	extractor := &TokenExtractor{
		jwksURL:          strings.TrimSpace(jwksURL),
		issuer:           strings.TrimSpace(issuer),
		audience:         strings.TrimSpace(audience),
		expectedClientID: strings.TrimSpace(expectedClientID),
		jwksCacheTTL:     defaultJWKSCacheTTL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}

	if err := extractor.validateConfig(); err != nil {
		return nil, err
	}

	return extractor, nil
}

func NewTokenExtractorWithClient(jwksURL, issuer, audience, expectedClientID string, httpClient *http.Client) (*TokenExtractor, error) {
	if httpClient == nil {
		return NewTokenExtractor(jwksURL, issuer, audience, expectedClientID)
	}

	extractor := &TokenExtractor{
		jwksURL:          strings.TrimSpace(jwksURL),
		issuer:           strings.TrimSpace(issuer),
		audience:         strings.TrimSpace(audience),
		expectedClientID: strings.TrimSpace(expectedClientID),
		jwksCacheTTL:     defaultJWKSCacheTTL,
		httpClient:       httpClient,
	}

	if err := extractor.validateConfig(); err != nil {
		return nil, err
	}

	return extractor, nil
}

func (te *TokenExtractor) validateConfig() error {
	if te.jwksURL == "" {
		return fmt.Errorf("jwks url is not configured")
	}
	if te.issuer == "" {
		return fmt.Errorf("issuer is not configured")
	}
	if te.audience == "" {
		return fmt.Errorf("audience is not configured")
	}
	if te.expectedClientID == "" {
		return fmt.Errorf("client id is not configured")
	}
	if te.httpClient == nil {
		return fmt.Errorf("http client is not configured")
	}

	return nil
}

// ExtractClaimsFromHeader extracts the claims from Authorization header.
// Expected header format: "Bearer <jwt_token>".
// JWT signature is validated against configured JWKS endpoint and `sub` is used as trader ID.
func (te *TokenExtractor) ExtractClaimsFromHeader(authHeader string) (*ExtractedClaims, error) {
	if authHeader == "" {
		return nil, fmt.Errorf("authorization header is empty")
	}
	parts := strings.Fields(strings.TrimSpace(authHeader))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return nil, fmt.Errorf("invalid authorization header format: expected 'Bearer <token>'")
	}
	tokenString := strings.TrimSpace(parts[1])
	if tokenString == "" {
		return nil, fmt.Errorf("authorization token is empty")
	}

	claims := &tokenClaims{}
	parsedToken, err := jwt.ParseWithClaims(tokenString, claims, te.keyFunc,
		jwt.WithValidMethods([]string{"RS256", "RS384", "RS512"}),
		jwt.WithIssuer(te.issuer),
		jwt.WithAudience(te.audience),
		jwt.WithLeeway(30*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("invalid jwt token: %w", err)
	}
	if !parsedToken.Valid {
		return nil, fmt.Errorf("invalid jwt token")
	}

	if claims.ExpiresAt == nil {
		return nil, fmt.Errorf("jwt missing exp claim")
	}

	traderID := strings.TrimSpace(claims.Subject)
	if len(traderID) == 0 {
		return nil, fmt.Errorf("jwt missing sub claim")
	}

	if strings.TrimSpace(claims.ClientID) == "" {
		return nil, fmt.Errorf("jwt missing client_id claim")
	}
	if strings.TrimSpace(claims.ClientID) != te.expectedClientID {
		return nil, fmt.Errorf("jwt client_id does not match expected client id")
	}

	return &ExtractedClaims{
		TraderID: traderID,
		OUHandle: strings.TrimSpace(claims.OUHandle),
	}, nil
}

func (te *TokenExtractor) keyFunc(token *jwt.Token) (interface{}, error) {
	if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
		return nil, fmt.Errorf("unexpected signing method: %s", token.Method.Alg())
	}

	kidValue, ok := token.Header["kid"]
	if !ok {
		return nil, fmt.Errorf("token header missing kid")
	}
	kid, ok := kidValue.(string)
	if !ok || strings.TrimSpace(kid) == "" {
		return nil, fmt.Errorf("token header has invalid kid")
	}

	keySet, err := te.getJWKS(false)
	if err != nil {
		return nil, err
	}

	for _, key := range keySet.Keys {
		if key.Kid != kid {
			continue
		}
		publicKey, err := parseRSAPublicKey(key)
		if err != nil {
			return nil, err
		}
		return publicKey, nil
	}

	// Key rotation can result in unknown kid in cache; force a refresh and retry once.
	keySet, err = te.getJWKS(true)
	if err != nil {
		return nil, err
	}

	for _, key := range keySet.Keys {
		if key.Kid != kid {
			continue
		}
		publicKey, err := parseRSAPublicKey(key)
		if err != nil {
			return nil, err
		}
		return publicKey, nil
	}

	return nil, fmt.Errorf("no jwk found for kid: %s", kid)
}

func (te *TokenExtractor) getJWKS(forceRefresh bool) (*jwksResponse, error) {
	now := time.Now()

	te.cacheMu.RLock()
	cacheValid := te.cachedJWKS != nil && te.jwksCacheTTL > 0 && now.Sub(te.lastJWKSFetch) < te.jwksCacheTTL
	if !forceRefresh && cacheValid {
		cached := te.cachedJWKS
		te.cacheMu.RUnlock()
		return cached, nil
	}
	te.cacheMu.RUnlock()

	te.cacheMu.Lock()
	defer te.cacheMu.Unlock()

	// Re-check after acquiring write lock in case another goroutine refreshed it.
	now = time.Now()
	cacheValid = te.cachedJWKS != nil && te.jwksCacheTTL > 0 && now.Sub(te.lastJWKSFetch) < te.jwksCacheTTL
	if !forceRefresh && cacheValid {
		return te.cachedJWKS, nil
	}

	jwks, err := te.fetchJWKS()
	if err != nil {
		return nil, err
	}

	te.cachedJWKS = jwks
	te.lastJWKSFetch = now

	return te.cachedJWKS, nil
}

func (te *TokenExtractor) fetchJWKS() (*jwksResponse, error) {
	request, err := http.NewRequest(http.MethodGet, te.jwksURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build jwks request: %w", err)
	}

	response, err := te.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch jwks: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks endpoint returned status %d", response.StatusCode)
	}

	var jwks jwksResponse
	if err := json.NewDecoder(response.Body).Decode(&jwks); err != nil {
		return nil, fmt.Errorf("failed to decode jwks response: %w", err)
	}

	if len(jwks.Keys) == 0 {
		return nil, fmt.Errorf("jwks response has no keys")
	}

	return &jwks, nil
}

func parseRSAPublicKey(key jwk) (*rsa.PublicKey, error) {
	if key.Kty != "RSA" {
		return nil, fmt.Errorf("unsupported jwk key type: %s", key.Kty)
	}

	modulusBytes, err := base64.RawURLEncoding.DecodeString(key.N)
	if err != nil {
		return nil, fmt.Errorf("failed to decode jwk modulus: %w", err)
	}
	exponentBytes, err := base64.RawURLEncoding.DecodeString(key.E)
	if err != nil {
		return nil, fmt.Errorf("failed to decode jwk exponent: %w", err)
	}

	if len(modulusBytes) == 0 || len(exponentBytes) == 0 {
		return nil, fmt.Errorf("invalid jwk key data")
	}

	exponentInt := new(big.Int).SetBytes(exponentBytes).Int64()
	if exponentInt <= 0 {
		return nil, fmt.Errorf("invalid jwk exponent")
	}

	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(modulusBytes),
		E: int(exponentInt),
	}, nil
}
