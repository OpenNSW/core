// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package notification

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// Manager routes notification requests to registered providers.
// It is safe for concurrent use after construction.
type Manager struct {
	providers map[ChannelType]Provider
}

// ErrNoProviders is returned by NewManager when it is given no providers.
var ErrNoProviders = errors.New("notification: at least one provider is required")

// NewManager returns a Manager that routes each request to the provider for
// its channel. The providers are already configured by their constructors.
// Returns an error if none is given, one is nil, or two handle the same
// channel.
func NewManager(providers ...Provider) (*Manager, error) {
	if len(providers) == 0 {
		return nil, ErrNoProviders
	}

	m := &Manager{
		providers: make(map[ChannelType]Provider, len(providers)),
	}

	for _, p := range providers {
		if p == nil {
			return nil, errors.New("nil provider passed to NewManager")
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
