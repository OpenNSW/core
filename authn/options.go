// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package authn

// Option configures optional behavior on TokenExtractor construction.
//
// Options never fail on their own: they record intent verbatim and every check
// runs afterwards in validateConfig, which NewTokenExtractor reports as a
// construction error.
type Option func(*TokenExtractor)

// WithClaims configures the standard identity claims (see ClaimsConfig). A
// later call replaces an earlier one.
func WithClaims(claims ClaimsConfig) Option {
	return func(te *TokenExtractor) { te.claims = claims }
}
