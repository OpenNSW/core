// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package refid

import "context"

// Store is the persistence interface for durable, atomic sequence counters.
// Each distinct scope key gets its own counter, starting at 1.
//
// The refid/postgres and refid/sqlite subpackages each provide a raw-SQL
// (no ORM) implementation. Any caller that needs a different backend (Redis,
// in-memory for tests, etc.) can provide their own implementation.
type Store interface {
	// Next atomically increments the counter for the given scope key and
	// returns the new value, provided the current counter is less than max.
	// If the counter would exceed max, Next returns ErrCounterOverflow
	// without incrementing the counter in storage.
	//
	// Implementations must be safe for concurrent use by multiple goroutines,
	// including across multiple process instances sharing the same backing
	// store.
	Next(ctx context.Context, scopeKey string, max int64) (int64, error)
}
