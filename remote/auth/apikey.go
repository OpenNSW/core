// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package auth

import (
	"fmt"
	"net/http"

	"github.com/OpenNSW/core/secret"
)

type APIKeyConfig struct {
	Key   string           `json:"key"`
	Value secret.SecretRef `json:"value"`
}

// build resolves the configured value (failing loud on an unresolvable reference)
// and constructs the authenticator.
func (c APIKeyConfig) build() (Authenticator, error) {
	value, err := c.Value.Resolve()
	if err != nil {
		return nil, fmt.Errorf("api_key value: %w", err)
	}
	return NewAPIKey(c.Key, value), nil
}

type APIKey struct {
	key   string
	value string
}

// NewAPIKey builds an API-key authenticator from already-resolved values.
func NewAPIKey(key, value string) *APIKey {
	return &APIKey{key: key, value: value}
}

func (a *APIKey) Apply(req *http.Request) error {
	req.Header.Set(a.key, a.value)
	return nil
}
