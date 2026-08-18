// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package refid

import "context"

// SequenceStore is the persistence interface for durable, atomic sequence counters.
// Each distinct scope key gets its own counter, starting at 1.
//
// The package ships with a PostgreSQL implementation via NewPostgresStore in
// postgres_store.go. Any caller that needs a different backend (Redis,
// in-memory for tests, etc.) can provide their own implementation.
type SequenceStore interface {
	// Next atomically increments the counter for the given scope key and returns
	// the new value. The counter starts at 1 on first use (i.e. the first call
	// for a new key returns 1, not 0).
	Next(ctx context.Context, scopeKey string) (int64, error)
}
