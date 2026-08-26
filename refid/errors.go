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
)
