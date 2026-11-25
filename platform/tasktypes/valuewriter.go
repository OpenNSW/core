package tasktypes

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/OpenNSW/core/json/jsonpointer"
	"github.com/OpenNSW/core/shared/deepcopy"
)

// TaskTypeValueWriter writes values the workflow already has into fields of
// its inputs.
const TaskTypeValueWriter = "VALUE_WRITER"

// ValueWriterPlugin is a synchronous plugin that writes values into fields of a
// copy of the step's inputs, and writes the filled copy to the template's
// output_namespace. It is how a value issued once for a case reaches every
// place a form keeps it, such as one number in every item of a list. Each
// entry in plugin_properties.writes names a field and the value to write:
//
//	{"task_type": "VALUE_WRITER", "output_namespace": "written",
//	 "plugin_properties": {"writes": [
//	   {"path": "/order/customer_ref", "from": "/customer/ref"},
//	   {"each": "/order/lines", "path": "0/label", "template": "{region}-{sku}",
//	    "params": {"region": "/order/region", "sku": "0/sku"}}]}}
//
// With input_mapping {"order": "order", "customer": "customer"}, the node's
// output_mapping {"written.order": "order"} passes the order on with the
// values written.
//
// Pointers are those of REFID_GENERATOR: "/" from the root of the inputs, and
// with "each", which points at an array of objects, "0/" from the current
// element. With "each" the path must be relative, so every element is
// written. An entry takes its value from "from", copied as it is, or from
// "template", whose {name} placeholders are filled with the params' values,
// each a string. A field is always written, whatever it held, and a missing
// source fails the step rather than leaving the field as it was. Every value
// is read before any field is written, so the order of the entries doesn't
// change what they read.
type ValueWriterPlugin struct{}

// NewValueWriterPlugin builds the plugin.
func NewValueWriterPlugin() *ValueWriterPlugin { return &ValueWriterPlugin{} }

type valueWriterConfig struct {
	Writes []valueWriteSpec `json:"writes"`
}

// valueWriteSpec is one entry of plugin_properties.writes, as written.
type valueWriteSpec struct {
	Each     string            `json:"each,omitempty"`
	Path     string            `json:"path"`
	From     string            `json:"from,omitempty"`
	Template string            `json:"template,omitempty"`
	Params   map[string]string `json:"params,omitempty"`
}

// valueWriteEntry is a valueWriteSpec with its pointers parsed.
type valueWriteEntry struct {
	spec     valueWriteSpec
	each     string // absolute JSON Pointer, "" without "each"
	path     refIDPointer
	pathSegs []string
	from     *refIDPointer // nil for a template
	params   map[string]refIDPointer
}

// templatePlaceholder matches a {name} placeholder in a template.
var templatePlaceholder = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)

func (c valueWriterConfig) entries() ([]valueWriteEntry, error) {
	if len(c.Writes) == 0 {
		return nil, errors.New("plugin_properties.writes must list at least one value to write")
	}
	entries := make([]valueWriteEntry, 0, len(c.Writes))
	for i, s := range c.Writes {
		e, err := parseValueWriteEntry(s)
		if err != nil {
			return nil, fmt.Errorf("writes[%d]%w", i, err)
		}
		// Two paths in the same scope must not overlap: one field inside the
		// other would be written and then written through.
		for j := range entries {
			if entries[j].each == e.each && overlapping(entries[j].pathSegs, e.pathSegs) {
				return nil, fmt.Errorf("writes[%d].path %q overlaps writes[%d].path %q", i, s.Path, j, entries[j].spec.Path)
			}
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// parseValueWriteEntry validates one entry and parses its pointers. Its errors
// start with the field they concern, for entries to prefix with the index.
func parseValueWriteEntry(s valueWriteSpec) (valueWriteEntry, error) {
	e := valueWriteEntry{spec: s, params: make(map[string]refIDPointer, len(s.Params))}
	hasEach := s.Each != ""
	if hasEach {
		each, err := parseRefIDPointer(s.Each, false)
		if err != nil {
			return valueWriteEntry{}, fmt.Errorf(".each: %w", err)
		}
		e.each = each.ptr
	}

	path, err := parseRefIDPointer(s.Path, hasEach)
	if err != nil {
		return valueWriteEntry{}, fmt.Errorf(".path: %w", err)
	}
	if hasEach && !path.relative {
		return valueWriteEntry{}, fmt.Errorf(`.path %q must be relative ("0/…") with "each", so every element is written`, s.Path)
	}
	e.path = path
	if e.pathSegs, err = jsonpointer.Segments(path.ptr); err != nil {
		return valueWriteEntry{}, fmt.Errorf(".path: %w", err)
	}

	if err := e.parseSource(hasEach); err != nil {
		return valueWriteEntry{}, err
	}
	return e, nil
}

// parseSource checks that the entry has exactly one source, "from" or
// "template", and parses its pointers. A template's placeholders must all be
// params.
func (e *valueWriteEntry) parseSource(hasEach bool) error {
	switch {
	case e.spec.From != "" && e.spec.Template != "":
		return errors.New(` takes "from" or "template", not both`)
	case e.spec.From != "":
		if len(e.spec.Params) > 0 {
			return errors.New(`.params only fill a "template"`)
		}
		from, err := parseRefIDPointer(e.spec.From, hasEach)
		if err != nil {
			return fmt.Errorf(".from: %w", err)
		}
		e.from = &from
	case e.spec.Template != "":
		for name, raw := range e.spec.Params {
			if strings.TrimSpace(name) == "" {
				return errors.New(".params has an empty name")
			}
			p, err := parseRefIDPointer(raw, hasEach)
			if err != nil {
				return fmt.Errorf(".params.%s: %w", name, err)
			}
			e.params[name] = p
		}
		for _, m := range templatePlaceholder.FindAllStringSubmatch(e.spec.Template, -1) {
			if _, ok := e.params[m[1]]; !ok {
				return fmt.Errorf(".template uses {%s}, which .params doesn't name", m[1])
			}
		}
	default:
		return errors.New(` needs "from" or "template"`)
	}
	return nil
}

// valueWrite is one field to write, with its value already read.
type valueWrite struct {
	obj   map[string]any
	ptr   string
	label string
	value any
}

// Execute writes every field and returns nil (not ErrSuspended), so the engine
// advances immediately.
func (p *ValueWriterPlugin) Execute(ctx pluginContext, configRaw json.RawMessage) error {
	var cfg valueWriterConfig
	if err := json.Unmarshal(configRaw, &cfg); err != nil {
		return configError(fmt.Errorf("value_writer: invalid config: %w", err))
	}
	entries, err := cfg.entries()
	if err != nil {
		return configError(fmt.Errorf("value_writer: %w", err))
	}
	if ctx.OutputNamespace == "" {
		return configError(errors.New("value_writer: output_namespace is required to hold the written inputs"))
	}
	if ctx.Record == nil {
		return inputError(errors.New("value_writer: task record is nil"))
	}

	// The inputs belong to the caller, so the values are written into a copy.
	root := deepcopy.Map(ctx.Inputs)
	if root == nil {
		root = make(map[string]any)
	}

	var writes []valueWrite
	for i := range entries {
		w, err := collectValueWrites(root, &entries[i])
		if err != nil {
			return inputError(fmt.Errorf("value_writer: writes[%d]: %w", i, err))
		}
		writes = append(writes, w...)
	}

	for _, w := range writes {
		if !jsonpointer.Set(w.obj, w.ptr, w.value) {
			return inputError(fmt.Errorf("value_writer: cannot write %s", w.label))
		}
	}

	if ctx.Record.Data == nil {
		ctx.Record.Data = make(map[string]any)
	}
	ctx.Record.Data[ctx.OutputNamespace] = root
	return nil
}

// collectValueWrites resolves e against root: one write without "each", one
// per array element with it, and none for a missing or null array.
func collectValueWrites(root map[string]any, e *valueWriteEntry) ([]valueWrite, error) {
	if e.each == "" {
		w, err := newValueWrite(root, nil, e, e.path.ptr)
		if err != nil {
			return nil, err
		}
		return []valueWrite{w}, nil
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

	writes := make([]valueWrite, 0, len(items))
	for i, el := range items {
		elem, ok := el.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s/%d is a %T, not an object", e.each, i, el)
		}
		w, err := newValueWrite(root, elem, e, fmt.Sprintf("%s/%d%s", e.each, i, e.path.ptr))
		if err != nil {
			return nil, err
		}
		writes = append(writes, w)
	}
	return writes, nil
}

// newValueWrite checks that the field can be written and reads its value.
// elem is the current element, nil without "each".
func newValueWrite(root, elem map[string]any, e *valueWriteEntry, label string) (valueWrite, error) {
	obj := e.path.in(root, elem)
	if err := checkObjectPath(obj, e.path.ptr); err != nil {
		return valueWrite{}, fmt.Errorf("%s: %w", label, err)
	}

	var value any
	if e.from != nil {
		v, ok := jsonpointer.Get(e.from.in(root, elem), e.from.ptr)
		if !ok || v == nil {
			return valueWrite{}, fmt.Errorf("%s: %s has no value", label, e.from.raw)
		}
		value = deepcopy.Value(v)
	} else {
		params, err := readRefIDParams(root, elem, e.params)
		if err != nil {
			return valueWrite{}, fmt.Errorf("%s: %w", label, err)
		}
		for name, p := range e.params {
			if _, ok := params[name]; !ok {
				return valueWrite{}, fmt.Errorf("%s: param %q (%s) has no value", label, name, p.raw)
			}
		}
		value = templatePlaceholder.ReplaceAllStringFunc(e.spec.Template, func(m string) string {
			return params[m[1:len(m)-1]]
		})
	}
	return valueWrite{obj: obj, ptr: e.path.ptr, label: label, value: value}, nil
}
