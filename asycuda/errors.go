// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package asycuda

import "errors"

// ErrDispatchNoteNotFound indicates no dispatch note matches the given edgId
// or cdnRef. This is a permanent condition — the caller should not retry.
var ErrDispatchNoteNotFound = errors.New("dispatch note not found")

// ErrInvalidCallbackPayload indicates the ASYCUDA callback payload was
// malformed, missing required fields, or otherwise failed validation.
var ErrInvalidCallbackPayload = errors.New("invalid callback payload")
