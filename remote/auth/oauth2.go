package auth

import (
	"context"
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
	TokenURL     string   `json:"token_url"`
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	Scopes       []string `json:"scopes,omitempty"`
}

type OAuth2 struct {
	cfg OAuth2Config

	// Internal client for fetching tokens
	httpClient *http.Client

	mu          sync.Mutex
	accessToken string
	expiry      time.Time
}

func NewOAuth2(cfg OAuth2Config) *OAuth2 {
	return &OAuth2{cfg: cfg}
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

	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("client_id", a.cfg.ClientID)
	data.Set("client_secret", a.cfg.ClientSecret)
	if len(a.cfg.Scopes) > 0 {
		data.Set("scope", strings.Join(a.cfg.Scopes, " "))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to create token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

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
