// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package auth

import (
	"net/http"
)

type APIKeyConfig struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// build constructs the authenticator.
func (c APIKeyConfig) build() (Authenticator, error) {
	return NewAPIKey(c.Key, c.Value), nil
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
