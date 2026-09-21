// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
)

// Manager routes notification requests to registered providers.
// It is safe for concurrent use after construction.
type Manager struct {
	providers map[ChannelType]Provider
}

// NewManager configures each provider from its block in cfg.Providers and
// returns a ready Manager. Returns an error if cfg is invalid, a provider's
// channel is missing from cfg.Providers, or any provider's Configure call
// fails.
func NewManager(cfg Config, providers ...Provider) (*Manager, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid notification config: %w", err)
	}

	m := &Manager{
		providers: make(map[ChannelType]Provider, len(providers)),
	}

	for _, p := range providers {
		if p == nil {
			return nil, errors.New("nil provider passed to NewManager")
		}
		block, ok := cfg.Providers[p.Type()]
		if !ok {
			return nil, fmt.Errorf("no config for %q provider", p.Type())
		}
		// Providers still configure from json.RawMessage (unchanged interface,
		// so every existing Provider keeps working); the block just arrives
		// from cfg.Providers now instead of a standalone JSON file.
		raw, err := json.Marshal(block)
		if err != nil {
			return nil, fmt.Errorf("marshal %q provider config: %w", p.Type(), err)
		}
		if err := p.Configure(raw); err != nil {
			return nil, fmt.Errorf("configure %q provider: %w", p.Type(), err)
		}
		if _, dup := m.providers[p.Type()]; dup {
			return nil, fmt.Errorf("duplicate notification provider for channel %q", p.Type())
		}
		m.providers[p.Type()] = p
	}

	providerTypes := make([]string, 0, len(m.providers))
	for t := range m.providers {
		providerTypes = append(providerTypes, string(t))
	}
	slog.Info("notification manager initialized", "providers", providerTypes)
	return m, nil
}

// Send validates req and dispatches it to the registered provider for req.Channel.
// Returns an error if the request is invalid, no provider is registered for the
// channel, or the provider's Send call fails.
func (m *Manager) Send(ctx context.Context, req Request) error {
	if m == nil {
		return errors.New("notifications manager is not initialized")
	}

	if err := req.Validate(); err != nil {
		return fmt.Errorf("invalid notification request: %w", err)
	}

	p, ok := m.providers[req.Channel]
	if !ok {
		return fmt.Errorf("no provider registered for channel %q", req.Channel)
	}

	return p.Send(ctx, req)
}
