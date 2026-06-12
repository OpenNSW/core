// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package auth

import (
	"fmt"
	"net/http"
)

type BearerConfig struct {
	Token Secret `json:"token"`
}

type Bearer struct {
	cfg BearerConfig
}

func NewBearer(cfg BearerConfig) *Bearer {
	return &Bearer{cfg: cfg}
}

func (a *Bearer) Apply(req *http.Request) error {
	token, err := a.cfg.Token.Resolve(req.Context())
	if err != nil {
		return fmt.Errorf("remote/auth: bearer token resolution failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}
