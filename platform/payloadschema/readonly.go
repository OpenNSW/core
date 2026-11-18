package payloadschema

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// readOnlyForm is what the read-only check knows about a form: the
// values its schema marks readOnly, which the server owns, and whether the user
// may add or remove the rows of each list. A UI schema "readonly" option is
// display only and is not read.
type readOnlyForm struct {
	// root is the submission as a whole; nil when nothing in the form is
	// read-only.
	root  *roNode
	lists listSwitches
	// reaches caches, per schema, whether a readOnly can be reached from it.
	reaches map[*jsonschema.Schema]bool
}

// newReadOnlyForm reads a form's schema, compiled and as written, and its UI
// schema. It fails when a readOnly sits where the check doesn't follow it (see
// refusedReadOnly and refSiblings), or when the UI schema isn't JSON: the form
// is then misconfigured.
func newReadOnlyForm(schema *jsonschema.Schema, raw []byte, uiSchema json.RawMessage) (*readOnlyForm, error) {
	// Up to draft-07, "all other properties in a "$ref" object MUST be
	// ignored" (JSON Schema Core draft-07, section 8.3); from draft 2019-09,
	// $ref applies alongside them (JSON Schema Core 2019-09, section 8.2.4.1).
	// The forms are draft-07.
	if schema.DraftVersion < 2019 {
		if err := refSiblings(raw); err != nil {
			return nil, err
		}
	}
	if err := refusedReadOnly(schema); err != nil {
		return nil, err
	}
	lists, err := parseListSwitches(uiSchema)
	if err != nil {
		return nil, err
	}
	f := &readOnlyForm{lists: lists, reaches: make(map[*jsonschema.Schema]bool)}
	f.root = f.node(sameValue(schema), "")
	return f, nil
}

// roNode is one value of a submission: every schema that applies to it, with
// $ref and allOf followed, and the path the UI schema knows it by, such as
// "/containers/[]/seals". Nodes are built as they are asked for, so a schema
// that refers to itself is followed only as deep as the data goes.
type roNode struct {
	form    *readOnlyForm
	schemas []*jsonschema.Schema
	path    string
}

// node returns the node for schemas at path, or nil when no readOnly can be
// reached from any of them: nothing in that value needs comparing.
func (f *readOnlyForm) node(schemas []*jsonschema.Schema, path string) *roNode {
	for _, s := range schemas {
		if f.reachesReadOnly(s) {
			return &roNode{form: f, schemas: schemas, path: path}
		}
	}
	return nil
}

// readOnly reports whether the value itself is server-owned, and so compared
// as a whole.
func (n *roNode) readOnly() bool {
	for _, s := range n.schemas {
		if s.ReadOnly {
			return true
		}
	}
	return false
}

// readOnlyAt reports whether the value at location, a path into data, is
// server-owned: whether it, or a value holding it, is read-only.
func (f *readOnlyForm) readOnlyAt(data any, location []string) bool {
	n, v := f.root, data
	for _, token := range location {
		if n == nil {
			return false
		}
		if n.readOnly() {
			return true
		}
		switch c := v.(type) {
		case map[string]any:
			n, v = n.property(token), c[token]
		case []any:
			i, err := strconv.Atoi(token)
			if err != nil || i < 0 || i >= len(c) {
				return false
			}
			n, v = n.row(), c[i]
		default:
			return false
		}
	}
	return n != nil && n.readOnly()
}

// defaultValue returns the value's schema default, the first one declared.
func (n *roNode) defaultValue() (any, bool) {
	for _, s := range n.schemas {
		if s.Default != nil {
			return *s.Default, true
		}
	}
	return nil, false
}

// property returns the node for the value under key, or nil when nothing in
// it is read-only. A key gets the schemas of the property of that name, of
// every pattern it matches, and otherwise of additionalProperties (JSON Schema
// Validation draft-07, sections 6.5.4 to 6.5.6).
func (n *roNode) property(key string) *roNode {
	var schemas []*jsonschema.Schema
	for _, s := range n.schemas {
		matched := false
		if p, ok := s.Properties[key]; ok {
			schemas = append(schemas, p)
			matched = true
		}
		for re, p := range s.PatternProperties {
			if re.MatchString(key) {
				schemas = append(schemas, p)
				matched = true
			}
		}
		if ap, ok := s.AdditionalProperties.(*jsonschema.Schema); ok && !matched {
			schemas = append(schemas, ap)
		}
	}
	return n.form.node(sameValue(schemas...), n.path+"/"+escapeToken(key))
}

// namedKeys returns, as the keys of a map, the properties the value's schemas
// name, so a read-only value a submission leaves out is still visited.
func (n *roNode) namedKeys() map[string]any {
	keys := make(map[string]any)
	for _, s := range n.schemas {
		for k := range s.Properties {
			keys[k] = nil
		}
	}
	return keys
}

// row returns the node for each row of the value as a list, or nil when
// nothing in a row is read-only.
func (n *roNode) row() *roNode {
	var schemas []*jsonschema.Schema
	for _, s := range n.schemas {
		if items, ok := s.Items.(*jsonschema.Schema); ok {
			schemas = append(schemas, items)
		}
		if s.Items2020 != nil {
			schemas = append(schemas, s.Items2020)
		}
	}
	return n.form.node(sameValue(schemas...), n.path+"/[]")
}

// switches returns whether the user may add and remove rows of the value as a
// list.
func (n *roNode) switches() listSwitch {
	return n.form.lists.at(n.path)
}

// sameValue returns schemas with every schema that applies to the same value
// as one of them: those they refer to ($ref) and their allOf parts, followed
// through. Each appears once, in the order first reached.
func sameValue(schemas ...*jsonschema.Schema) []*jsonschema.Schema {
	var out []*jsonschema.Schema
	seen := make(map[*jsonschema.Schema]bool)
	var add func(s *jsonschema.Schema)
	add = func(s *jsonschema.Schema) {
		if s == nil || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
		add(s.Ref)
		for _, a := range s.AllOf {
			add(a)
		}
	}
	for _, s := range schemas {
		add(s)
	}
	return out
}

// reachesReadOnly reports whether s, or a schema the check follows from it,
// is readOnly.
func (f *readOnlyForm) reachesReadOnly(s *jsonschema.Schema) bool {
	if r, ok := f.reaches[s]; ok {
		return r
	}
	found := false
	seen := make(map[*jsonschema.Schema]bool)
	var visit func(s *jsonschema.Schema)
	visit = func(s *jsonschema.Schema) {
		if s == nil || seen[s] || found {
			return
		}
		seen[s] = true
		if s.ReadOnly {
			found = true
			return
		}
		for _, next := range followed(s) {
			visit(next)
		}
	}
	visit(s)
	f.reaches[s] = found
	return found
}

// followed returns the schemas the check follows from s: those that apply to
// the same value, to its properties, or to each of its rows.
func followed(s *jsonschema.Schema) []*jsonschema.Schema {
	out := []*jsonschema.Schema{s.Ref, s.Items2020}
	out = append(out, s.AllOf...)
	for _, p := range s.Properties {
		out = append(out, p)
	}
	for _, p := range s.PatternProperties {
		out = append(out, p)
	}
	if ap, ok := s.AdditionalProperties.(*jsonschema.Schema); ok {
		out = append(out, ap)
	}
	if items, ok := s.Items.(*jsonschema.Schema); ok {
		out = append(out, items)
	}
	return out
}

// refusedPart is a schema the check doesn't follow, and the keyword it sits
// under.
type refusedPart struct {
	keyword string
	schema  *jsonschema.Schema
}

// refused returns the schemas under s the check doesn't follow. Whether they
// apply depends on the data (a branch, a key being present, some row) or on a
// row's position, so a readOnly under them is refused rather than guessed.
func refused(s *jsonschema.Schema) []refusedPart {
	out := []refusedPart{
		{"if", s.If}, {"then", s.Then}, {"else", s.Else}, {"not", s.Not},
		{"contains", s.Contains}, {"propertyNames", s.PropertyNames},
		{"unevaluatedProperties", s.UnevaluatedProperties},
		{"unevaluatedItems", s.UnevaluatedItems}, {"$recursiveRef", s.RecursiveRef},
	}
	for _, a := range s.OneOf {
		out = append(out, refusedPart{"oneOf", a})
	}
	for _, a := range s.AnyOf {
		out = append(out, refusedPart{"anyOf", a})
	}
	for _, d := range s.Dependencies {
		if ds, ok := d.(*jsonschema.Schema); ok {
			out = append(out, refusedPart{"dependencies", ds})
		}
	}
	for _, d := range s.DependentSchemas {
		out = append(out, refusedPart{"dependentSchemas", d})
	}
	if tuple, ok := s.Items.([]*jsonschema.Schema); ok {
		for _, t := range tuple {
			out = append(out, refusedPart{"items", t})
		}
	}
	if ai, ok := s.AdditionalItems.(*jsonschema.Schema); ok {
		out = append(out, refusedPart{"additionalItems", ai})
	}
	for _, p := range s.PrefixItems {
		out = append(out, refusedPart{"prefixItems", p})
	}
	if len(s.PrefixItems) > 0 {
		// items then covers only the rows after the prefix (JSON Schema
		// Core 2020-12, section 10.3.1.2).
		out = append(out, refusedPart{"items", s.Items2020})
	}
	if s.DynamicRef != nil {
		out = append(out, refusedPart{"$dynamicRef", s.DynamicRef.Ref})
	}
	return out
}

// refSiblings returns an error when an object in raw, a schema document of
// draft-07 or older, has a $ref next to a default or a keyword that holds a
// readOnly. Those drafts say to ignore every keyword beside $ref (JSON Schema
// Core draft-07, section 8.3), and the library drops most of them, including
// properties, allOf and default, but the trader app's Ajv keeps them: a
// readOnly or a default the check needs would be lost without a word. A form
// without readOnly values is not checked, since the check needs nothing from
// it.
//
// The walk reads every key as a keyword, so a property named like one ("$ref",
// "readOnly") can refuse a form that is fine; that fails closed, and is
// accepted rather than walking the schema keyword by keyword.
func refSiblings(raw []byte) error {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("payload schema: parse schema: %w", err)
	}
	if !holdsReadOnly(doc) {
		return nil
	}
	var walk func(v any, at string) error
	walk = func(v any, at string) error {
		switch v := v.(type) {
		case map[string]any:
			if _, ok := v["$ref"]; ok {
				for k, sibling := range v {
					if k == "default" || (k != "$ref" && k != "readOnly" && holdsReadOnly(sibling)) {
						return fmt.Errorf("payload schema: %q at %s sits beside $ref, which the read-only check doesn't support", k, at)
					}
				}
			}
			for k, child := range v {
				if err := walk(child, at+"/"+escapeToken(k)); err != nil {
					return err
				}
			}
		case []any:
			for i, child := range v {
				if err := walk(child, fmt.Sprintf("%s/%d", at, i)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(doc, "#")
}

// holdsReadOnly reports whether a "readOnly": true sits anywhere in v.
func holdsReadOnly(v any) bool {
	switch v := v.(type) {
	case map[string]any:
		if v["readOnly"] == true {
			return true
		}
		for _, child := range v {
			if holdsReadOnly(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if holdsReadOnly(child) {
				return true
			}
		}
	}
	return false
}

// refusedReadOnly returns an error naming the first readOnly reached through a
// part of schema the check doesn't follow (see refused). A readOnly in a
// definition nothing refers to applies to nothing, and is not reached.
func refusedReadOnly(schema *jsonschema.Schema) error {
	type visit struct {
		s       *jsonschema.Schema
		refused bool
	}
	seen := make(map[visit]bool)
	var walk func(s *jsonschema.Schema, under string) error
	walk = func(s *jsonschema.Schema, under string) error {
		if s == nil || seen[visit{s, under != ""}] {
			return nil
		}
		seen[visit{s, under != ""}] = true
		if s.ReadOnly && under != "" {
			return fmt.Errorf("payload schema: readOnly at %s is under %q, which the read-only check doesn't follow", s.Location, under)
		}
		for _, next := range followed(s) {
			if err := walk(next, under); err != nil {
				return err
			}
		}
		for _, r := range refused(s) {
			keyword := under
			if keyword == "" {
				keyword = r.keyword
			}
			if err := walk(r.schema, keyword); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(schema, "")
}

// escapeToken escapes key as a JSON Pointer reference token (RFC 6901), as
// keys appear in UI schema scopes.
func escapeToken(key string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(key)
}
