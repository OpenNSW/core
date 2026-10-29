// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package drivers

import "errors"

// ErrNotFound is returned by a driver for a file the backend does not hold.
// Callers can use errors.Is(err, drivers.ErrNotFound) to detect it.
var ErrNotFound = errors.New("file not found")

// The Content-Disposition values a driver serves a download with.
const (
	// DispositionInline lets the browser open the file.
	DispositionInline = "inline"
	// DispositionAttachment makes the browser save the file instead of
	// opening it.
	DispositionAttachment = "attachment"
)

// ValidDisposition reports whether d is DispositionInline or
// DispositionAttachment.
func ValidDisposition(d string) bool {
	return d == DispositionInline || d == DispositionAttachment
}

// ObjectInfo describes a stored file.
type ObjectInfo struct {
	// ContentType is the type the file was stored with.
	ContentType string
	// Size is the file's size in bytes.
	Size int64
}
