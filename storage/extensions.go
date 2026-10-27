// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// extensionRx matches an extension WithUploadTypeExtensions accepts: a dot and
// lower-case letters or digits, which a storage key can end in.
var extensionRx = regexp.MustCompile(`^\.[a-z0-9]+$`)

// CheckUploadTypeExtensions returns an error unless every type in byType is one
// ValidMediaType accepts and lists at least one extension, each a dot followed
// by lower-case letters or digits, such as ".pdf". WithUploadTypeExtensions
// makes the same check; a host calls this first to report a bad configuration
// without a panic.
func CheckUploadTypeExtensions(byType map[string][]string) error {
	for t, exts := range byType {
		if err := ValidMediaType(t); err != nil {
			return err
		}
		if len(exts) == 0 {
			return fmt.Errorf("%q lists no extensions", t)
		}
		for _, ext := range exts {
			if !extensionRx.MatchString(ext) {
				return fmt.Errorf("%q: %q is not a lower-case extension such as \".pdf\"", t, ext)
			}
		}
	}
	return nil
}

// extensionPolicy lists, per media type, the extensions an uploaded file's
// name may end in, the first being the one a name without an extension gets.
// A type without an entry is not checked.
type extensionPolicy map[string][]string

// newExtensionPolicy returns the policy for byType, a map
// CheckUploadTypeExtensions accepts. It copies the map, so the caller changing
// it later doesn't change the policy.
func newExtensionPolicy(byType map[string][]string) extensionPolicy {
	p := make(extensionPolicy, len(byType))
	for t, exts := range byType {
		p[t] = slices.Clone(exts)
	}
	return p
}

// match returns filename as Upload keeps it: unchanged when its extension is
// one the type lists, with the type's first extension when it has none, and an
// *ExtensionMismatchError otherwise. A type without an entry is not checked.
// The type is read as a download reads it (mediaType), so malformed parameters
// don't hide it.
func (p extensionPolicy) match(filename, contentType string) (string, error) {
	mt := mediaType(contentType)
	exts := p[mt]
	if len(exts) == 0 {
		return filename, nil
	}
	// The extension newKey keeps in the key.
	switch ext := filepath.Ext(filename); {
	case ext == "":
		return filename + exts[0], nil
	case slices.Contains(exts, strings.ToLower(ext)):
		return filename, nil
	default:
		return "", &ExtensionMismatchError{Extension: ext, MediaType: mt}
	}
}
