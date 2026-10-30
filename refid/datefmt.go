// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package refid

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// datePart renders one piece of a date format from a time: fixed text or one
// field of the time.
type datePart func(time.Time) string

// dateFormat is a compiled date format, the parts of a pattern such as
// "yyyyMMdd" in order. It renders fields directly instead of translating the
// pattern to a Go reference layout, because Go has no unpadded 24-hour field
// and would read quoted literal text as layout tokens.
type dateFormat []datePart

func (f dateFormat) render(t time.Time) string {
	var b strings.Builder
	for _, part := range f {
		b.WriteString(part(t))
	}
	return b.String()
}

// dateForm is one spelling of a field: "MM" and "M" are the padded and
// unpadded forms of the month.
type dateForm struct {
	token  string
	render datePart
}

// dateField is a letter a date format accepts and the forms of it that exist.
type dateField struct {
	name  string
	forms []dateForm
}

// dateFields is the table of letters a date format accepts. A run of the same
// letter is one token, and a repeated letter means zero padding. Adding a field
// is one more entry here.
var dateFields = map[byte]dateField{
	'y': {"year", []dateForm{
		{"yyyy", intPart(func(t time.Time) int { return t.Year() }, 4)},
		{"yy", intPart(yearOfCentury, 2)},
	}},
	'M': {"month", []dateForm{
		{"MM", intPart(func(t time.Time) int { return int(t.Month()) }, 2)},
		{"M", intPart(func(t time.Time) int { return int(t.Month()) }, 1)},
	}},
	'd': {"day of the month", []dateForm{
		{"dd", intPart(func(t time.Time) int { return t.Day() }, 2)},
		{"d", intPart(func(t time.Time) int { return t.Day() }, 1)},
	}},
	'H': {"hour (0-23)", []dateForm{
		{"HH", intPart(func(t time.Time) int { return t.Hour() }, 2)},
		{"H", intPart(func(t time.Time) int { return t.Hour() }, 1)},
	}},
	'm': {"minute", []dateForm{
		{"mm", intPart(func(t time.Time) int { return t.Minute() }, 2)},
		{"m", intPart(func(t time.Time) int { return t.Minute() }, 1)},
	}},
	's': {"second", []dateForm{
		{"ss", intPart(func(t time.Time) int { return t.Second() }, 2)},
		{"s", intPart(func(t time.Time) int { return t.Second() }, 1)},
	}},
}

// unsupportedDateLetters names what the letters that other pattern languages
// use would mean, so a format that asks for one gets "not supported" instead of
// "unknown".
var unsupportedDateLetters = map[byte]string{
	'h': "12-hour time", 'K': "12-hour time", 'a': "AM/PM markers", 'k': "hours numbered 1 to 24",
	'E': "day names", 'e': "day names", 'c': "day names", 'L': "month names",
	'S': "fractions of a second", 'A': "fractions of a second", 'n': "fractions of a second", 'N': "fractions of a second",
	'z': "time zones", 'Z': "time zones", 'X': "time zones", 'x': "time zones", 'O': "time zones", 'V': "time zones", 'v': "time zones",
	'G': "eras", 'Q': "quarters", 'q': "quarters", 'w': "week numbers", 'W': "week numbers",
}

// intPart renders an integer field, zero-padded to width. A width of 1 leaves it
// unpadded. A negative value (only a year before 1 BC can be one) keeps its sign
// in front of the padded digits, as Go's own formatter does: -0001, not 00-1.
func intPart(get func(time.Time) int, width int) datePart {
	return func(t time.Time) string {
		n, sign := get(t), ""
		if n < 0 {
			n, sign = -n, "-"
		}
		s := strconv.Itoa(n)
		if len(s) < width {
			s = strings.Repeat("0", width-len(s)) + s
		}
		return sign + s
	}
}

// yearOfCentury is the last two digits of the year. Like Go's formatter it uses
// the absolute year, so year -1 gives 01 and not -1.
func yearOfCentury(t time.Time) int {
	y := t.Year()
	if y < 0 {
		y = -y
	}
	return y % 100
}

func literalPart(s string) datePart {
	return func(time.Time) string { return s }
}

// compileDateFormat parses a date format such as "yyyyMMdd" or
// "yyyy-MM-dd'T'HH:mm" into a dateFormat. A repeated letter means zero padding
// ("MM" is 03, "M" is 3), text in single quotes is literal (a doubled single
// quote is an apostrophe), and any other character that is not an ASCII letter
// or digit is literal as written. It rejects everything it does not understand, so a typo
// fails at startup instead of rendering wrong IDs.
func compileDateFormat(format string) (dateFormat, error) {
	var (
		parts  dateFormat
		lit    strings.Builder
		fields int
	)
	flush := func() {
		if lit.Len() > 0 {
			parts = append(parts, literalPart(lit.String()))
			lit.Reset()
		}
	}

	for i := 0; i < len(format); {
		c := format[i]
		switch {
		case c == '\'':
			next, err := scanQuoted(format, i, &lit)
			if err != nil {
				return nil, err
			}
			i = next

		case isASCIILetter(c):
			end := i
			for end < len(format) && format[end] == c {
				end++
			}
			render, err := fieldForRun(c, end-i)
			if err != nil {
				return nil, err
			}
			flush()
			parts = append(parts, render)
			fields++
			i = end

		case c >= '0' && c <= '9':
			return nil, fmt.Errorf("unquoted digit %q: this looks like a Go reference layout; "+
				"write the fields as letters (for example \"yyyyMMdd\" for \"20060102\") "+
				"and put literal digits in single quotes", string(c))

		default:
			lit.WriteByte(c)
			i++
		}
	}
	flush()

	if fields == 0 {
		return nil, fmt.Errorf("it has no date or time field; use a literal segment for fixed text")
	}
	return parts, nil
}

// scanQuoted reads the literal that starts at the quote at format[start],
// appends its text to lit, and returns the index just past it. A doubled quote
// is an apostrophe, both inside a literal and on its own.
func scanQuoted(format string, start int, lit *strings.Builder) (int, error) {
	if start+1 < len(format) && format[start+1] == '\'' {
		lit.WriteByte('\'')
		return start + 2, nil
	}
	for i := start + 1; i < len(format); i++ {
		if format[i] != '\'' {
			lit.WriteByte(format[i])
			continue
		}
		if i+1 < len(format) && format[i+1] == '\'' {
			lit.WriteByte('\'')
			i++
			continue
		}
		return i + 1, nil
	}
	return 0, fmt.Errorf("unterminated quote: the literal text that starts at %q needs a closing '", format[start:])
}

// fieldForRun returns the renderer for a run of n copies of the letter c.
func fieldForRun(c byte, n int) (datePart, error) {
	field, ok := dateFields[c]
	if !ok {
		return nil, unknownLetterError(c)
	}
	for _, form := range field.forms {
		if len(form.token) == n {
			return form.render, nil
		}
	}

	run := strings.Repeat(string(c), n)
	tokens := make([]string, len(field.forms))
	for i, form := range field.forms {
		tokens[i] = strconv.Quote(form.token)
	}
	msg := fmt.Sprintf("%q is not a form of the %s field; use %s", run, field.name, strings.Join(tokens, " or "))
	if c == 'M' && n >= 3 {
		msg += " (month names are not supported)"
	}
	return nil, fmt.Errorf("%s", msg)
}

func unknownLetterError(c byte) error {
	switch c {
	case 'Y':
		return fmt.Errorf("%q is not a format letter (in Java and CLDR it is the week-based year, "+
			"which differs from the year around 1 January); use \"yyyy\" or \"yy\" for the year", string(c))
	case 'D':
		return fmt.Errorf("%q is not a format letter (in Java and CLDR it is the day of the year); "+
			"use \"dd\" or \"d\" for the day of the month", string(c))
	}
	if what, ok := unsupportedDateLetters[c]; ok {
		return fmt.Errorf("%q (%s) is not supported", string(c), what)
	}
	return fmt.Errorf("unknown letter %q: the format letters are y, M, d, H, m and s, "+
		"and literal letters go in single quotes (for example 'T')", string(c))
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
