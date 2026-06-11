// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package artifact

import "errors"

// ErrNotFound is returned when the requested artifact, version, or key is missing.
var ErrNotFound = errors.New("artifact not found")
