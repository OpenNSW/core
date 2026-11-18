package payloadschema

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/message"
)

// ajvFormats are the formats on which this library and the trader app's
// ajv-formats (2.1.1, "full" mode) disagree, defined as ajv-formats defines
// them, so the server accepts exactly what the browser does. Formats the two
// agree on, such as "date", keep this library's definition.
var ajvFormats = []*jsonschema.Format{
	{Name: "time", Validate: validateTime},
	{Name: "email", Validate: validateEmail},
}

// ajvTime is ajv-formats' TIME: the timezone is optional, so "10:00:00", which
// the trader app sends, is valid. This library requires one.
var ajvTime = regexp.MustCompile(`(?i)^(\d\d):(\d\d):(\d\d)(\.\d+)?(z|[+-]\d\d(?::?\d\d)?)?$`)

// validateTime ports ajv-formats' time(str) with no timezone required: any
// hh:mm:ss up to 23:59:59, plus the leap second 23:59:60.
func validateTime(v any) error {
	s, ok := v.(string)
	if !ok {
		return nil
	}
	m := ajvTime.FindStringSubmatch(s)
	if m == nil {
		return errors.New("not a valid time")
	}
	hour, _ := strconv.Atoi(m[1])
	minute, _ := strconv.Atoi(m[2])
	second, _ := strconv.Atoi(m[3])
	if (hour <= 23 && minute <= 59 && second <= 59) || (hour == 23 && minute == 59 && second == 60) {
		return nil
	}
	return errors.New("not a valid time")
}

// ajvEmail is ajv-formats' full-mode email: the domain needs at least one dot,
// so "a@localhost" is invalid. This library accepts it.
var ajvEmail = regexp.MustCompile(`(?i)^[a-z0-9!#$%&'*+/=?^_` + "`" + `{|}~-]+(?:\.[a-z0-9!#$%&'*+/=?^_` + "`" + `{|}~-]+)*@(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)

// validateEmail ports ajv-formats' full-mode email check.
func validateEmail(v any) error {
	s, ok := v.(string)
	if !ok {
		return nil
	}
	if !ajvEmail.MatchString(s) {
		return errors.New("not a valid email")
	}
	return nil
}

// ajvDateLimit is ajv-formats' limits on a date (formatMaximum, formatMinimum
// and their exclusive forms) as the forms use them: with the limit read from a
// sibling value through Ajv's $data, such as {"formatMaximum": {"$data":
// "1/end"}}, so one date can't be later than another. This library has
// neither. Its keywords see their own value alone, so the limit is checked from
// the object holding both. Only that use is checked: a limit written in the
// schema, on another format, read from elsewhere, or on a value that isn't an
// object's property, is left to the browser.
var ajvDateLimit = &jsonschema.Vocabulary{
	URL:     "https://github.com/ajv-validator/ajv-formats#formatMaximum",
	Compile: compileDateLimit,
}

// ajvDateLimitKeywords are ajv-formats' limits, each with the order it
// requires of a date and its limit, and how a violation describes it.
var ajvDateLimitKeywords = []struct {
	keyword, rule string
	holds         func(cmp int) bool
}{
	{"formatMaximum", "must not be later than", func(cmp int) bool { return cmp <= 0 }},
	{"formatExclusiveMaximum", "must be earlier than", func(cmp int) bool { return cmp < 0 }},
	{"formatMinimum", "must not be earlier than", func(cmp int) bool { return cmp >= 0 }},
	{"formatExclusiveMinimum", "must be later than", func(cmp int) bool { return cmp > 0 }},
}

// compileDateLimit returns the date limits on the properties of obj, a schema.
func compileDateLimit(_ *jsonschema.CompilerContext, obj map[string]any) (jsonschema.SchemaExt, error) {
	limits := dateLimits{}
	props, _ := obj["properties"].(map[string]any)
	for name, p := range props {
		s, _ := p.(map[string]any)
		if l := dateLimitsOf(s); len(l) > 0 {
			limits[name] = l
		}
	}
	if len(limits) == 0 {
		return nil, nil
	}
	return limits, nil
}

// dateLimit is one limit on a date: the sibling it compares the date with, and
// the keyword's rule.
type dateLimit struct {
	keyword, rule, sibling string
	holds                  func(cmp int) bool
}

// dateLimitsOf returns the limits s, a schema, sets on a date as
// {"$data": "1/<sibling>"}, and none for any other schema.
func dateLimitsOf(s map[string]any) []dateLimit {
	if s["format"] != "date" {
		return nil
	}
	var out []dateLimit
	for _, kw := range ajvDateLimitKeywords {
		data, _ := s[kw.keyword].(map[string]any)
		ptr, _ := data["$data"].(string)
		sibling, relative := strings.CutPrefix(ptr, "1/")
		if len(data) == 1 && relative && !strings.ContainsAny(sibling, "/~") {
			out = append(out, dateLimit{keyword: kw.keyword, rule: kw.rule, sibling: sibling, holds: kw.holds})
		}
	}
	return out
}

// dateLimits maps each property of an object to the limits on its date.
type dateLimits map[string][]dateLimit

// Validate ports ajv-formats' limits with a $data limit: a value or a limit
// that is absent, or an empty date, passes; a limit that isn't a string fails;
// dates otherwise order as strings, as RFC 3339 full-dates do.
func (d dateLimits) Validate(ctx *jsonschema.ValidatorContext, v any) {
	obj, ok := v.(map[string]any)
	if !ok {
		return
	}
	for name, limits := range d {
		date, ok := obj[name].(string)
		if !ok {
			continue
		}
		for _, l := range limits {
			limit, found := obj[l.sibling]
			if !found {
				continue
			}
			if s, ok := limit.(string); ok && (date == "" || s == "" || l.holds(strings.Compare(date, s))) {
				continue
			}
			ctx.AddErr(&jsonschema.ValidationError{
				InstanceLocation: append(slices.Clone(ctx.ValueLocation()), name),
				ErrorKind:        &dateLimitKind{l},
			})
		}
	}
}

// dateLimitKind is a date that breaks a limit.
type dateLimitKind struct {
	dateLimit
}

func (k *dateLimitKind) KeywordPath() []string { return []string{k.keyword} }

func (k *dateLimitKind) LocalizedString(*message.Printer) string {
	return k.rule + " " + k.sibling
}
