// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package drivers

import (
	"errors"
	"mime"
	"strings"
)

// ErrNotFound is returned by a driver that looks a file up before it issues a
// download URL, for a file the backend does not hold. Callers can use
// errors.Is(err, drivers.ErrNotFound) to detect it.
var ErrNotFound = errors.New("file not found")

// ForcesDownload reports whether a file stored as contentType is served as a
// download instead of being opened in the browser. That is XML in any form: a
// browser renders it, and XML with an XHTML body or an XSLT stylesheet runs
// script from the origin the file is served on.
//
// text/html is not covered. Documents a service generates are stored as HTML
// and are meant to be read in the browser, so serving HTML safely is a
// separate question.
func ForcesDownload(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil && mediaType == "" {
		// A type whose parameters are malformed still names its media type.
		mediaType, _, _ = strings.Cut(contentType, ";")
		mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	}
	return mediaType == "text/xml" || mediaType == "application/xml" || strings.HasSuffix(mediaType, "+xml")
}
