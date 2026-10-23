// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import (
	"fmt"
	"mime"
	"slices"
	"strings"

	"github.com/OpenNSW/core/storage/drivers"
)

// DefaultInlineTypes returns the media types a download opens in the browser
// with unless WithInlineTypes lists others: documents and images a browser
// shows without running anything from them.
func DefaultInlineTypes() []string {
	return []string{
		"application/pdf",
		"image/png",
		"image/jpeg",
		"image/gif",
		"image/webp",
		"text/plain",
	}
}

// ValidMediaType returns an error unless t is a bare, lower-case type/subtype
// such as "application/pdf": no parameters and no wildcard. It is the form a
// Service compares media types in.
func ValidMediaType(t string) error {
	mediaType, params, err := mime.ParseMediaType(t)
	if err != nil || len(params) > 0 || mediaType != t || !strings.Contains(t, "/") || strings.Contains(t, "*") {
		return fmt.Errorf("%q is not a bare, lower-case MIME type such as \"application/pdf\"", t)
	}
	return nil
}

// CheckUploadTypes returns an error naming each type in allowed, the types
// given to WithAllowedUploadTypes, that a Service given the inline types
// would also open in the browser although it is not one of
// DefaultInlineTypes: a client could upload a file of that type and run script
// from it when someone opens it. NewService makes the same check; a host calls
// this first to report a bad configuration without a panic.
func CheckUploadTypes(allowed, inline []string) error {
	if unsafe := newInlinePolicy(inline).notInert(allowed); len(unsafe) > 0 {
		return errUploadableInline(unsafe)
	}
	return nil
}

func errUploadableInline(types []string) error {
	return fmt.Errorf("storage: %s may be uploaded and also opens in the browser; remove it from the upload types or from the inline types", strings.Join(types, ", "))
}

// inlinePolicy is the set of media types a download opens in the browser
// with. A browser interprets many types as documents: HTML, XML (XHTML and
// XSLT included) and SVG run script from the origin they are served on. So a
// file opens inline only when its type is in the set, and every other file is
// saved.
type inlinePolicy map[string]struct{}

// defaultInline is the policy for DefaultInlineTypes.
var defaultInline = newInlinePolicy(DefaultInlineTypes())

// newInlinePolicy returns the policy that opens exactly the given types
// inline. Parameters and case are ignored, in the given types and in the types
// the policy is asked about.
func newInlinePolicy(types []string) inlinePolicy {
	p := make(inlinePolicy, len(types))
	for _, t := range types {
		if mt := mediaType(t); mt != "" {
			p[mt] = struct{}{}
		}
	}
	return p
}

// disposition returns the disposition a file stored as contentType is served
// with.
func (p inlinePolicy) disposition(contentType string) string {
	if _, ok := p[mediaType(contentType)]; ok {
		return drivers.DispositionInline
	}
	return drivers.DispositionAttachment
}

// notInert returns the types in candidates that p opens inline although
// DefaultInlineTypes does not name them, without repeats.
func (p inlinePolicy) notInert(candidates []string) []string {
	var out []string
	for _, c := range candidates {
		mt := mediaType(c)
		if _, ok := p[mt]; !ok {
			continue
		}
		if _, inert := defaultInline[mt]; inert || slices.Contains(out, c) {
			continue
		}
		out = append(out, c)
	}
	slices.Sort(out)
	return out
}

// mediaType returns the lower-case type/subtype of a Content-Type value,
// without its parameters, or "" when the value names no media type. A value
// whose parameters are malformed still names its media type.
func mediaType(contentType string) string {
	mt, _, _ := mime.ParseMediaType(contentType)
	return mt
}
