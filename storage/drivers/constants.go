// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package drivers

import "time"

// DefaultPresignTTL is the default time-to-live for presigned upload and download URLs.
const DefaultPresignTTL = 15 * time.Minute

// DefaultMime is the fallback MIME type when none is provided.
const DefaultMime = "application/octet-stream"
