// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package refid

import "context"

// RandomStore is the persistence interface for tracking issued random ID
// values so that collisions can be detected. Each distinct scope key has its
// own independent set of reserved values.
//
// The refid/store/postgres and refid/store/sqlite subpackages each provide
// an implementation via their NewRandom constructor. Any caller that needs a
// different backend (Redis, in-memory for tests, etc.) can provide their own
// implementation.
type RandomStore interface {
	// Reserve atomically records value as issued under scopeKey. If value is
	// already reserved under the same scope key, Reserve returns
	// ErrRandomCollision without side effects, and the caller should generate
	// a new value and retry.
	Reserve(ctx context.Context, scopeKey, value string) error
}
