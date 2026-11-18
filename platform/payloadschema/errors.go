package payloadschema

import (
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// ErrInvalidPayload is matched by every error reporting a submission that
// doesn't match its form, so the task handler can answer it with 400.
// errors.Is still matches after the orchestrator wraps it with %w.
var ErrInvalidPayload = errors.New("payload schema: invalid payload")

// Violation is one way a submission fails its form: Path is a JSON Pointer
// into the submitted payload, "" for the payload as a whole. Violations are
// logged, not returned to the caller, and Message names the failing rule
// without echoing the submitted value, so what a trader typed stays out of
// the logs.
type Violation struct {
	Path    string
	Message string
}

// InvalidPayloadError reports a submission that doesn't match its form. It
// matches ErrInvalidPayload.
type InvalidPayloadError struct {
	Violations []Violation
}

func (e *InvalidPayloadError) Error() string {
	parts := make([]string, len(e.Violations))
	for i, v := range e.Violations {
		parts[i] = v.Path + ": " + v.Message
	}
	return ErrInvalidPayload.Error() + ": " + strings.Join(parts, "; ")
}

// Is makes errors.Is(err, ErrInvalidPayload) true.
func (e *InvalidPayloadError) Is(target error) bool {
	return target == ErrInvalidPayload
}

var printer = message.NewPrinter(language.English)

// violations flattens the library's tree of errors into the failures a caller
// can act on: one per missing property, at the property, and one per failing
// rule elsewhere. A failed oneOf or anyOf is one failure at its value, not one
// per alternative.
func violations(err *jsonschema.ValidationError) []Violation {
	var out []Violation
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		switch k := e.ErrorKind.(type) {
		case *kind.Required:
			for _, name := range k.Missing {
				out = append(out, Violation{Path: pointer(append(slices.Clone(e.InstanceLocation), name)), Message: "missing property"})
			}
			return
		case *kind.OneOf, *kind.AnyOf:
			out = append(out, Violation{Path: pointer(e.InstanceLocation), Message: alternativesMessage(e)})
			return
		}
		if len(e.Causes) == 0 {
			out = append(out, Violation{Path: pointer(e.InstanceLocation), Message: ruleMessage(e.ErrorKind)})
			return
		}
		for _, cause := range e.Causes {
			walk(cause)
		}
	}
	walk(err)
	return out
}

// alternativesMessage describes a failed oneOf or anyOf. When every
// alternative is a const, as the forms write their choice lists, it names the
// allowed values.
func alternativesMessage(e *jsonschema.ValidationError) string {
	allowed := make([]string, 0, len(e.Causes))
	for _, cause := range e.Causes {
		c, ok := cause.ErrorKind.(*kind.Const)
		if !ok || len(cause.Causes) > 0 {
			return e.ErrorKind.LocalizedString(printer)
		}
		allowed = append(allowed, fmt.Sprintf("%q", fmt.Sprint(c.Want)))
	}
	if len(allowed) == 0 {
		return e.ErrorKind.LocalizedString(printer)
	}
	return "value must be one of " + strings.Join(allowed, ", ")
}

// ruleMessage describes a failed rule without the submitted value. It keeps
// the library's message only for the rules known not to echo the value,
// describes the ones known to echo it from the schema side, and names any
// other rule by its keyword alone, so a rule this list doesn't know, or a
// library change to a message, can't put a submitted value in the log.
func ruleMessage(k jsonschema.ErrorKind) string {
	switch k := k.(type) {
	case *kind.Type, *kind.Const, *kind.Enum,
		*kind.MinLength, *kind.MaxLength,
		*kind.MinItems, *kind.MaxItems,
		*kind.MinProperties, *kind.MaxProperties,
		*kind.Dependency, *kind.DependentRequired,
		*kind.UniqueItems, *kind.Contains,
		*kind.Not, *kind.AllOf, *kind.FalseSchema:
		return k.LocalizedString(printer)
	case *kind.Format:
		return "not a valid " + k.Want
	case *kind.Pattern:
		return "does not match pattern " + strconv.Quote(k.Want)
	case *kind.Minimum:
		return "must be >= " + number(k.Want)
	case *kind.Maximum:
		return "must be <= " + number(k.Want)
	case *kind.ExclusiveMinimum:
		return "must be > " + number(k.Want)
	case *kind.ExclusiveMaximum:
		return "must be < " + number(k.Want)
	case *kind.MultipleOf:
		return "must be a multiple of " + number(k.Want)
	case *dateLimitKind, *xFileKind:
		return k.LocalizedString(printer)
	}
	return "'" + strings.Join(k.KeywordPath(), "/") + "' failed"
}

// number formats a schema limit as a plain decimal.
func number(r *big.Rat) string {
	f, _ := r.Float64()
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// pointer returns the JSON Pointer (RFC 6901) for location.
func pointer(location []string) string {
	escape := strings.NewReplacer("~", "~0", "/", "~1")
	var b strings.Builder
	for _, token := range location {
		b.WriteString("/")
		b.WriteString(escape.Replace(token))
	}
	return b.String()
}
