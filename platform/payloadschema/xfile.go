package payloadschema

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/message"
)

// xFileRules is the forms' x-file keyword as the trader app's file control
// reads it: on a file field, one file ({"type": "string", "format": "file"}) or
// a list of them ({"type": "array", "items": {"type": "string", "format":
// "file"}}), as {"x-file": {"accept": ".pdf,.png", "maxFiles": 10}}. Each file
// must match accept, and a list may hold at most maxFiles. A rule the field
// doesn't set isn't checked: the deployment's upload limits apply to it, and
// the file control's defaults and uiSchema options apply in the browser only.
// maxSize is the upload's concern and isn't checked here.
//
// A file is checked by its stored value alone, nothing is looked up: whether
// its upload was made and what it holds are not checked.
var xFileRules = &jsonschema.Vocabulary{
	URL:     "https://github.com/OpenNSW/ui-packages#x-file",
	Compile: compileXFile,
}

// compileXFile returns the x-file rules obj, a schema, sets on a file field,
// and none for any other schema. A rule of the wrong type means what it means
// to the file control, in JavaScript: an accept that isn't text accepts no
// file, as matching one against it throws, and a maxFiles given as text is
// the number it reads as, or no limit when it reads as none.
func compileXFile(_ *jsonschema.CompilerContext, obj map[string]any) (jsonschema.SchemaExt, error) {
	opts, ok := obj["x-file"].(map[string]any)
	if !ok || !isFileField(obj) {
		return nil, nil
	}
	rules := &xFile{}
	switch accept := opts["accept"].(type) {
	case nil:
	case string:
		rules.accept = &accept
	default:
		rules.acceptsNone = true
	}
	rules.maxFiles = maxFilesOf(opts["maxFiles"])
	return rules, nil
}

// maxFilesOf returns the most files a list may hold under maxFiles, as the
// file control lets them be added: one at a time while the list holds fewer
// than maxFiles. It is nil for no limit: a maxFiles that is absent, or that
// JavaScript reads as no number.
func maxFilesOf(maxFiles any) *int {
	var n float64
	switch v := maxFiles.(type) {
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return nil
		}
		n = f
	case string:
		// JavaScript reads blank text as 0.
		if t := strings.TrimSpace(v); t != "" {
			f, err := strconv.ParseFloat(t, 64)
			if err != nil {
				return nil
			}
			n = f
		}
	default:
		return nil
	}
	// No length reaches Infinity, and none compares with NaN.
	if math.IsInf(n, 1) || math.IsNaN(n) {
		return nil
	}
	most := int(math.Max(math.Ceil(n), 0))
	return &most
}

// isFileField reports whether s, a schema, is one the trader app renders with
// its file control.
func isFileField(s map[string]any) bool {
	isFile := func(s map[string]any) bool { return s["type"] == "string" && s["format"] == "file" }
	if isFile(s) {
		return true
	}
	items, _ := s["items"].(map[string]any)
	return s["type"] == "array" && items != nil && isFile(items)
}

// xFile is the x-file rules on one file field.
type xFile struct {
	accept      *string
	acceptsNone bool
	maxFiles    *int
}

// Validate checks the files of a field: one file, or a list of them.
func (x *xFile) Validate(ctx *jsonschema.ValidatorContext, v any) {
	switch v := v.(type) {
	case string:
		if kind := x.check(v); kind != nil {
			ctx.AddError(kind)
		}
	case []any:
		if x.maxFiles != nil && len(v) > *x.maxFiles {
			files := "files"
			if *x.maxFiles == 1 {
				files = "file"
			}
			ctx.AddError(&xFileKind{keyword: "maxFiles", message: fmt.Sprintf("more than %d %s", *x.maxFiles, files)})
		}
		for i, el := range v {
			value, ok := el.(string)
			if !ok {
				continue
			}
			if kind := x.check(value); kind != nil {
				ctx.AddErr(&jsonschema.ValidationError{
					InstanceLocation: append(slices.Clone(ctx.ValueLocation()), strconv.Itoa(i)),
					ErrorKind:        kind,
				})
			}
		}
	}
}

// check returns how the file stored under value breaks the field's rules, or
// nil. An empty value names no file, so it is refused: the file control drops
// a field it holds no file for rather than sending one. Swapping file
// references for stored values passes an empty value through, so this is
// where it is refused.
func (x *xFile) check(value string) *xFileKind {
	switch {
	case value == "":
		return &xFileKind{keyword: "empty", message: "empty value instead of a file"}
	case x.acceptsNone:
		return &xFileKind{keyword: "accept", message: "file type not accepted (accept is not text)"}
	case x.accept != nil && !accepts(*x.accept, value):
		return &xFileKind{keyword: "accept", message: "file type not accepted (accept: " + strconv.Quote(*x.accept) + ")"}
	}
	return nil
}

// accepts reports whether value, a stored file value, matches accept, a
// comma-separated list, as the trader app's matchesAccept matches a file's
// name: "*" and "*/*" match any file, and ".<ext>" a value that ends with it,
// whatever its case. The value ends with the file's extension, which storage
// matched to its type on upload. Any other entry names a type, which the value
// doesn't carry, so it matches nothing.
func accepts(accept, value string) bool {
	for _, entry := range strings.Split(accept, ",") {
		entry = strings.TrimSpace(entry)
		switch {
		case entry == "*" || entry == "*/*":
			return true
		case strings.HasPrefix(entry, ".") && strings.HasSuffix(strings.ToLower(value), strings.ToLower(entry)):
			return true
		}
	}
	return false
}

// dropReadOnlyFiles removes from err, in place, the x-file failures of values
// readOnly reports server-owned, and reports whether any failure is left. The
// read-only check holds those values to what the step stored, and their files
// came in through another form, under its rules, so this form's x-file has
// nothing to judge there.
func dropReadOnlyFiles(err *jsonschema.ValidationError, readOnly func(location []string) bool) bool {
	if len(err.Causes) == 0 {
		_, isFile := err.ErrorKind.(*xFileKind)
		return !isFile || !readOnly(err.InstanceLocation)
	}
	kept := err.Causes[:0]
	for _, cause := range err.Causes {
		if dropReadOnlyFiles(cause, readOnly) {
			kept = append(kept, cause)
		}
	}
	err.Causes = kept
	return len(kept) > 0
}

// xFileKind is a file field that breaks one of its x-file rules.
type xFileKind struct {
	keyword, message string
}

func (k *xFileKind) KeywordPath() []string { return []string{"x-file", k.keyword} }

func (k *xFileKind) LocalizedString(*message.Printer) string { return k.message }
