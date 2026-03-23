package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/OpenNSW/nsw/pkg/remote/auth"
)

// RetryConfig defines the strategy for retrying failed requests.
type RetryConfig struct {
	MaxRetries      int           // Maximum number of retries (0 = no retries)
	InitialBackoff  time.Duration // Time to wait before the first retry
	MaxBackoff      time.Duration // Maximum wait time between retries
	RetryableStatus []int         // HTTP status codes that should trigger a retry
}

// DefaultRetryConfig provides a sensible default for most services.
var DefaultRetryConfig = RetryConfig{
	MaxRetries:     3,
	InitialBackoff: 500 * time.Millisecond,
	MaxBackoff:     10 * time.Second,
	RetryableStatus: []int{
		http.StatusTooManyRequests,     // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout,      // 504
	},
}

// Request bundles all the caller-provided parts of an outbound call.
type Request struct {
	Method  string
	Path    string
	Query   url.Values
	Body    any
	Headers map[string]string
	Retry   *RetryConfig // If nil, no retries will be performed
}

type Client struct {
	httpClient    *http.Client
	baseURL       string
	authenticator auth.Authenticator
	headers       map[string]string
	logger        *slog.Logger
}

func NewClient(baseURL string, opts ...Option) *Client {
	if baseURL == "" {
		panic("remote: base URL is required")
	}

	c := &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: strings.TrimSuffix(baseURL, "/"),
		logger:  slog.Default(),
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

func (c *Client) Do(ctx context.Context, method, path string, body io.Reader, extraHeaders map[string]string, retry *RetryConfig) (*http.Response, error) {
	// Re-usable body for retries
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = io.ReadAll(body)
		if err != nil {
			return nil, fmt.Errorf("remote: failed to read body for possible retries: %w", err)
		}
	}

	return c.executeWithRetry(ctx, method, path, bodyBytes, extraHeaders, retry)
}

func (c *Client) executeWithRetry(ctx context.Context, method, path string, body []byte, headers map[string]string, retry *RetryConfig) (*http.Response, error) {
	if retry == nil {
		return c.executeOnce(ctx, method, path, body, headers)
	}

	var lastResp *http.Response
	var lastErr error
	backoff := retry.InitialBackoff

	for attempt := 0; attempt <= retry.MaxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		lastResp, lastErr = c.executeOnce(ctx, method, path, body, headers)

		shouldRetry := false
		if lastErr != nil {
			// Always retry on network errors (e.g., timeouts, connection refused)
			shouldRetry = true
		} else if lastResp != nil {
			for _, status := range retry.RetryableStatus {
				if lastResp.StatusCode == status {
					shouldRetry = true
					break
				}
			}
		}

		if !shouldRetry || attempt == retry.MaxRetries {
			return lastResp, lastErr
		}

		if lastResp != nil {
			_ = lastResp.Body.Close()
		}

		c.logger.Info("remote: retrying request",
			"method", method,
			"path", path,
			"attempt", attempt+1,
			"wait", backoff,
			"error", lastErr,
		)

		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		}

		backoff *= 2
		if backoff > retry.MaxBackoff {
			backoff = retry.MaxBackoff
		}
	}

	return lastResp, lastErr
}

func (c *Client) executeOnce(ctx context.Context, method, path string, body []byte, extraHeaders map[string]string) (*http.Response, error) {
	finalURL := path

	// Handle URL construction and verification
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		// Absolute URL validation
		if c.baseURL != "" {
			base, err := url.Parse(c.baseURL)
			if err != nil {
				return nil, fmt.Errorf("remote: invalid base URL: %w", err)
			}

			provided, err := url.Parse(path)
			if err != nil {
				return nil, fmt.Errorf("remote: invalid absolute URL: %w", err)
			}

			// Ensure Scheme and Host match to prevent SSRF or credential leakage
			if base.Scheme != provided.Scheme || base.Host != provided.Host {
				return nil, fmt.Errorf("remote: absolute URL host %q does not match configured service host %q", provided.Host, base.Host)
			}
		}
	} else {
		// Relative path handling
		// Ensure baseURL ends with / and path doesn't start with / to avoid double slashes or missing slashes
		base := strings.TrimSuffix(c.baseURL, "/")
		p := strings.TrimPrefix(path, "/")
		finalURL = base + "/" + p
	}

	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewBuffer(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, finalURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("remote: failed to create request: %w", err)
	}

	// Apply JSON Content-Type if body is present
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	if c.authenticator != nil {
		if err := c.authenticator.Apply(req); err != nil {
			return nil, fmt.Errorf("remote: auth failed: %w", err)
		}
	}

	c.logger.Info("remote: outbound request starting", "method", method, "url", finalURL)

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	duration := time.Since(start)

	if err != nil {
		c.logger.Error("remote: outbound request failed", "method", method, "url", finalURL, "duration", duration, "error", err)
		return nil, c.mapNetworkError(err)
	}

	c.logger.Info("remote: outbound request completed", "method", method, "url", finalURL, "status", resp.StatusCode, "duration", duration)

	return resp, nil
}

func (c *Client) JSONRequest(ctx context.Context, req Request, response interface{}) error {
	// Handle Query Parameters
	fullPath := req.Path
	if len(req.Query) > 0 {
		if strings.Contains(req.Path, "?") {
			fullPath += "&" + req.Query.Encode()
		} else {
			fullPath += "?" + req.Query.Encode()
		}
	}

	// Handle Body
	var bodyReader io.Reader
	if req.Body != nil {
		data, err := json.Marshal(req.Body)
		if err != nil {
			return fmt.Errorf("remote: failed to marshal payload: %w", err)
		}
		bodyReader = bytes.NewBuffer(data)
	}

	// Use the Do method which handles Auth and BaseURL injection
	resp, err := c.Do(ctx, req.Method, fullPath, bodyReader, req.Headers, req.Retry)
	if err != nil {
		return err
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Warn("failed to close response body", "error", err)
		}
	}()

	if response != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(response); err != nil {
			return fmt.Errorf("remote: failed to decode response: %w", err)
		}
	}

	if resp.StatusCode >= 400 {
		return c.handleErrorResponse(resp)
	}

	return nil
}

func (c *Client) mapNetworkError(err error) error {
	if strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "deadline exceeded") {
		return ErrTimeout
	}
	return fmt.Errorf("%w: %v", ErrRequestFailed, err)
}

func (c *Client) handleErrorResponse(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	errMsg := string(body)
	if errMsg == "" {
		errMsg = resp.Status
	}

	var baseErr error
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		baseErr = ErrUnauthorized
	case http.StatusNotFound:
		baseErr = ErrNotFound
	case http.StatusBadRequest:
		baseErr = ErrBadRequest
	case http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		baseErr = ErrServiceUnavailable
	default:
		baseErr = ErrRequestFailed
	}

	return &RemoteError{
		StatusCode: resp.StatusCode,
		Message:    errMsg,
		Wrapped:    baseErr,
	}
}
