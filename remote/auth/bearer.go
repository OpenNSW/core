// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package auth

import (
	"net/http"
)

type BearerConfig struct {
	Token string `json:"token"`
}

type Bearer struct {
	token string
}

// build constructs the authenticator.
func (c BearerConfig) build() (Authenticator, error) {
	return NewBearer(c.Token), nil
}

// NewBearer builds a bearer-token authenticator from an already-resolved token.
func NewBearer(token string) *Bearer {
	return &Bearer{token: token}
}

func (a *Bearer) Apply(req *http.Request) error {
	req.Header.Set("Authorization", "Bearer "+a.token)
	return nil
}
