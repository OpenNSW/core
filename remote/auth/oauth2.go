// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package auth

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/OpenNSW/core/secret"
)

// reservedEndpointParams are set by the client-credentials flow itself. Letting
// configuration override them would either break the grant or, for the credential pair,
// transmit it by two routes at once — this authenticator always uses client_secret_basic.
var reservedEndpointParams = []string{"grant_type", "scope", "client_id", "client_secret"}

// EndpointParams carries extra parameters for the token request. They are sent in the
// request body alongside grant_type and scope, as RFC 6749 §3.2 requires — for example the
// RFC 8707 `resource` indicator, which names the resource server an access token is for.
//
// It is url.Values so it drops straight into the token request (and into
// golang.org/x/oauth2/clientcredentials.Config.EndpointParams) without conversion, but its
// JSON accepts either a string or an array of strings per key, since a single value is the
// normal case:
//
//	"endpoint_params": { "resource": "https://api.example" }
//	"endpoint_params": { "resource": ["https://a.example", "https://b.example"] }
type EndpointParams url.Values

// UnmarshalJSON accepts a string or an array of strings for each key.
func (p *EndpointParams) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("endpoint_params: %w", err)
	}

	out := make(EndpointParams, len(raw))
	for key, value := range raw {
		var single string
		if err := json.Unmarshal(value, &single); err == nil {
			out[key] = []string{single}
			continue
		}

		var multiple []string
		if err := json.Unmarshal(value, &multiple); err != nil {
			return fmt.Errorf("endpoint_params: %q must be a string or an array of strings", key)
		}
		out[key] = multiple
	}

	*p = out
	return nil
}

// validate rejects parameters the flow sets itself.
func (p EndpointParams) validate() error {
	for _, key := range reservedEndpointParams {
		if _, ok := p[key]; ok {
			return fmt.Errorf("endpoint_params must not set %q: it is set by the client-credentials flow", key)
		}
	}
	return nil
}

type OAuth2Config struct {
	TokenURL              string           `json:"token_url"`
	ClientID              string           `json:"client_id"`
	ClientSecret          secret.SecretRef `json:"client_secret"`
	Scopes                []string         `json:"scopes,omitempty"`
	InsecureSkipTLSVerify bool             `json:"insecure_skip_tls_verify,omitempty"`
	EndpointParams        EndpointParams   `json:"endpoint_params,omitempty"`
}

// build resolves the configured client secret (failing loud on an unresolvable
// reference) and constructs the authenticator.
func (c OAuth2Config) build() (Authenticator, error) {
	clientSecret, err := c.ClientSecret.Resolve()
	if err != nil {
		return nil, fmt.Errorf("oauth2 client_secret: %w", err)
	}
	// Validated here, not just at token-request time, so a bad configuration fails when
	// services are loaded rather than on the first outbound call.
	if err := c.EndpointParams.validate(); err != nil {
		return nil, fmt.Errorf("oauth2: %w", err)
	}
	auth := NewOAuth2(c.TokenURL, c.ClientID, clientSecret, c.Scopes,
		WithEndpointParams(url.Values(c.EndpointParams)))
	auth.SetInsecureSkipTLSVerify(c.InsecureSkipTLSVerify)
	return auth, nil
}

type OAuth2 struct {
	tokenURL              string
	clientID              string
	clientSecret          string
	scopes                []string
	endpointParams        url.Values
	insecureSkipTLSVerify bool

	// Internal client for fetching tokens
	httpClient *http.Client

	mu          sync.Mutex
	accessToken string
	expiry      time.Time
}

// OAuth2Option configures an OAuth2 authenticator.
type OAuth2Option func(*OAuth2)

// WithEndpointParams adds extra parameters to the token request body. Empty values are
// ignored. See EndpointParams for what belongs here.
func WithEndpointParams(params url.Values) OAuth2Option {
	return func(a *OAuth2) {
		if len(params) > 0 {
			a.endpointParams = params
		}
	}
}

// NewOAuth2 builds an OAuth2 client-credentials authenticator from
// already-resolved values.
func NewOAuth2(tokenURL, clientID, clientSecret string, scopes []string, opts ...OAuth2Option) *OAuth2 {
	a := &OAuth2{
		tokenURL:     tokenURL,
		clientID:     clientID,
		clientSecret: clientSecret,
		scopes:       scopes,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
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

// oauth2ErrorResponse is the error payload of RFC 6749 §5.2.
type oauth2ErrorResponse struct {
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

// maxErrorBodyBytes caps how much of a failed token response is read for diagnostics.
const maxErrorBodyBytes = 4 << 10

// oauthErrorDetail extracts the RFC 6749 §5.2 error code and description from a failed
// token response, formatted for appending to an error message. Without it a rejection
// reads only as a status code, which hides the reason — `invalid_target` for a resource
// indicator that names no registered resource server, say, or `invalid_scope`.
//
// Returns an empty string when the body is missing, unreadable, or not an OAuth2 error;
// the caller still reports the status code, so nothing is lost.
func oauthErrorDetail(body io.Reader) string {
	raw, err := io.ReadAll(io.LimitReader(body, maxErrorBodyBytes))
	if err != nil || len(raw) == 0 {
		return ""
	}

	var errResp oauth2ErrorResponse
	if err := json.Unmarshal(raw, &errResp); err != nil || errResp.Error == "" {
		return ""
	}

	if errResp.Description == "" {
		return ": " + errResp.Error
	}
	return ": " + errResp.Error + ": " + errResp.Description
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
	// Re-checked here because an authenticator can also be built programmatically, which
	// does not go through OAuth2Config.build.
	if err := EndpointParams(a.endpointParams).validate(); err != nil {
		return "", time.Time{}, err
	}
	for key, values := range a.endpointParams {
		data[key] = values
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
			slog.WarnContext(ctx, "failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("token request returned status %d%s",
			resp.StatusCode, oauthErrorDetail(resp.Body))
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
