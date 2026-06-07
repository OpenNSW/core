package notifications

import (
	"context"
	"errors"
	"fmt"
)

// Manager routes notification requests to registered providers.
// It is safe for concurrent use after construction.
type Manager struct {
	providers map[ChannelType]Provider
}

// NewManager loads the provider config from cfg.Path, configures each provider
// from its JSON blob, and returns a ready Manager. Returns an error if cfg is
// invalid, the config file cannot be read, a provider's key is missing from the
// file, or any provider's Configure call fails.
func NewManager(cfg Config, providers ...Provider) (*Manager, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid notification config: %w", err)
	}

	cfgMap, err := loadConfigMap(cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("load notification config: %w", err)
	}

	m := &Manager{
		providers: make(map[ChannelType]Provider, len(providers)),
	}

	for _, p := range providers {
		if p == nil {
			return nil, errors.New("nil provider passed to NewManager")
		}
		raw, ok := cfgMap[string(p.Type())]
		if !ok {
			return nil, fmt.Errorf("no config for %q provider in %s", p.Type(), cfg.Path)
		}
		if err := p.Configure(raw); err != nil {
			return nil, fmt.Errorf("configure %q provider: %w", p.Type(), err)
		}
		if _, dup := m.providers[p.Type()]; dup {
			return nil, fmt.Errorf("duplicate notification provider for channel %q", p.Type())
		}
		m.providers[p.Type()] = p
	}

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
