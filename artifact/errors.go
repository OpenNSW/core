// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package artifact

import "errors"

// ErrNotFound is returned when the requested artifact, version, or key is missing.
var ErrNotFound = errors.New("artifact not found")

// ErrInvalid is returned when an artifact was loaded but does not parse as the requested kind.
// Loading it again gives the same result, unlike a loader I/O error.
var ErrInvalid = errors.New("artifact invalid")
