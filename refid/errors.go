// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package refid

import "errors"

// Sentinel errors returned by the refid package.
// Use errors.Is to check for these in calling code.
var (
	// ErrUnknownIssuer is returned when Generate is called with an issuer
	// that was not present in the config used to build the registry.
	ErrUnknownIssuer = errors.New("refid: unknown issuer")

	// ErrUnknownIDType is returned when Generate is called with an idType
	// that was not declared under the given issuer.
	ErrUnknownIDType = errors.New("refid: unknown id type")

	// ErrInvalidParam is returned when a list segment's required caller-supplied
	// param is missing or its value is not in the allowed list.
	ErrInvalidParam = errors.New("refid: invalid or missing param")

	// ErrCounterOverflow is returned when the sequence counter value for a scope
	// key exceeds the number of digits allowed by the segment's padding setting.
	// For example, a counter of 1,000,001 with padding:6 would produce a 7-digit
	// string, breaking the expected ID format. Callers should alert operations
	// when this occurs; the scope key is likely configured too broadly.
	ErrCounterOverflow = errors.New("refid: sequence counter exceeds padding width")

	// ErrRandomCollision is returned by RandomStore.Reserve when the given
	// value is already reserved under the same scope key. A random segment
	// treats this as a signal to generate a new value and retry, up to its
	// configured maxAttempts.
	ErrRandomCollision = errors.New("refid: random value already reserved for this scope")

	// ErrRandomExhausted is returned when a random segment could not find an
	// unreserved value within its configured maxAttempts. This usually means
	// the charset/length combination is too small for the volume of IDs being
	// issued in that scope; widen the charset or length, or narrow the scope.
	ErrRandomExhausted = errors.New("refid: random segment exhausted attempts without finding an unreserved value")
)
