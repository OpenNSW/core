package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/OpenNSW/nsw/pkg/remote/auth"
)

type AuthConfig struct {
	Type    string          `json:"type"` // "api_key", "oauth2", "bearer"
	Options json.RawMessage `json:"options"`
}

type ServiceConfig struct {
	ID      string      `json:"id"`
	URL     string      `json:"url"`
	Timeout string      `json:"timeout"`
	Auth    *AuthConfig `json:"auth,omitempty"`
}

type Registry struct {
	Version  string          `json:"version"`
	Services []ServiceConfig `json:"services"`
}

type Manager struct {
	mu      sync.RWMutex
	configs map[string]ServiceConfig
	clients map[string]*Client
}

func NewManager() *Manager {
	return &Manager{
		configs: make(map[string]ServiceConfig),
		clients: make(map[string]*Client),
	}
}

func (m *Manager) LoadServices(filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("remote: failed to read services file: %w", err)
	}

	var registry Registry
	if err := json.Unmarshal(data, &registry); err != nil {
		return fmt.Errorf("remote: failed to unmarshal services registry: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Reset clients when loading new configs
	m.clients = make(map[string]*Client)
	m.configs = make(map[string]ServiceConfig)
	for _, cfg := range registry.Services {
		// Normalize URL by removing trailing slash for consistent matching
		cfg.URL = strings.TrimSuffix(cfg.URL, "/")
		m.configs[cfg.ID] = cfg
	}

	return nil
}

func (m *Manager) Call(ctx context.Context, serviceID string, req Request, response interface{}) error {
	var client *Client
	var err error

	if serviceID != "" {
		client, err = m.GetClient(serviceID)
	} else {
		// Attempt to resolve service by URL for backward compatibility
		var resolvedID string
		client, resolvedID, err = m.GetClientByURL(req.Path)
		if err == nil {
			// Update the request path to be relative if it matched a service baseURL
			m.mu.RLock()
			if cfg, ok := m.configs[resolvedID]; ok {
				req.Path = strings.TrimPrefix(req.Path, cfg.URL)
			}
			m.mu.RUnlock()
		}
	}

	if err != nil {
		return err
	}

	return client.JSONRequest(ctx, req, response)
}

func (m *Manager) GetClientByURL(rawURL string) (*Client, string, error) {
	if !strings.HasPrefix(rawURL, "http") {
		return nil, "", fmt.Errorf("remote: cannot resolve service from relative path: %s", rawURL)
	}

	parsedReq, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", fmt.Errorf("remote: invalid URL: %w", err)
	}

	m.mu.RLock()
	// No defer here because we need to release for GetClient call

	for id, cfg := range m.configs {
		parsedBase, err := url.Parse(cfg.URL)
		if err != nil {
			continue
		}

		// Check if Scheme and Host match
		if parsedReq.Scheme == parsedBase.Scheme && parsedReq.Host == parsedBase.Host {
			// Also ensure the path matches the base path if provided
			if strings.HasPrefix(parsedReq.Path, parsedBase.Path) {
				m.mu.RUnlock()
				client, err := m.GetClient(id)
				if err != nil {
					// If a service matches but fails to initialize, it's a configuration error.
					// We should return this error instead of continuing the search.
					return nil, "", fmt.Errorf("remote: failed to create client for matched service %q: %w", id, err)
				}
				return client, id, nil
			}
		}
	}
	m.mu.RUnlock()

	return nil, "", fmt.Errorf("remote: no registered service found for URL: %s", rawURL)
}

func (m *Manager) GetClient(id string) (*Client, error) {
	m.mu.RLock()
	client, ok := m.clients[id]
	m.mu.RUnlock()

	if ok {
		return client, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if client, ok := m.clients[id]; ok {
		return client, nil
	}

	cfg, ok := m.configs[id]
	if !ok {
		return nil, fmt.Errorf("remote: service %q not found in registry", id)
	}

	var opts []Option

	if cfg.Timeout != "" {
		d, err := time.ParseDuration(cfg.Timeout)
		if err != nil {
			return nil, fmt.Errorf("remote: invalid timeout %q for service %q: %w", cfg.Timeout, id, err)
		}
		opts = append(opts, WithTimeout(d))
	}

	if cfg.Auth != nil {
		authenticator, err := m.createAuthenticator(cfg.Auth)
		if err != nil {
			return nil, fmt.Errorf("remote: failed to create authenticator for %q: %w", id, err)
		}
		opts = append(opts, WithAuthenticator(authenticator))
	}

	newClient := NewClient(cfg.URL, opts...)
	m.clients[id] = newClient

	return newClient, nil
}

func (m *Manager) createAuthenticator(cfg *AuthConfig) (auth.Authenticator, error) {
	switch cfg.Type {
	case "api_key":
		var apiCfg auth.APIKeyConfig
		if err := json.Unmarshal(cfg.Options, &apiCfg); err != nil {
			return nil, fmt.Errorf("invalid api_key options: %w", err)
		}
		return auth.NewAPIKey(apiCfg), nil

	case "bearer":
		var bearerCfg auth.BearerConfig
		if err := json.Unmarshal(cfg.Options, &bearerCfg); err != nil {
			return nil, fmt.Errorf("invalid bearer options: %w", err)
		}
		return auth.NewBearer(bearerCfg), nil

	case "oauth2":
		var oauthCfg auth.OAuth2Config
		if err := json.Unmarshal(cfg.Options, &oauthCfg); err != nil {
			return nil, fmt.Errorf("invalid oauth2 options: %w", err)
		}
		return auth.NewOAuth2(oauthCfg), nil

	default:
		return nil, fmt.Errorf("unsupported auth type: %s", cfg.Type)
	}
}

func (m *Manager) ListServices() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.configs))
	for id := range m.configs {
		ids = append(ids, id)
	}
	return ids
}
