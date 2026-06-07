package authn

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type AllowedGrantType string

const (
	AuthorizationCodeGrant AllowedGrantType = "authorization_code"
	ClientCredentialsGrant AllowedGrantType = "client_credentials"
)

// spaceDelimitedScope unmarshals the OAuth2 "scope" claim, which is a single
// space-delimited string (RFC 6749 §3.3), e.g. "nsw:task:read nsw:storage:read".
// It defensively also accepts a JSON array of strings. An absent claim leaves it
// nil; an empty string yields an empty slice.
type spaceDelimitedScope []string

func (s *spaceDelimitedScope) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		*s = strings.Fields(str)
		return nil
	}
	var arr []string
	if err := json.Unmarshal(data, &arr); err != nil {
		return fmt.Errorf("scope claim must be a string or array of strings: %w", err)
	}
	*s = arr
	return nil
}

type tokenClaims struct {
	jwt.RegisteredClaims
	ClientID    string              `json:"client_id"`
	GrantType   AllowedGrantType    `json:"grant_type"`
	Email       *string             `json:"email,omitempty"`
	PhoneNumber *string             `json:"phone_number,omitempty"`
	OUID        *string             `json:"ouId,omitempty"`
	OUHandle    *string             `json:"ouHandle,omitempty"`
	Roles       []string            `json:"roles,omitempty"`
	Scopes      spaceDelimitedScope `json:"scope,omitempty"`
}

type PrincipalType string

const (
	UserPrincipalType   PrincipalType = "user"
	ClientPrincipalType PrincipalType = "client"
)

type ClientPrincipal struct {
	ClientID string   `json:"clientId"`
	Roles    []string `json:"roles"`
	Scopes   []string `json:"scopes"`
}

type UserPrincipal struct {
	UserID      string   `json:"userId"`
	Email       string   `json:"email"`
	PhoneNumber *string  `json:"phone_number,omitempty"`
	OUID        string   `json:"ouId"`
	OUHandle    string   `json:"ouHandle"`
	Roles       []string `json:"roles"`
	Scopes      []string `json:"scopes"`
}

type Principal struct {
	Type            PrincipalType    `json:"type"`
	UserPrincipal   *UserPrincipal   `json:"userPrincipal,omitempty"`
	ClientPrincipal *ClientPrincipal `json:"clientPrincipal,omitempty"`
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
// It validates JWT signatures using JWKS and resolves a user principal
// or client principal based on grant type.
type TokenExtractor struct {
	jwksURL      string
	expIssuer    string
	expAudience  string
	expClientIDs []string
	httpClient   *http.Client

	cacheMu       sync.RWMutex
	cachedJWKS    *jwksResponse
	lastJWKSFetch time.Time
	jwksCacheTTL  time.Duration
}

func NewTokenExtractor(jwksURL, issuer, audience string, expectedClientIDs []string) (*TokenExtractor, error) {
	extractor := &TokenExtractor{
		jwksURL:      strings.TrimSpace(jwksURL),
		expIssuer:    strings.TrimSpace(issuer),
		expAudience:  strings.TrimSpace(audience),
		expClientIDs: expectedClientIDs,
		jwksCacheTTL: defaultJWKSCacheTTL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}

	if err := extractor.validateConfig(); err != nil {
		return nil, err
	}

	return extractor, nil
}

func NewTokenExtractorWithClient(jwksURL, issuer, audience string, expectedClientIDs []string, httpClient *http.Client) (*TokenExtractor, error) {
	if httpClient == nil {
		return NewTokenExtractor(jwksURL, issuer, audience, expectedClientIDs)
	}

	extractor := &TokenExtractor{
		jwksURL:      strings.TrimSpace(jwksURL),
		expIssuer:    strings.TrimSpace(issuer),
		expAudience:  strings.TrimSpace(audience),
		expClientIDs: expectedClientIDs,
		jwksCacheTTL: defaultJWKSCacheTTL,
		httpClient:   httpClient,
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
	if te.expIssuer == "" {
		return fmt.Errorf("issuer is not configured")
	}
	if te.expAudience == "" {
		return fmt.Errorf("audience is not configured")
	}
	if len(te.expClientIDs) == 0 {
		return fmt.Errorf("client ids are not configured")
	}
	if te.httpClient == nil {
		return fmt.Errorf("http client is not configured")
	}

	return nil
}

// ExtractPrincipalFromHeader extracts the principal from Authorization header.
// Expected header format: "Bearer <jwt_token>".
// JWT signature is validated against configured JWKS endpoint, then claims are
// mapped into either UserPrincipal or ClientPrincipal.
func (te *TokenExtractor) ExtractPrincipalFromHeader(authHeader string) (*Principal, error) {
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
		jwt.WithIssuer(te.expIssuer),
		jwt.WithAudience(te.expAudience),
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

	if claims.ClientID == "" {
		return nil, fmt.Errorf("jwt missing client_id claim")
	}
	if !slices.Contains(te.expClientIDs, claims.ClientID) {
		return nil, fmt.Errorf("unexpected client_id claim: %q", claims.ClientID)
	}

	switch claims.GrantType {
	case AuthorizationCodeGrant:
		userPrincipal, err := te.userPrincipalFromClaims(claims)
		if err != nil {
			return nil, err
		}
		return &Principal{
			Type:          UserPrincipalType,
			UserPrincipal: userPrincipal,
		}, nil
	case ClientCredentialsGrant:
		clientPrincipal, err := te.clientPrincipalFromClaims(claims)
		if err != nil {
			return nil, err
		}
		return &Principal{
			Type:            ClientPrincipalType,
			ClientPrincipal: clientPrincipal,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported grant type: %q", claims.GrantType)
	}
}

func (te *TokenExtractor) userPrincipalFromClaims(claims *tokenClaims) (*UserPrincipal, error) {
	if claims.Subject == "" {
		return nil, fmt.Errorf("jwt missing sub claim for user principal")
	}
	if claims.Email == nil {
		return nil, fmt.Errorf("jwt missing email claim for user principal")
	}
	if claims.OUID == nil {
		return nil, fmt.Errorf("jwt missing ouId claim for user principal")
	}
	if claims.OUHandle == nil {
		return nil, fmt.Errorf("jwt missing ouHandle claim for user principal")
	}

	// Phone number is optional as not all IdPs may provide it, but if it's present it should not be empty.
	if claims.PhoneNumber != nil && strings.TrimSpace(*claims.PhoneNumber) == "" {
		return nil, fmt.Errorf("jwt has empty phone_number claim for user principal")
	}

	return &UserPrincipal{
		UserID:      claims.Subject,
		Email:       *claims.Email,
		PhoneNumber: claims.PhoneNumber,
		OUID:        *claims.OUID,
		OUHandle:    *claims.OUHandle,
		Roles:       claims.Roles,
		Scopes:      []string(claims.Scopes),
	}, nil
}

func (te *TokenExtractor) clientPrincipalFromClaims(claims *tokenClaims) (*ClientPrincipal, error) {
	if claims.ClientID == "" {
		return nil, fmt.Errorf("jwt missing client_id claim for client principal")
	}
	return &ClientPrincipal{
		ClientID: claims.ClientID,
		Roles:    claims.Roles,
		Scopes:   []string(claims.Scopes),
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
