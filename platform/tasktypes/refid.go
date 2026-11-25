package tasktypes

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/OpenNSW/core/json/jsonpointer"
	"github.com/OpenNSW/core/refid"
	"github.com/OpenNSW/core/shared/deepcopy"
)

// TaskTypeRefIDGenerator generates reference IDs from the refid formats
// in the server config.
const TaskTypeRefIDGenerator = "REFID_GENERATOR"

// RefIDGeneratorPlugin is a synchronous plugin that fills fields of a copy of
// the step's inputs with generated reference IDs, and writes the filled copy
// to the template's output_namespace. Each entry in plugin_properties.ids
// names a field and the (issuer, id_type) format to generate it from:
//
//	{"task_type": "REFID_GENERATOR", "output_namespace": "refid",
//	 "plugin_properties": {"ids": [
//	   {"path": "/order/order_ref", "issuer": "ACME", "id_type": "order"},
//	   {"each": "/order/lines", "path": "0/line_no", "issuer": "ACME", "id_type": "order_line",
//	    "params": {"warehouse": "0/warehouse_code", "region": "/order/region"}}]}}
//
// With input_mapping {"order": "order"}, the node's output_mapping
// {"refid.order": "order"} passes the order on with its IDs.
//
// A pointer starting with "/" is a JSON Pointer from the root of the inputs.
// With "each", which points at an array of objects, the entry applies to every
// element, and a pointer starting with "0/" is relative to that element. Its
// path must be relative, so each element gets an ID of its own. Entries are
// filled in the order listed, and elements in index order.
//
// params maps each param a format expects (a list or param segment's param, or
// a scope-key placeholder) to a pointer to its value. Params are read before
// any ID of the step is generated. A field that already holds an ID is kept,
// unless its entry sets "overwrite": true.
type RefIDGeneratorPlugin struct {
	refIDs refid.Registry
}

// NewRefIDGeneratorPlugin builds the plugin. refIDs must be non-nil; a
// deployment with no refid section passes a registry whose Generate always
// fails, so a template using this plugin fails loudly rather than silently.
func NewRefIDGeneratorPlugin(refIDs refid.Registry) *RefIDGeneratorPlugin {
	if refIDs == nil {
		panic("refIDs is nil")
	}
	return &RefIDGeneratorPlugin{refIDs: refIDs}
}

type refIDGeneratorConfig struct {
	IDs []refIDSpec `json:"ids"`
}

// refIDSpec is one entry of plugin_properties.ids, as written.
type refIDSpec struct {
	Each      string            `json:"each,omitempty"`
	Path      string            `json:"path"`
	Issuer    string            `json:"issuer"`
	IDType    string            `json:"id_type"`
	Params    map[string]string `json:"params,omitempty"`
	Overwrite bool              `json:"overwrite,omitempty"`
}

// refIDEntry is a refIDSpec with its pointers parsed.
type refIDEntry struct {
	spec     refIDSpec
	each     string // absolute JSON Pointer, "" without "each"
	path     refIDPointer
	pathSegs []string // path's decoded segments
	params   map[string]refIDPointer
}

// refIDPointer is a pointer from plugin_properties: an absolute JSON Pointer
// ("/a/b"), resolved against the root, or a relative one ("0/a/b"), resolved
// against the current element of "each".
type refIDPointer struct {
	raw      string
	ptr      string // the JSON Pointer part, always starting with "/"
	relative bool
}

// parseRefIDPointer parses s. A relative pointer is accepted only when
// allowRelative is set, that is, inside an entry with "each".
func parseRefIDPointer(s string, allowRelative bool) (refIDPointer, error) {
	if strings.HasPrefix(s, "/") {
		if !jsonpointer.Valid(s) {
			return refIDPointer{}, fmt.Errorf("%q is not a valid JSON Pointer", s)
		}
		return refIDPointer{raw: s, ptr: s}, nil
	}
	rest, ok := strings.CutPrefix(s, "0/")
	if !ok {
		return refIDPointer{}, fmt.Errorf(`%q must start with "/" (from the root) or "0/" (from the current element)`, s)
	}
	if !allowRelative {
		return refIDPointer{}, fmt.Errorf(`%q is relative, but only an entry with "each" has a current element`, s)
	}
	ptr := "/" + rest
	if !jsonpointer.Valid(ptr) {
		return refIDPointer{}, fmt.Errorf("%q is not a valid relative JSON Pointer", s)
	}
	return refIDPointer{raw: s, ptr: ptr, relative: true}, nil
}

// in returns the object p resolves against: elem for a relative pointer, root
// otherwise.
func (p refIDPointer) in(root, elem map[string]any) map[string]any {
	if p.relative {
		return elem
	}
	return root
}

// entries validates the configuration and parses every pointer in it.
func (c refIDGeneratorConfig) entries() ([]refIDEntry, error) {
	if len(c.IDs) == 0 {
		return nil, errors.New("plugin_properties.ids must list at least one ID")
	}
	entries := make([]refIDEntry, 0, len(c.IDs))
	for i, s := range c.IDs {
		e, err := parseRefIDEntry(s)
		if err != nil {
			return nil, fmt.Errorf("ids[%d]%w", i, err)
		}
		// Two paths in the same scope must not overlap: one field inside the
		// other would be written as an ID and then written through.
		for j := range entries {
			if entries[j].each == e.each && overlapping(entries[j].pathSegs, e.pathSegs) {
				return nil, fmt.Errorf("ids[%d].path %q overlaps ids[%d].path %q", i, s.Path, j, entries[j].spec.Path)
			}
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// parseRefIDEntry validates one entry and parses its pointers. Its errors
// start with the field they concern (".path: …"), for entries to prefix with
// the entry's index.
func parseRefIDEntry(s refIDSpec) (refIDEntry, error) {
	if strings.TrimSpace(s.Issuer) == "" {
		return refIDEntry{}, errors.New(".issuer is required")
	}
	if strings.TrimSpace(s.IDType) == "" {
		return refIDEntry{}, errors.New(".id_type is required")
	}

	e := refIDEntry{spec: s, params: make(map[string]refIDPointer, len(s.Params))}
	hasEach := s.Each != ""
	if hasEach {
		each, err := parseRefIDPointer(s.Each, false)
		if err != nil {
			return refIDEntry{}, fmt.Errorf(".each: %w", err)
		}
		e.each = each.ptr
	}

	path, err := parseRefIDPointer(s.Path, hasEach)
	if err != nil {
		return refIDEntry{}, fmt.Errorf(".path: %w", err)
	}
	if hasEach && !path.relative {
		return refIDEntry{}, fmt.Errorf(`.path %q must be relative ("0/…") with "each", so each element gets its own ID`, s.Path)
	}
	e.path = path
	if e.pathSegs, err = jsonpointer.Segments(path.ptr); err != nil {
		return refIDEntry{}, fmt.Errorf(".path: %w", err)
	}

	for name, raw := range s.Params {
		if strings.TrimSpace(name) == "" {
			return refIDEntry{}, errors.New(".params has an empty name")
		}
		p, err := parseRefIDPointer(raw, hasEach)
		if err != nil {
			return refIDEntry{}, fmt.Errorf(".params.%s: %w", name, err)
		}
		e.params[name] = p
	}
	return e, nil
}

// overlapping reports whether one decoded pointer is a prefix of the other,
// so the field one names sits inside, or is, the field the other names.
func overlapping(a, b []string) bool {
	n := min(len(a), len(b))
	return slices.Equal(a[:n], b[:n])
}

// refIDTarget is one field to fill.
type refIDTarget struct {
	obj    map[string]any // the object holding the field
	ptr    string         // JSON Pointer to the field within obj
	label  string         // where the field sits from the root, for messages
	entry  *refIDEntry
	params map[string]string
}

// Execute fills the targets and returns nil (not ErrSuspended), so the engine
// advances immediately. Every target and its params are resolved before any
// ID is generated, so inputs of the wrong shape fail without using up numbers.
func (p *RefIDGeneratorPlugin) Execute(ctx pluginContext, configRaw json.RawMessage) error {
	var cfg refIDGeneratorConfig
	if err := json.Unmarshal(configRaw, &cfg); err != nil {
		return configError(fmt.Errorf("refid_generator: invalid config: %w", err))
	}
	entries, err := cfg.entries()
	if err != nil {
		return configError(fmt.Errorf("refid_generator: %w", err))
	}
	if ctx.OutputNamespace == "" {
		return configError(errors.New("refid_generator: output_namespace is required to hold the generated IDs"))
	}
	if ctx.Record == nil {
		return inputError(errors.New("refid_generator: task record is nil"))
	}

	// The inputs belong to the caller, so the IDs are filled into a copy.
	root := deepcopy.Map(ctx.Inputs)
	if root == nil {
		root = make(map[string]any)
	}

	var targets []refIDTarget
	for i := range entries {
		t, err := collectRefIDTargets(root, &entries[i])
		if err != nil {
			return inputError(fmt.Errorf("refid_generator: ids[%d]: %w", i, err))
		}
		targets = append(targets, t...)
	}

	for _, t := range targets {
		if !t.entry.spec.Overwrite {
			if v, _ := jsonpointer.Get(t.obj, t.ptr); v != nil && v != "" {
				continue
			}
		}
		spec := t.entry.spec
		id, err := p.refIDs.Generate(ctx.Context, spec.Issuer, spec.IDType, t.params)
		if err != nil {
			return refIDError(fmt.Errorf("refid_generator: generate %s (%q, %q): %w", t.label, spec.Issuer, spec.IDType, err))
		}
		if !jsonpointer.Set(t.obj, t.ptr, id) {
			return inputError(fmt.Errorf("refid_generator: cannot write %s", t.label))
		}
	}

	if ctx.Record.Data == nil {
		ctx.Record.Data = make(map[string]any)
	}
	ctx.Record.Data[ctx.OutputNamespace] = root
	return nil
}

// collectRefIDTargets resolves e against root: one target without "each", one
// per array element with it, and none for a missing or null array.
// refIDError marks a Generate error retrying can't fix: an issuer, ID type or
// padding the configuration names wrongly, or a param the inputs lack. A random
// collision, an exhausted search and a database error stay retryable.
func refIDError(err error) error {
	switch {
	case errors.Is(err, refid.ErrUnknownIssuer), errors.Is(err, refid.ErrUnknownIDType), errors.Is(err, refid.ErrCounterOverflow):
		return configError(err)
	case errors.Is(err, refid.ErrInvalidParam):
		return inputError(err)
	}
	return err
}

func collectRefIDTargets(root map[string]any, e *refIDEntry) ([]refIDTarget, error) {
	if e.each == "" {
		t, err := newRefIDTarget(root, nil, e, e.path.ptr)
		if err != nil {
			return nil, err
		}
		return []refIDTarget{t}, nil
	}

	if err := checkObjectPath(root, e.each); err != nil {
		return nil, err
	}
	raw, _ := jsonpointer.Get(root, e.each)
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s is a %T, not an array", e.each, raw)
	}

	targets := make([]refIDTarget, 0, len(items))
	for i, el := range items {
		elem, ok := el.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s/%d is a %T, not an object", e.each, i, el)
		}
		t, err := newRefIDTarget(root, elem, e, fmt.Sprintf("%s/%d%s", e.each, i, e.path.ptr))
		if err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	return targets, nil
}

// newRefIDTarget checks that the field can hold an ID and resolves the
// entry's params for it. elem is the current element, nil without "each".
func newRefIDTarget(root, elem map[string]any, e *refIDEntry, label string) (refIDTarget, error) {
	obj := e.path.in(root, elem)
	if err := checkObjectPath(obj, e.path.ptr); err != nil {
		return refIDTarget{}, fmt.Errorf("%s: %w", label, err)
	}
	if v, ok := jsonpointer.Get(obj, e.path.ptr); ok && v != nil {
		if _, isString := v.(string); !isString {
			return refIDTarget{}, fmt.Errorf("%s holds a %T, not an ID", label, v)
		}
	}

	params, err := readRefIDParams(root, elem, e.params)
	if err != nil {
		return refIDTarget{}, fmt.Errorf("%s: %w", label, err)
	}
	return refIDTarget{obj: obj, ptr: e.path.ptr, label: label, entry: e, params: params}, nil
}

// readRefIDParams reads the value of each param through its pointer. A pointer
// with no value leaves its param out, and a non-string value is an error. elem
// is the current element, nil without "each".
func readRefIDParams(root, elem map[string]any, pointers map[string]refIDPointer) (map[string]string, error) {
	params := make(map[string]string, len(pointers))
	for name, p := range pointers {
		v, ok := jsonpointer.Get(p.in(root, elem), p.ptr)
		if !ok || v == nil {
			continue
		}
		s, isString := v.(string)
		if !isString {
			return nil, fmt.Errorf("param %q (%s) is a %T, not a string", name, p.raw, v)
		}
		params[name] = s
	}
	return params, nil
}

// checkObjectPath reports an error when a segment before the last one in
// pointer exists but is not an object, which jsonpointer.Set cannot write
// through. Missing segments are fine: Set creates them.
func checkObjectPath(doc map[string]any, pointer string) error {
	segs, err := jsonpointer.Segments(pointer)
	if err != nil {
		return err
	}
	cur := doc
	for i, seg := range segs[:len(segs)-1] {
		next, ok := cur[seg]
		if !ok {
			return nil
		}
		m, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: segment %d (%q) is a %T, not an object", pointer, i+1, seg, next)
		}
		cur = m
	}
	return nil
}
