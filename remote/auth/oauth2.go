// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package auth

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type OAuth2Config struct {
	TokenURL              string    `json:"token_url"`
	ClientID              string    `json:"client_id"`
	ClientSecret          SecretRef `json:"client_secret"`
	Scopes                []string  `json:"scopes,omitempty"`
	InsecureSkipTLSVerify bool      `json:"insecure_skip_tls_verify,omitempty"`
}

// build resolves the configured client secret (failing loud on an unresolvable
// reference) and constructs the authenticator.
func (c OAuth2Config) build() (Authenticator, error) {
	clientSecret, err := c.ClientSecret.Resolve()
	if err != nil {
		return nil, fmt.Errorf("oauth2 client_secret: %w", err)
	}
	auth := NewOAuth2(c.TokenURL, c.ClientID, clientSecret, c.Scopes)
	auth.SetInsecureSkipTLSVerify(c.InsecureSkipTLSVerify)
	return auth, nil
}

type OAuth2 struct {
	tokenURL              string
	clientID              string
	clientSecret          string
	scopes                []string
	insecureSkipTLSVerify bool

	// Internal client for fetching tokens
	httpClient *http.Client

	mu          sync.Mutex
	accessToken string
	expiry      time.Time
}

// NewOAuth2 builds an OAuth2 client-credentials authenticator from
// already-resolved values.
func NewOAuth2(tokenURL, clientID, clientSecret string, scopes []string) *OAuth2 {
	return &OAuth2{
		tokenURL:     tokenURL,
		clientID:     clientID,
		clientSecret: clientSecret,
		scopes:       scopes,
	}
}

// SetInsecureSkipTLSVerify controls whether the OAuth2 token request client skips
// certificate verification. This is intended for local development with self-signed
// identity-provider certificates only.
func (a *OAuth2) SetInsecureSkipTLSVerify(skip bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.insecureSkipTLSVerify = skip
	if a.httpClient == nil {
		return
	}
	if skip {
		a.httpClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
		return
	}
	a.httpClient.Transport = nil
}

type oauth2TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

func (a *OAuth2) Apply(req *http.Request) error {
	token, err := a.getToken(req.Context())
	if err != nil {
		return fmt.Errorf("remote/auth: oauth2 failed: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

// getToken retrieves a valid token from cache or fetches a new one if expired.
func (a *OAuth2) getToken(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Return cached token if still valid (with 1-minute buffer for safety)
	if a.accessToken != "" && time.Now().Add(time.Minute).Before(a.expiry) {
		return a.accessToken, nil
	}

	// Fetch new token
	token, expiry, err := a.refreshToken(ctx)
	if err != nil {
		return "", err
	}

	a.accessToken = token
	a.expiry = expiry

	return a.accessToken, nil
}

func (a *OAuth2) refreshToken(ctx context.Context) (string, time.Time, error) {
	if a.httpClient == nil {
		a.httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	if a.insecureSkipTLSVerify && a.httpClient.Transport == nil {
		a.httpClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}

	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	if len(a.scopes) > 0 {
		data.Set("scope", strings.Join(a.scopes, " "))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to create token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Authenticate with client_secret_basic (RFC 6749 §2.3.1): credentials go in
	// the Authorization header, form-url-encoded then base64. This is the OAuth2
	// recommended method and is required by some providers; credentials in the
	// body (client_secret_post) are not universally accepted.
	req.SetBasicAuth(url.QueryEscape(a.clientID), url.QueryEscape(a.clientSecret))

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("token request failed: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Warn("failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("token request returned status %d", resp.StatusCode)
	}

	var tokenResp oauth2TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", time.Time{}, fmt.Errorf("failed to decode token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return "", time.Time{}, fmt.Errorf("token response contained no access token")
	}

	// Calculate absolute expiry time
	expiry := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	return tokenResp.AccessToken, expiry, nil
}
