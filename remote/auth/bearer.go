// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package auth

import (
	"fmt"
	"net/http"
)

type BearerConfig struct {
	Token SecretRef `json:"token"`
}

type Bearer struct {
	token string
}

// build resolves the configured token (failing loud on an unresolvable reference)
// and constructs the authenticator.
func (c BearerConfig) build() (Authenticator, error) {
	token, err := c.Token.Resolve()
	if err != nil {
		return nil, fmt.Errorf("bearer token: %w", err)
	}
	return NewBearer(token), nil
}

// NewBearer builds a bearer-token authenticator from an already-resolved token.
func NewBearer(token string) *Bearer {
	return &Bearer{token: token}
}

func (a *Bearer) Apply(req *http.Request) error {
	req.Header.Set("Authorization", "Bearer "+a.token)
	return nil
}
