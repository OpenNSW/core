// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import "context"

// Principal is the minimal identity contract storage needs to gate authenticated
// endpoints. It is deliberately tiny so an authentication layer's context type
// (e.g. *authn.AuthContext) satisfies it structurally, without storage importing
// that package — mirrors the authz.Principal/Extractor pattern.
type Principal interface {
	// Subject returns a stable identifier for the caller, used for audit logging.
	Subject() string
}

// Extractor retrieves the authenticated Principal from a request context. It is
// injected at construction so this package stays decoupled from any specific
// authentication implementation. It must return (nil, false) when the request is
// unauthenticated.
type Extractor func(ctx context.Context) (Principal, bool)
