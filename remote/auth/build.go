// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package auth

import (
	"errors"
	"fmt"
)

// Type names a built-in authentication strategy.
type Type string

const (
	TypeAPIKey Type = "apiKey"
	TypeBearer Type = "bearer"
	TypeOAuth2 Type = "oauth2"
)

// Config selects a built-in strategy and carries its settings. Type names the
// strategy, and the matching block — and only that block — must be set:
//
//	{"type": "bearer", "bearer": {"token": "my-token"}}
//
// It is plain data with json and yaml tags (the same camelCase keys in both), so it
// decodes from whichever format the application keeps its configuration in.
type Config struct {
	Type   Type          `json:"type" yaml:"type"`
	APIKey *APIKeyConfig `json:"apiKey,omitempty" yaml:"apiKey,omitempty"`
	Bearer *BearerConfig `json:"bearer,omitempty" yaml:"bearer,omitempty"`
	OAuth2 *OAuth2Config `json:"oauth2,omitempty" yaml:"oauth2,omitempty"`
}

// authConfig is implemented by each strategy's settings struct. build validates the
// settings and constructs the authenticator.
type authConfig interface {
	build() (Authenticator, error)
}

// Build constructs the authenticator cfg describes. It is the single entry point
// callers (e.g. the remote Manager) use, so they need not know about individual
// strategies. It performs no I/O: secret values are used verbatim, and resolving them
// (from env vars, files, a secret store, ...) is the caller's job.
func Build(cfg Config) (Authenticator, error) {
	strategy, err := cfg.strategy()
	if err != nil {
		return nil, err
	}
	return strategy.build()
}

// strategy returns the settings block named by Type, rejecting a missing block or
// a stray block for a different strategy — either is a misconfiguration that
// would otherwise be silently ignored.
func (c Config) strategy() (authConfig, error) {
	set := 0
	for _, isSet := range []bool{c.APIKey != nil, c.Bearer != nil, c.OAuth2 != nil} {
		if isSet {
			set++
		}
	}

	var strategy authConfig
	switch c.Type {
	case TypeAPIKey:
		if c.APIKey != nil {
			strategy = c.APIKey
		}
	case TypeBearer:
		if c.Bearer != nil {
			strategy = c.Bearer
		}
	case TypeOAuth2:
		if c.OAuth2 != nil {
			strategy = c.OAuth2
		}
	case "":
		return nil, errors.New("missing auth type")
	default:
		return nil, fmt.Errorf("unsupported auth type: %q", c.Type)
	}

	if strategy == nil {
		return nil, fmt.Errorf("missing %q settings for auth type %q", c.Type, c.Type)
	}
	if set > 1 {
		return nil, fmt.Errorf("auth type %q must set only its own %q settings", c.Type, c.Type)
	}
	return strategy, nil
}
