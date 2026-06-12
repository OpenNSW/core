// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package auth

import (
	"fmt"
	"net/http"
)

type APIKeyConfig struct {
	Key   string `json:"key"`
	Value Secret `json:"value"`
}

type APIKey struct {
	cfg APIKeyConfig
}

func NewAPIKey(cfg APIKeyConfig) *APIKey {
	return &APIKey{cfg: cfg}
}

func (a *APIKey) Apply(req *http.Request) error {
	val, err := a.cfg.Value.Resolve(req.Context())
	if err != nil {
		return fmt.Errorf("remote/auth: apikey resolution failed: %w", err)
	}
	req.Header.Set(a.cfg.Key, val)
	return nil
}
