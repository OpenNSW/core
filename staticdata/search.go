package staticdata

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/OpenNSW/core/pagination"
)

// The keys every row has: const is the value a field stores, title its label.
const (
	constKey = "const"
	titleKey = "title"
)

// defaultSearchKeys are the row keys a search matches when the request names none: the
// label, then the stored value.
var defaultSearchKeys = []string{titleKey, constKey}

// parentsKey is the row key x-search.dependsOn filters on: the values of the sibling field
// a row belongs to.
const parentsKey = "parents"

// SearchResult is the pagination envelope returned when a static-data request includes q,
// offset, or limit. Each item is a row exactly as the artifact has it, so every key reaches
// the client.
type SearchResult = pagination.Page[json.RawMessage]

// Option is the const and title of one static_data row, as Find returns them.
type Option struct {
	Const string `json:"const"`
	Title string `json:"title"`
}

// row is one object of a static_data artifact, with what Search and Find read from it.
type row struct {
	raw   json.RawMessage
	value string // the row's const
	title string
	// texts holds the lowercased text of each search key, in search-key order; "" when the
	// row has no searchable value for that key.
	texts   []string
	parents []string
}

// Search loads nothing itself: raw is one static_data artifact body. Rows are the objects in
// its "data" array; other entries are skipped. Every row must have a non-empty string const
// and title, else it is an error; any other keys are kept. Search drops rows whose parents
// list does not include parent (an empty parent keeps every row), ranks the remainder
// against query on searchKeys (empty means title, then const), and returns one page of the
// original rows. A row with no searchable value for a key just cannot match on that key.
// query is matched case-insensitively. An empty query keeps artifact order.
func Search(raw json.RawMessage, query, parent string, searchKeys []string, offset, limit int) (SearchResult, error) {
	if len(searchKeys) == 0 {
		searchKeys = defaultSearchKeys
	}
	rows, err := parseRows(raw, searchKeys)
	if err != nil {
		return SearchResult{}, err
	}
	scoped := filterByParent(rows, parent)
	matched := rank(scoped, query)
	page := pageRows(matched, offset, limit)
	items := make([]json.RawMessage, len(page))
	for i, r := range page {
		items[i] = r.raw
	}
	return pagination.NewPageResult(items, int64(len(matched)), offset, limit), nil
}

// Find returns the const and title of the first row of raw whose const is value, and
// whether there is one. raw is one static_data artifact body, as Load returns it, and its
// rows are read as Search reads them, so a row without a const or a title is an error.
// Unlike Search, Find matches the const exactly and never the title.
func Find(raw json.RawMessage, value string) (Option, bool, error) {
	rows, err := parseRows(raw, nil)
	if err != nil {
		return Option{}, false, err
	}
	for _, r := range rows {
		if r.value == value {
			return Option{Const: r.value, Title: r.title}, true, nil
		}
	}
	return Option{}, false, nil
}

// filterByParent runs before ranking and pagination so page contents and total
// describe the same parent scope. A row with no parents list does not match.
func filterByParent(rows []row, parent string) []row {
	if parent == "" {
		return rows
	}
	scoped := make([]row, 0, len(rows))
	for _, r := range rows {
		if slices.Contains(r.parents, parent) {
			scoped = append(scoped, r)
		}
	}
	return scoped
}

func parseRows(raw json.RawMessage, searchKeys []string) ([]row, error) {
	var envelope struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("parse static data rows: %w", err)
	}
	rows := make([]row, 0, len(envelope.Data))
	for i, item := range envelope.Data {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(item, &fields); err != nil || fields == nil {
			continue
		}
		value, title := stringField(fields, constKey), stringField(fields, titleKey)
		if value == "" || title == "" {
			return nil, fmt.Errorf("static data row at index %d must have a non-empty string const and title", i)
		}
		texts := make([]string, len(searchKeys))
		for k, key := range searchKeys {
			texts[k] = searchText(fields[key])
		}
		rows = append(rows, row{raw: item, value: value, title: title, texts: texts, parents: rowParents(fields)})
	}
	return rows, nil
}

// stringField reads a string value. A missing key, null, or a value of another type is "".
func stringField(fields map[string]json.RawMessage, key string) string {
	var s string
	if err := json.Unmarshal(fields[key], &s); err != nil {
		return ""
	}
	return s
}

// rowParents reads a row's parents list. A missing or malformed list is none, so the row
// matches no parent.
func rowParents(fields map[string]json.RawMessage) []string {
	var parents []string
	if err := json.Unmarshal(fields[parentsKey], &parents); err != nil {
		return nil
	}
	return parents
}

// searchText is the lowercased text a search matches for one value: a string as it is, a
// number or boolean as written. Anything else (missing, null, an array, an object) is "".
func searchText(value json.RawMessage) string {
	if len(value) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(value, &s); err == nil {
		return strings.ToLower(s)
	}
	var n json.Number
	if err := json.Unmarshal(value, &n); err == nil {
		return n.String()
	}
	var b bool
	if err := json.Unmarshal(value, &b); err == nil {
		return strconv.FormatBool(b)
	}
	return ""
}

// rank scores a lowercased query in four tiers, each over every search key: exact value,
// prefix of a value, a later word of a value, then a substring of a value. Equal scores
// prefer the shorter first-key value, then the other keys' values in key order.
func rank(rows []row, query string) []row {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return rows
	}

	type scored struct {
		r     row
		score int
	}
	matched := make([]scored, 0, len(rows))
	for _, r := range rows {
		score, ok := matchScore(r, q)
		if !ok {
			continue
		}
		matched = append(matched, scored{r: r, score: score})
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].score != matched[j].score {
			return matched[i].score < matched[j].score
		}
		return tieLess(matched[i].r.texts, matched[j].r.texts)
	})

	out := make([]row, len(matched))
	for i, m := range matched {
		out[i] = m.r
	}
	return out
}

// matchTiers are the ways a search text can match a query, best first. A row's score is the
// index of the first tier any of its texts matches.
var matchTiers = []func(text, q string) bool{
	func(text, q string) bool { return text == q },
	strings.HasPrefix,
	wordBoundary,
	strings.Contains,
}

func matchScore(r row, q string) (int, bool) {
	for tier, matches := range matchTiers {
		for _, text := range r.texts {
			if text != "" && matches(text, q) {
				return tier, true
			}
		}
	}
	return 0, false
}

// tieLess orders two equally scored rows by their texts, which always hold one entry per
// search key: the shorter first-key value first, then the other keys' values in key order.
func tieLess(a, b []string) bool {
	if len(a[0]) != len(b[0]) {
		return len(a[0]) < len(b[0])
	}
	return slices.Compare(a[1:], b[1:]) < 0
}

// wordBoundary reports whether q starts a word after the first. The first word
// is a prefix of the whole value, so the prefix tier already covers it.
// Separators are space, slash, and hyphen.
func wordBoundary(text, q string) bool {
	words := strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == '/' || r == '-'
	})
	if len(words) < 2 {
		return false
	}
	for _, word := range words[1:] {
		if strings.HasPrefix(word, q) {
			return true
		}
	}
	return false
}

func pageRows(rows []row, offset, limit int) []row {
	if offset >= len(rows) || limit <= 0 {
		return []row{}
	}
	end := offset + limit
	if end > len(rows) {
		end = len(rows)
	}
	return rows[offset:end]
}
