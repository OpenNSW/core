// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package remote

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/OpenNSW/core/remote/auth"
)

// TLSSettings configures transport-level client authentication (mTLS) for a
// service. Both values are filesystem paths to PEM files, read on each TLS
// handshake (see WithClientCertificateFiles) so rotated material is picked up
// without a restart.
type TLSSettings struct {
	ClientCertFile string `json:"clientCertFile" yaml:"clientCertFile"`
	ClientKeyFile  string `json:"clientKeyFile" yaml:"clientKeyFile"`
}

// ServiceConfig declares one remote service. It is plain data with json and yaml
// tags: the application decodes it from wherever it keeps its configuration and
// hands the result to Manager.SetServices.
type ServiceConfig struct {
	ID      string       `json:"id" yaml:"id"`
	URL     string       `json:"url" yaml:"url"`
	Timeout string       `json:"timeout" yaml:"timeout"`
	Auth    *auth.Config `json:"auth,omitempty" yaml:"auth,omitempty"`
	TLS     *TLSSettings `json:"tls,omitempty" yaml:"tls,omitempty"`
}

type Manager struct {
	mu             sync.RWMutex
	configs        map[string]ServiceConfig
	clients        map[string]*Client
	authenticators map[string]auth.Authenticator
}

func NewManager() *Manager {
	return &Manager{
		configs:        make(map[string]ServiceConfig),
		clients:        make(map[string]*Client),
		authenticators: make(map[string]auth.Authenticator),
	}
}

// SetServices validates services and replaces the manager's registry with them.
// It is all-or-nothing: on error the existing registry is left untouched. Cached
// clients are discarded, so subsequent calls use the new configuration.
func (m *Manager) SetServices(services ...ServiceConfig) error {
	// Prepare the new state before taking the lock, so a failure leaves the
	// manager's existing state untouched rather than corrupted.
	configs := make(map[string]ServiceConfig, len(services))
	authenticators := make(map[string]auth.Authenticator)
	for _, cfg := range services {
		// Normalize URL by removing trailing slash for consistent matching
		cfg.URL = strings.TrimSuffix(cfg.URL, "/")
		configs[cfg.ID] = cfg

		// Build authenticators eagerly so any misconfiguration (unsupported
		// type, missing settings, reserved endpoint params) fails loud at
		// startup rather than on the first request.
		if cfg.Auth != nil {
			authenticator, err := auth.Build(*cfg.Auth)
			if err != nil {
				return fmt.Errorf("remote: failed to configure auth for service %q: %w", cfg.ID, err)
			}
			authenticators[cfg.ID] = authenticator
		}

		// Validate the tls config shape now, but defer reading the PEM files to
		// the first client build (GetClient): mTLS material is operator-supplied
		// and often absent in dev setups, and deferring lets it be provided
		// after boot without failing startup for every other service.
		if cfg.TLS != nil && (cfg.TLS.ClientCertFile == "" || cfg.TLS.ClientKeyFile == "") {
			return fmt.Errorf("remote: service %q tls config requires both clientCertFile and clientKeyFile", cfg.ID)
		}
	}

	// Swap in the validated state atomically, and reset the client cache.
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clients = make(map[string]*Client)
	m.configs = configs
	m.authenticators = authenticators

	slog.Info("remote: services loaded", "count", len(configs))
	return nil
}

func (m *Manager) Call(ctx context.Context, serviceID string, req Request, response interface{}) error {
	client, err := m.GetClient(serviceID)
	if err != nil {
		return err
	}

	return client.Request(ctx, req, response)
}

// CallRaw sends a raw-bodied request (e.g. a SOAP/XML envelope, via
// req.Body = RawBody{...}) to a registered service. See Client.RawRequest for
// the error semantics: a non-2xx status is returned in the response, not as
// an error.
func (m *Manager) CallRaw(ctx context.Context, serviceID string, req Request) (*RawResponse, error) {
	client, err := m.GetClient(serviceID)
	if err != nil {
		return nil, err
	}
	return client.RawRequest(ctx, req)
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

	// Authenticators are built once, when services are set (see SetServices).
	if authenticator, ok := m.authenticators[id]; ok {
		opts = append(opts, WithAuthenticator(authenticator))
	}

	// The client certificate is read from disk on each TLS handshake (see
	// WithClientCertificateFiles): a missing PEM fails the call, not startup,
	// and rotated material is picked up by new connections without a restart.
	if cfg.TLS != nil {
		opts = append(opts, WithClientCertificateFiles(cfg.TLS.ClientCertFile, cfg.TLS.ClientKeyFile))
	}

	newClient := NewClient(cfg.URL, opts...)
	m.clients[id] = newClient

	return newClient, nil
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
