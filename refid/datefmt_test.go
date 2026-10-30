// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package refid

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func renderFormat(t *testing.T, format string, at time.Time) string {
	t.Helper()
	f, err := compileDateFormat(format)
	if err != nil {
		t.Fatalf("compileDateFormat(%q): %v", format, err)
	}
	return f.render(at)
}

// Two instants chosen so that each field is a single digit in one and two digits
// in the other, which is what tells a padded token from an unpadded one.
var (
	smallFields = time.Date(2026, time.March, 5, 4, 7, 9, 0, time.UTC)
	largeFields = time.Date(2026, time.December, 25, 14, 37, 19, 0, time.UTC)
)

func TestDateFormat_Render(t *testing.T) {
	tests := []struct {
		format string
		small  string // smallFields: 2026-03-05 04:07:09
		large  string // largeFields: 2026-12-25 14:37:19
	}{
		{"yyyyMMdd", "20260305", "20261225"},
		{"yyyyMd", "202635", "20261225"},
		{"yyMMdd", "260305", "261225"},
		{"d/M/yyyy", "5/3/2026", "25/12/2026"},
		{"HH:mm:ss", "04:07:09", "14:37:19"},
		{"H:m:s", "4:7:9", "14:37:19"},
		{"yyyyMMddHHmmss", "20260305040709", "20261225143719"},
		{"yyyy-MM-dd'T'HH:mm", "2026-03-05T04:07", "2026-12-25T14:37"},
		{"yyyy.MM.dd HH.mm", "2026.03.05 04.07", "2026.12.25 14.37"},
		{"yyyy", "2026", "2026"},
		{"yy", "26", "26"},
		{"MM", "03", "12"},
		{"M", "3", "12"},
		{"dd", "05", "25"},
		{"d", "5", "25"},
		{"HH", "04", "14"},
		{"H", "4", "14"},
		{"mm", "07", "37"},
		{"m", "7", "37"},
		{"ss", "09", "19"},
		{"s", "9", "19"},
	}
	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			if got := renderFormat(t, tt.format, smallFields); got != tt.small {
				t.Errorf("at %s: %q, want %q", smallFields.Format(time.DateTime), got, tt.small)
			}
			if got := renderFormat(t, tt.format, largeFields); got != tt.large {
				t.Errorf("at %s: %q, want %q", largeFields.Format(time.DateTime), got, tt.large)
			}
		})
	}
}

// A run of the same letter is one token and a change of letter starts the next,
// so adjacent fields need no separator.
func TestDateFormat_AdjacentFieldsSplitByLetter(t *testing.T) {
	if got := renderFormat(t, "yyyyMd", smallFields); got != "202635" {
		t.Errorf("yyyyMd = %q, want 202635 (yyyy, M, d)", got)
	}
	if got := renderFormat(t, "MMdd", smallFields); got != "0305" {
		t.Errorf("MMdd = %q, want 0305 (MM, dd)", got)
	}
}

// Unpadded fields trade a fixed width for brevity, and the README warns that two
// different dates can then render the same. This pins that warning, and that
// the padded form tells them apart.
func TestDateFormat_UnpaddedFieldsCanCollide(t *testing.T) {
	jan11 := time.Date(2026, time.January, 11, 0, 0, 0, 0, time.UTC)
	nov1 := time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC)

	a, b := renderFormat(t, "yyyyMd", jan11), renderFormat(t, "yyyyMd", nov1)
	if a != "2026111" || b != "2026111" {
		t.Errorf("yyyyMd gives %q for 11 January and %q for 1 November, want 2026111 for both", a, b)
	}

	a, b = renderFormat(t, "yyyyMMdd", jan11), renderFormat(t, "yyyyMMdd", nov1)
	if a != "20260111" || b != "20261101" {
		t.Errorf("yyyyMMdd gives %q and %q, want 20260111 and 20261101", a, b)
	}
}

func TestDateFormat_Literals(t *testing.T) {
	tests := []struct {
		name   string
		format string
		want   string
	}{
		{"word Go would read as tokens", "'Month' yyyy", "Month 2026"},
		{"quoted letters between fields", "yyyy'T'MM", "2026T03"},
		{"apostrophe on its own", "yyyy''MM", "2026'03"},
		{"apostrophe inside a literal", "'it''s' yyyy", "it's 2026"},
		{"digits are allowed inside quotes", "yyyy'-Q1-'MM", "2026-Q1-03"},
		{"quoted punctuation", "yyyy'/'MM", "2026/03"},
		{"unquoted punctuation and space", "dd-MM-yyyy HH:mm", "05-03-2026 04:07"},
		{"non-ASCII text outside quotes", "yyyy年MM月", "2026年03月"},
		{"non-ASCII text inside quotes", "'№' yyyy", "№ 2026"},
		{"literal at both ends", "'['yyyy']'", "[2026]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := renderFormat(t, tt.format, smallFields); got != tt.want {
				t.Errorf("%q = %q, want %q", tt.format, got, tt.want)
			}
		})
	}
}

func TestDateFormat_Rejected(t *testing.T) {
	tests := []struct {
		name    string
		format  string
		wantErr string
	}{
		// No field to render.
		{"empty", "", "no date or time field"},
		{"only a quoted literal", "'T'", "no date or time field"},
		{"only punctuation", "-", "no date or time field"},
		{"only a slash", "/", "no date or time field"},
		{"only a space", " ", "no date or time field"},
		{"only whitespace", " \t ", "no date or time field"},

		// Letters from other pattern languages with another meaning.
		{"uppercase Y is week-year", "YYYYMMDD", "week-based year"},
		{"uppercase D is day of year", "yyyyDD", "day of the year"},
		{"hint names the replacement for Y", "YYYY", `"yyyy" or "yy"`},
		{"hint names the replacement for D", "yyyyMMD", `"dd" or "d"`},

		// What an old Go reference layout looks like.
		{"Go layout 20060102", "20060102", "Go reference layout"},
		{"Go layout 2006/01/", "2006/01/", "Go reference layout"},
		{"Go layout with letters", "2006-Jan-02", "Go reference layout"},
		{"unquoted digit after a field", "yyyy1", "unquoted digit"},
		{"hint shows the migration", "20060102", `"yyyyMMdd"`},
		{"the typo from the issue", "2006909", "Go reference layout"},
		{"the typo is not read as year, month, day", "2006909", `unquoted digit "2"`},

		// Unknown letters.
		{"unknown letter J", "Jan", `unknown letter "J"`},
		{"unquoted T", "yyyyMMddTHHmm", `unknown letter "T"`},
		{"unknown lowercase letter", "yyyyj", `unknown letter "j"`},
		{"unknown letter hint mentions quotes", "yyyyT", "single quotes"},

		// A run length that is not a form of the field.
		{"three y", "yyy", `"yyy" is not a form of the year field`},
		{"five y", "yyyyy", `"yyyyy" is not a form of the year field`},
		{"three M are month names", "MMM", "month names are not supported"},
		{"four M are month names", "MMMM", "month names are not supported"},
		{"three d", "ddd", `"ddd" is not a form of the day of the month field`},
		{"three H", "HHH", `"HHH" is not a form of the hour (0-23) field`},
		{"three m", "mmm", `"mmm" is not a form of the minute field`},
		{"three s", "sss", `"sss" is not a form of the second field`},
		{"hint lists the valid forms", "yyy", `"yyyy" or "yy"`},

		// Fields other pattern languages have that this one does not.
		{"12-hour h", "hh", "12-hour time"},
		{"AM/PM", "yyyy a", "AM/PM"},
		{"day name", "E yyyy", "day names"},
		{"fraction of a second", "HH:mm:ss.SSS", "fractions of a second"},
		{"time zone", "yyyy z", "time zones"},
		{"quarter", "yyyyQ", "quarters"},
		{"week number", "yyyyww", "week numbers"},
		{"hours numbered 1 to 24", "k", "hours numbered 1 to 24"},

		// Quoting.
		{"unterminated quote", "yyyy'", "unterminated quote"},
		{"unterminated literal", "'abc", "unterminated quote"},
		{"quote closed by an apostrophe pair only", "yyyy'abc''", "unterminated quote"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := compileDateFormat(tt.format)
			if err == nil {
				t.Fatalf("compileDateFormat(%q) succeeded, want an error containing %q", tt.format, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("compileDateFormat(%q) error = %q, want it to contain %q", tt.format, err, tt.wantErr)
			}
		})
	}
}

// goLayouts are the Go reference layouts that format the same fields, so Go's
// own formatter is the oracle for what each token should print. Go has no
// unpadded 24-hour field, so H is checked against strconv.
var goLayouts = []struct {
	format string
	layout string
}{
	{"yyyy", "2006"},
	{"yy", "06"},
	{"MM", "01"},
	{"M", "1"},
	{"dd", "02"},
	{"d", "2"},
	{"HH", "15"},
	{"mm", "04"},
	{"m", "4"},
	{"ss", "05"},
	{"s", "5"},
	{"yyyyMMdd", "20060102"},
	{"yyyyMMdd HH:mm:ss", "20060102 15:04:05"},
	{"d/M/yyyy", "2/1/2006"},
}

// Every day of 2024, a leap year, at hours and minutes that vary so each field
// takes single- and double-digit values, in UTC and in a zone that is not.
func TestDateFormat_MatchesGoFormatter(t *testing.T) {
	colombo := mustLocation(t, "Asia/Colombo")
	start := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)

	compiled := make(map[string]dateFormat, len(goLayouts))
	for _, g := range goLayouts {
		compiled[g.format] = mustCompileDateFormat(g.format)
	}

	for day := 0; day < 366; day++ {
		for _, hour := range []int{0, 5, 12, 23} {
			base := start.AddDate(0, 0, day).Add(time.Duration(hour) * time.Hour)
			at := base.Add(time.Duration(day*7%60)*time.Minute + time.Duration(day*13%60)*time.Second)
			for _, in := range []time.Time{at, at.In(colombo)} {
				for _, g := range goLayouts {
					if got, want := compiled[g.format].render(in), in.Format(g.layout); got != want {
						t.Fatalf("%q at %s = %q, Go layout %q gives %q", g.format, in.Format(time.RFC3339), got, g.layout, want)
					}
				}
				if got, want := renderFormat(t, "H", in), strconv.Itoa(in.Hour()); got != want {
					t.Fatalf("H at %s = %q, want %q", in.Format(time.RFC3339), got, want)
				}
			}
		}
	}
}

// The year and the century boundary are where a two-digit or four-digit year
// goes wrong, and years outside 1 to 9999 are where a hand-written formatter
// drifts from Go's: a negative year keeps its sign in front of the padded
// digits, and yy uses the absolute year.
func TestDateFormat_YearEdges(t *testing.T) {
	tests := []struct {
		at       time.Time
		yyyy, yy string
	}{
		{time.Date(1999, 12, 31, 23, 59, 59, 0, time.UTC), "1999", "99"},
		{time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), "2000", "00"},
		{time.Date(2009, 6, 1, 0, 0, 0, 0, time.UTC), "2009", "09"},
		{time.Date(99, 6, 1, 0, 0, 0, 0, time.UTC), "0099", "99"},
		{time.Date(9999, 6, 1, 0, 0, 0, 0, time.UTC), "9999", "99"},
		{time.Date(0, 6, 1, 0, 0, 0, 0, time.UTC), "0000", "00"},
		{time.Date(10000, 6, 1, 0, 0, 0, 0, time.UTC), "10000", "00"},
		{time.Date(-1, 6, 1, 0, 0, 0, 0, time.UTC), "-0001", "01"},
		{time.Date(-99, 6, 1, 0, 0, 0, 0, time.UTC), "-0099", "99"},
		{time.Date(-1234, 6, 1, 0, 0, 0, 0, time.UTC), "-1234", "34"},
	}
	for _, tt := range tests {
		t.Run(tt.at.Format("2006"), func(t *testing.T) {
			if got := renderFormat(t, "yyyy", tt.at); got != tt.yyyy || got != tt.at.Format("2006") {
				t.Errorf("yyyy = %q, want %q (Go gives %q)", got, tt.yyyy, tt.at.Format("2006"))
			}
			if got := renderFormat(t, "yy", tt.at); got != tt.yy || got != tt.at.Format("06") {
				t.Errorf("yy = %q, want %q (Go gives %q)", got, tt.yy, tt.at.Format("06"))
			}
		})
	}
}

// The scope-key placeholders render through the same engine and keep the
// output they had when they were Go layouts.
func TestResolveScopeKey_DatePlaceholdersMatchGoFormatter(t *testing.T) {
	start := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	for day := 0; day < 366; day++ {
		at := start.AddDate(0, 0, day).Add(time.Duration(day%24) * time.Hour)
		got, err := resolveScopeKey("{yyyy}|{yyyyMM}|{yyyyMMdd}", "ACME", "ref", nil, at)
		if err != nil {
			t.Fatal(err)
		}
		want := at.Format("2006") + "|" + at.Format("200601") + "|" + at.Format("20060102")
		if got != want {
			t.Fatalf("at %s: scope key = %q, want %q", at.Format(time.RFC3339), got, want)
		}
	}
}

func TestMustCompileDateFormat_PanicsOnInvalid(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("mustCompileDateFormat did not panic on an invalid format")
		}
	}()
	mustCompileDateFormat("20060102")
}
