// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package htmlgen

import "errors"

// Sentinel errors returned by the htmlgen package.
// Use errors.Is to check for these in calling code.
var (

	// ErrInvalidData is returned when data was supplied as []byte or
	// json.RawMessage and could not be decoded as JSON.
	ErrInvalidData = errors.New("htmlgen: data could not be decoded")

	// ErrParseTemplate is returned when the template text cannot be parsed. A
	// "function not defined" message means the template calls a resolver that
	// was not supplied; "no such template" means a {{ template }} call names a
	// template that is never defined.
	ErrParseTemplate = errors.New("htmlgen: template could not be parsed")

	// ErrUnsupportedTemplate is returned when the template contains a
	// construct htmlgen cannot apply its value rules to, or calls html, js or
	// urlquery. Every value is already escaped for its context, so applying
	// one of those as well would escape it twice.
	ErrUnsupportedTemplate = errors.New("htmlgen: template uses an unsupported construct")

	// ErrUnsafeTemplate is returned when html/template cannot escape the
	// template safely — an action whose HTML context is ambiguous, such as a
	// branch that ends inside an attribute on one side and outside it on the
	// other, or a template that ends inside a tag or quoted attribute.
	ErrUnsafeTemplate = errors.New("htmlgen: template cannot be escaped safely")

	// ErrUnsupportedValue is returned when a template prints a value that has
	// no meaningful text form — a map, a slice, or a []byte. Printing one
	// would emit Go syntax into the document.
	ErrUnsupportedValue = errors.New("htmlgen: value cannot be rendered as text")

	// ErrUnsafeValue is returned when html/template refused a value for the
	// context it was printed in — a javascript: URL in an href, an expression
	// in a style attribute — and substituted its ZgotmplZ marker. The document
	// would otherwise be returned with that marker in place of the value.
	ErrUnsafeValue = errors.New("htmlgen: value was rejected as unsafe for its context")

	// ErrMissingKey is returned only under WithStrictKeys, when the template
	// references a key the data does not contain.
	ErrMissingKey = errors.New("htmlgen: data has no entry for a key the template uses")

	// ErrHelper is returned when a built-in helper is misused — a date that
	// does not match its layout, a non-numeric value passed to decimal, an odd
	// number of arguments to lookup.
	ErrHelper = errors.New("htmlgen: template helper failed")

	// ErrInvalidResolverName is returned when a resolver's name is empty, is
	// not a valid Go identifier, or its ResolveFunc is nil. Names are checked
	// before they reach html/template, which panics on a bad name rather than
	// returning an error.
	ErrInvalidResolverName = errors.New("htmlgen: resolver name is not a valid identifier")

	// ErrReservedResolverName is returned when a resolver's name collides with
	// a template builtin, with one of html/template's internal escapers, or
	// with a function htmlgen defines itself, or with the break and continue
	// keywords. A caller function silently shadows a builtin or a helper, and
	// demotes a keyword to an ordinary call, so this is rejected rather than
	// allowed to change what index, printf or break mean inside a template.
	// html/template's escapers already take precedence over a caller function,
	// so their names are reserved as a safeguard.
	ErrReservedResolverName = errors.New("htmlgen: resolver name is reserved")

	// ErrResolver is returned when a caller-supplied resolver returns an
	// error. The resolver's own error is wrapped, so errors.Is also matches
	// the caller's sentinel.
	ErrResolver = errors.New("htmlgen: resolver returned an error")

	// ErrRender is returned when template execution fails for any other reason.
	ErrRender = errors.New("htmlgen: template execution failed")

	// ErrOutputTooLarge is returned when the rendered document exceeds the
	// configured size limit. Rendering is aborted rather than completed.
	ErrOutputTooLarge = errors.New("htmlgen: rendered output exceeds the size limit")
)
