package payloadschema

import (
	"encoding/json"
	"strings"
	"testing"
)

// readOnlyFormOf builds the readOnlyForm of schema with no UI schema.
func readOnlyFormOf(t *testing.T, schema string) *readOnlyForm {
	t.Helper()
	compiled, err := compile([]byte(schema))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	f, err := newReadOnlyForm(compiled, []byte(schema), nil)
	if err != nil {
		t.Fatalf("newReadOnlyForm: %v", err)
	}
	return f
}

// at walks from the form's root through path, a "/"-separated list of keys,
// with "[]" for a row. It returns nil as soon as a step finds nothing
// read-only.
func at(f *readOnlyForm, path string) *roNode {
	n := f.root
	for _, step := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if n == nil || step == "" {
			break
		}
		if step == "[]" {
			n = n.row()
		} else {
			n = n.property(step)
		}
	}
	return n
}

func TestReadOnlyForm_NoReadOnly(t *testing.T) {
	f := readOnlyFormOf(t, `{"type": "object", "properties": {"name": {"type": "string"}}}`)
	if f.root != nil {
		t.Fatalf("root = %+v, want nil for a form without readOnly", f.root)
	}
}

func TestReadOnlyForm_Finds(t *testing.T) {
	const schema = `{
		"type": "object",
		"definitions": {
			"result": {"type": "string", "readOnly": true, "default": "PENDING"},
			"node": {
				"type": "object",
				"properties": {
					"code": {"type": "string", "readOnly": true},
					"children": {"type": "array", "items": {"$ref": "#/definitions/node"}}
				}
			}
		},
		"properties": {
			"reference": {"type": "string", "readOnly": true},
			"note": {"type": "string"},
			"applicant": {
				"type": "object",
				"properties": {"name": {"type": "string"}, "tin": {"type": "string", "readOnly": true}}
			},
			"lab": {"$ref": "#/definitions/result"},
			"containers": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"cdn": {"type": "string", "readOnly": true},
						"seal": {"type": "string"},
						"tags": {"type": "array", "items": {"type": "string", "readOnly": true}}
					}
				}
			},
			"items": {"type": "array", "readOnly": true, "items": {"type": "object"}},
			"fees": {"type": "object", "additionalProperties": {"type": "number", "readOnly": true}},
			"codes": {"type": "object", "patternProperties": {"^hs_": {"type": "string", "readOnly": true}}},
			"tree": {"$ref": "#/definitions/node"}
		},
		"allOf": [{"properties": {"signedBy": {"type": "string", "readOnly": true}}}]
	}`
	f := readOnlyFormOf(t, schema)

	tests := []struct {
		path        string
		wantNode    bool
		wantRO      bool
		wantDefault any
	}{
		{path: "/reference", wantNode: true, wantRO: true},
		{path: "/note"},
		{path: "/applicant", wantNode: true},
		{path: "/applicant/name"},
		{path: "/applicant/tin", wantNode: true, wantRO: true},
		{path: "/lab", wantNode: true, wantRO: true, wantDefault: "PENDING"},
		{path: "/containers", wantNode: true},
		{path: "/containers/[]", wantNode: true},
		{path: "/containers/[]/cdn", wantNode: true, wantRO: true},
		{path: "/containers/[]/seal"},
		{path: "/containers/[]/tags/[]", wantNode: true, wantRO: true},
		{path: "/items", wantNode: true, wantRO: true},
		{path: "/fees/anything", wantNode: true, wantRO: true},
		{path: "/codes/hs_0901", wantNode: true, wantRO: true},
		{path: "/codes/other"},
		{path: "/signedBy", wantNode: true, wantRO: true},
		{path: "/tree/children/[]/children/[]/code", wantNode: true, wantRO: true},
		{path: "/undeclared"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			n := at(f, tt.path)
			if (n != nil) != tt.wantNode {
				t.Fatalf("node = %v, want one: %v", n, tt.wantNode)
			}
			if n == nil {
				return
			}
			if n.readOnly() != tt.wantRO {
				t.Errorf("readOnly() = %v, want %v", n.readOnly(), tt.wantRO)
			}
			def, ok := n.defaultValue()
			if ok != (tt.wantDefault != nil) || def != tt.wantDefault {
				t.Errorf("defaultValue() = %v, %v; want %v", def, ok, tt.wantDefault)
			}
			if n.path != tt.path {
				t.Errorf("path = %q, want %q", n.path, tt.path)
			}
		})
	}
}

// A key matching a named property and a pattern gets both; additionalProperties
// applies only to keys neither names.
func TestReadOnlyForm_PropertyAppliesPatternsAndAdditional(t *testing.T) {
	f := readOnlyFormOf(t, `{
		"type": "object",
		"properties": {"hs_code": {"type": "string"}, "other": {"type": "string"}},
		"patternProperties": {"^hs_": {"readOnly": true}},
		"additionalProperties": {"readOnly": true}
	}`)
	if n := at(f, "/hs_code"); n == nil || !n.readOnly() {
		t.Errorf("hs_code: want read-only from its pattern")
	}
	if n := at(f, "/other"); n != nil {
		t.Errorf("other: want nil, additionalProperties doesn't apply to a named property")
	}
	if n := at(f, "/extra"); n == nil || !n.readOnly() {
		t.Errorf("extra: want read-only from additionalProperties")
	}
}

func TestReadOnlyForm_RefusesReadOnlyItDoesNotFollow(t *testing.T) {
	ro := `{"readOnly": true}`
	tests := map[string]string{
		"if":                      `{"if": ` + ro + `}`,
		"then":                    `{"if": {}, "then": {"properties": {"a": ` + ro + `}}}`,
		"else":                    `{"if": {}, "else": ` + ro + `}`,
		"oneOf":                   `{"oneOf": [{}, {"properties": {"a": ` + ro + `}}]}`,
		"anyOf":                   `{"anyOf": [` + ro + `]}`,
		"not":                     `{"not": ` + ro + `}`,
		"dependencies":            `{"dependencies": {"a": {"properties": {"b": ` + ro + `}}}}`,
		"contains":                `{"contains": ` + ro + `}`,
		"propertyNames":           `{"propertyNames": ` + ro + `}`,
		"items":                   `{"items": [` + ro + `]}`,
		"additionalItems":         `{"items": [{}], "additionalItems": ` + ro + `}`,
		"through a reference":     `{"definitions": {"d": ` + ro + `}, "oneOf": [{"$ref": "#/definitions/d"}]}`,
		"deep inside a branch":    `{"anyOf": [{"properties": {"a": {"type": "array", "items": {"allOf": [` + ro + `]}}}}]}`,
		"dependentSchemas2019":    `{"$schema": "https://json-schema.org/draft/2019-09/schema", "dependentSchemas": {"a": ` + ro + `}}`,
		"unevaluatedProperties":   `{"$schema": "https://json-schema.org/draft/2020-12/schema", "unevaluatedProperties": ` + ro + `}`,
		"prefixItems":             `{"$schema": "https://json-schema.org/draft/2020-12/schema", "prefixItems": [` + ro + `]}`,
		"items after prefixItems": `{"$schema": "https://json-schema.org/draft/2020-12/schema", "prefixItems": [{}], "items": ` + ro + `}`,
		"dependentSchemas2020":    `{"$schema": "https://json-schema.org/draft/2020-12/schema", "dependentSchemas": {"a": ` + ro + `}}`,
		"unevaluatedItems":        `{"$schema": "https://json-schema.org/draft/2020-12/schema", "unevaluatedItems": ` + ro + `}`,
		"$dynamicRef":             `{"$schema": "https://json-schema.org/draft/2020-12/schema", "$defs": {"d": {"$dynamicAnchor": "d", "readOnly": true}}, "properties": {"a": {"$dynamicRef": "#d"}}}`,
		"$recursiveRef":           `{"$schema": "https://json-schema.org/draft/2019-09/schema", "$recursiveAnchor": true, "properties": {"a": {"$recursiveRef": "#"}, "b": ` + ro + `}}`,
		"a property beside $ref":  `{"definitions": {"x": {}}, "properties": {"a": {"$ref": "#/definitions/x", "properties": {"b": ` + ro + `}}}}`,
		"an allOf beside $ref":    `{"definitions": {"x": {}}, "properties": {"a": {"$ref": "#/definitions/x", "allOf": [` + ro + `]}}}`,
		"a default beside $ref":   `{"definitions": {"x": ` + ro + `}, "properties": {"a": {"$ref": "#/definitions/x", "default": "PENDING"}}}`,
		"beside $ref in a property named default": `{"definitions": {"x": {}}, "properties": {"default": {"properties": {"a": {"$ref": "#/definitions/x", "properties": {"b": ` + ro + `}}}}}}`,
	}
	for name, schema := range tests {
		t.Run(name, func(t *testing.T) {
			compiled, err := compile([]byte(schema))
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if _, err := newReadOnlyForm(compiled, []byte(schema), nil); err == nil {
				t.Fatal("newReadOnlyForm() = nil error; want the form refused")
			}
		})
	}
}

// A readOnly the check follows can sit next to the parts it refuses, and one in
// a definition nothing refers to applies to nothing.
func TestReadOnlyForm_AcceptsReadOnlyOutsideRefusedParts(t *testing.T) {
	tests := map[string]string{
		"next to a conditional": `{
			"definitions": {"unused": {"readOnly": true}},
			"properties": {"a": {"readOnly": true}},
			"if": {"properties": {"b": {"const": 1}}},
			"then": {"required": ["c"]}
		}`,
		"dependencies naming keys":                         `{"properties": {"a": {"readOnly": true}}, "dependencies": {"a": ["b"]}}`,
		"readOnly beside $ref, which is kept":              `{"definitions": {"x": {"type": "string"}}, "properties": {"a": {"$ref": "#/definitions/x", "readOnly": true, "title": "A"}}}`,
		"a default beside $ref in a form without readOnly": `{"definitions": {"x": {"type": "string"}}, "properties": {"a": {"$ref": "#/definitions/x", "default": "x"}}}`,
		"siblings of $ref in draft 2020, which are kept":   `{"$schema": "https://json-schema.org/draft/2020-12/schema", "$defs": {"x": {}}, "properties": {"a": {"$ref": "#/$defs/x", "properties": {"b": {"readOnly": true}}}}}`,
	}
	for name, schema := range tests {
		t.Run(name, func(t *testing.T) {
			compiled, err := compile([]byte(schema))
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if _, err := newReadOnlyForm(compiled, []byte(schema), nil); err != nil {
				t.Fatalf("newReadOnlyForm() = %v; want nil", err)
			}
		})
	}
}

// readOnly beside $ref is kept by the library, and protects the field.
func TestReadOnlyForm_ReadOnlyBesideRef(t *testing.T) {
	f := readOnlyFormOf(t, `{"definitions": {"x": {"type": "string"}}, "properties": {"a": {"$ref": "#/definitions/x", "readOnly": true}}}`)
	if n := at(f, "/a"); n == nil || !n.readOnly() {
		t.Fatal("a: want read-only")
	}
}

// In draft 2020, siblings of $ref apply too.
func TestReadOnlyForm_SiblingsOfRefIn2020(t *testing.T) {
	f := readOnlyFormOf(t, `{"$schema": "https://json-schema.org/draft/2020-12/schema", "$defs": {"x": {}}, "properties": {"a": {"$ref": "#/$defs/x", "properties": {"b": {"readOnly": true}}}}}`)
	if n := at(f, "/a/b"); n == nil || !n.readOnly() {
		t.Fatal("a/b: want read-only")
	}
}

func TestReadOnlyForm_RefusesAUISchemaThatIsNotJSON(t *testing.T) {
	const schema = `{"properties": {"a": {"readOnly": true}}}`
	compiled, err := compile([]byte(schema))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := newReadOnlyForm(compiled, []byte(schema), json.RawMessage(`{`)); err == nil {
		t.Fatal("newReadOnlyForm() = nil error; want the UI schema refused")
	}
}
