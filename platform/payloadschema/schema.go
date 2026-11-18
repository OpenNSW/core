// Package payloadschema provides nsw-srilanka's payload schema validation
// extension: a PRE_RESUME check that a task step submission matches the JSON
// Schema of the form its step template names.
//
// The trader app already validates every form in the browser, with JSON Forms'
// Ajv instance. This extension applies the same schema on the server, so a call
// that skips the browser is held to the same rules. Schemas are therefore read
// the way that Ajv instance reads them: see compile.
package payloadschema

import (
	"bytes"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// compile returns schema, a form's JSON Schema document, compiled as the
// trader app's Ajv instance (JSON Forms' createAjv, with ajv-formats in its
// default "full" mode) reads it. It compiles afresh on every call, as the
// other artifact templates are parsed on every use, so an edited form applies
// at once.
func compile(schema []byte) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return nil, fmt.Errorf("payload schema: parse schema: %w", err)
	}

	c := jsonschema.NewCompiler()
	// The forms declare no $schema, which Ajv reads as draft-07.
	c.DefaultDraft(jsonschema.Draft7)
	// Ajv rejects a value that doesn't match its format; this library only
	// annotates formats unless told to assert them. Formats neither library
	// defines, such as the forms' "file", are ignored by both.
	c.AssertFormat()
	for _, f := range ajvFormats {
		c.RegisterFormat(f)
	}
	// Ajv applies ajv-formats' limits whatever draft a form declares; this
	// library applies a vocabulary of its own from 2019-09 only when told to.
	c.RegisterVocabulary(ajvDateLimit)
	// The trader app's file control reads x-file; neither library does.
	c.RegisterVocabulary(xFileRules)
	c.AssertVocabs()

	const location = "form.json"
	if err := c.AddResource(location, doc); err != nil {
		return nil, fmt.Errorf("payload schema: load schema: %w", err)
	}
	compiled, err := c.Compile(location)
	if err != nil {
		return nil, fmt.Errorf("payload schema: compile schema: %w", err)
	}
	return compiled, nil
}
