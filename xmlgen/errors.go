// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package xmlgen

import "errors"

// Sentinel errors returned by the xmlgen package.
// Use errors.Is to check for these in calling code.
var (

	// ErrInvalidData is returned when data was supplied as []byte or
	// json.RawMessage and could not be decoded as JSON.
	ErrInvalidData = errors.New("xmlgen: data could not be decoded")

	// ErrParseTemplate is returned when the template text cannot be parsed. A
	// "function not defined" message means the template calls a resolver that
	// was not supplied.
	ErrParseTemplate = errors.New("xmlgen: template could not be parsed")

	// ErrUnsupportedTemplate is returned when the template contains a
	// construct xmlgen cannot guarantee escaping for.
	ErrUnsupportedTemplate = errors.New("xmlgen: template uses an unsupported construct")

	// ErrUnsupportedValue is returned when a template prints a value that has
	// no meaningful XML text form — a map, a slice, or a []byte. Printing one
	// would emit Go syntax into the document.
	ErrUnsupportedValue = errors.New("xmlgen: value cannot be rendered as XML text")

	// ErrMissingKey is returned only under WithStrictKeys, when the template
	// references a key the data does not contain.
	ErrMissingKey = errors.New("xmlgen: data has no entry for a key the template uses")

	// ErrHelper is returned when a built-in helper is misused — a date that
	// does not match its layout, a non-numeric value passed to decimal, an odd
	// number of arguments to lookup.
	ErrHelper = errors.New("xmlgen: template helper failed")

	// ErrInvalidResolverName is returned when a resolver's name is empty, is
	// not a valid Go identifier, or its ResolveFunc is nil. Names are checked
	// before they reach text/template, which panics on a bad name rather than
	// returning an error.
	ErrInvalidResolverName = errors.New("xmlgen: resolver name is not a valid identifier")

	// ErrReservedResolverName is returned when a resolver's name collides with
	// a text/template builtin or with a function xmlgen defines itself. A
	// caller function silently shadows a builtin, so this is rejected rather
	// than allowed to change what index or printf mean inside a template.
	ErrReservedResolverName = errors.New("xmlgen: resolver name is reserved")

	// ErrResolver is returned when a caller-supplied resolver returns an
	// error. The resolver's own error is wrapped, so errors.Is also matches
	// the caller's sentinel.
	ErrResolver = errors.New("xmlgen: resolver returned an error")

	// ErrRender is returned when template execution fails for any other reason.
	ErrRender = errors.New("xmlgen: template execution failed")

	// ErrOutputTooLarge is returned when the rendered document exceeds the
	// configured size limit. Rendering is aborted rather than completed.
	ErrOutputTooLarge = errors.New("xmlgen: rendered output exceeds the size limit")

	// ErrMalformedXML is returned when the rendered output is not well-formed.
	ErrMalformedXML = errors.New("xmlgen: rendered output is not well-formed XML")

	// ErrMultipleRoots is returned when the rendered output has more or fewer
	// than one root element.
	ErrMultipleRoots = errors.New("xmlgen: rendered output must have exactly one root element")

	// ErrDoctypeNotAllowed is returned when the rendered output contains a
	// DOCTYPE. Go's decoder ignores the internal subset; a receiving parser
	// may not.
	ErrDoctypeNotAllowed = errors.New("xmlgen: rendered output contains a DOCTYPE declaration")

	// ErrUndeclaredNamespace is returned when the rendered output uses a
	// namespace prefix that is not declared in scope. encoding/xml documents
	// that it does not reject these, so xmlgen checks them itself.
	ErrUndeclaredNamespace = errors.New("xmlgen: rendered output uses an undeclared namespace prefix")
)
