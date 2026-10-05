// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package callbacktoken encodes the address of one step of a task into the opaque token a plugin
// sends to an external system, and decodes it when the system calls back.
//
// The token names the step, not just the task, so a callback that arrives after the task has moved
// on is recognised as stale and rejected instead of completing whichever step is active now. It
// also serves as the external system's idempotency key: it is the same for every retry of one
// step's dispatch and different for every step.
//
// Format: the version character "1" followed by the unpadded base64url encoding of the 16 bytes of
// the task ID and the 16 bytes of the step ID, always 44 characters. The leading version lets a
// signed format (an HMAC and a key ID) be added later without changing consumers, which treat the
// token as an opaque string.
package callbacktoken

import (
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

const (
	// Version is the first character of every token produced by this package.
	Version = '1'
	// Length is the length of every token: the version character and 43 base64url characters.
	Length = 44
)

// ErrInvalid is returned by Decode for a token that is not one this package produced: a bad length,
// an unknown version or characters outside the base64url alphabet. A caller serving HTTP should
// answer it with 400.
var ErrInvalid = errors.New("invalid callback token")

var encoding = base64.RawURLEncoding

// Encode returns the token for a step of a task. Both IDs must be UUIDs.
func Encode(taskID, stepID string) (string, error) {
	task, err := uuid.Parse(taskID)
	if err != nil {
		return "", fmt.Errorf("task ID %q is not a UUID: %w", taskID, err)
	}
	step, err := uuid.Parse(stepID)
	if err != nil {
		return "", fmt.Errorf("step ID %q is not a UUID: %w", stepID, err)
	}
	raw := make([]byte, 0, 32)
	raw = append(raw, task[:]...)
	raw = append(raw, step[:]...)
	return string(Version) + encoding.EncodeToString(raw), nil
}

// Decode returns the task ID and step ID a token names, as canonical lower-case UUID strings.
func Decode(token string) (taskID, stepID string, err error) {
	if len(token) != Length {
		return "", "", fmt.Errorf("%w: length %d, want %d", ErrInvalid, len(token), Length)
	}
	if token[0] != Version {
		return "", "", fmt.Errorf("%w: unknown version %q", ErrInvalid, token[0])
	}
	raw, err := encoding.DecodeString(token[1:])
	if err != nil || len(raw) != 32 {
		return "", "", fmt.Errorf("%w: not base64url", ErrInvalid)
	}
	task, err := uuid.FromBytes(raw[:16])
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	step, err := uuid.FromBytes(raw[16:])
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return task.String(), step.String(), nil
}
