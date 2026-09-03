// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package refid

import "context"

// SequenceStore is the persistence interface for durable, atomic sequence counters.
// Each distinct scope key gets its own counter, starting at 1.
//
// The refid/store/postgres subpackage provides a PostgreSQL implementation
// via NewPostgresStore. Any caller that needs a different backend (Redis,
// in-memory for tests, etc.) can provide their own implementation.
type SequenceStore interface {
	// Next atomically increments the counter for the given scope key and returns
	// the new value, provided the current counter is less than max.
	// If the counter would exceed max, Next returns ErrCounterOverflow without
	// incrementing the counter in storage.
	Next(ctx context.Context, scopeKey string, max int64) (int64, error)
}
