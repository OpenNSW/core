package payloadschema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
)

// The messages of read-only violations. Like the schema ones, they name the
// rule, never a value.
const (
	msgReadOnlyChanged = "read-only value changed"
	msgReadOnlyMissing = "read-only value missing"
	msgReadOnlySet     = "read-only value set by the client"
	msgRowUnknown      = "row's read-only values don't match a row the server sent"
	msgRowAdded        = "rows can't be added to this list"
	msgRowsRemoved     = "rows the server sent can't be removed from this list"
)

// value is a value of the stored data or the submission, and whether it is
// there at all: a missing key differs from one holding null.
type value struct {
	v  any
	ok bool
}

// readOnlyViolations compares submitted, a submission's form data, with
// stored, the data the step holds, by the read-only rules: every
// read-only value comes back as the server stored it, and a list's rows come
// back as rows the server sent, or as new rows without read-only values,
// within what the list allows. A form without read-only values has none.
func (f *readOnlyForm) readOnlyViolations(stored value, submitted map[string]any) []Violation {
	if f.root == nil {
		return nil
	}
	var out []Violation
	f.root.compare(stored, value{submitted, true}, "", &out)
	return out
}

// compare appends to out the violations of sub, at path at, against st.
func (n *roNode) compare(st, sub value, at string, out *[]Violation) {
	if n.readOnly() {
		if msg := n.compareReadOnly(st, sub); msg != "" {
			*out = append(*out, Violation{Path: at, Message: msg})
		}
		return
	}
	if row := n.row(); row != nil {
		stRows, stIsList := st.v.([]any)
		subRows, subIsList := sub.v.([]any)
		if stIsList || subIsList {
			n.compareRows(row, stRows, subRows, at, out)
			return
		}
	}
	// A value that isn't an object has no keys: the read-only values under it
	// count as absent, so they are reported as missing or set by the client,
	// never passed over.
	stObj, _ := st.v.(map[string]any)
	subObj, _ := sub.v.(map[string]any)
	for _, key := range keysOf(stObj, subObj) {
		child := n.property(key)
		if child == nil {
			continue
		}
		stChild, stOK := stObj[key]
		subChild, subOK := subObj[key]
		child.compare(value{stChild, stOK}, value{subChild, subOK}, at+"/"+escapeToken(key), out)
	}
}

// compareReadOnly applies the rule for a read-only value: one the server
// stored comes back unchanged; one it didn't is left out, or sent as its
// schema default, which the browser fills in by itself. It returns the
// violated rule, or "".
func (n *roNode) compareReadOnly(st, sub value) string {
	switch {
	case st.ok && !sub.ok:
		return msgReadOnlyMissing
	case st.ok && !equalJSON(st.v, sub.v):
		return msgReadOnlyChanged
	case !st.ok && sub.ok:
		if def, ok := n.defaultValue(); ok && equalJSON(def, sub.v) {
			return ""
		}
		return msgReadOnlySet
	}
	return ""
}

// compareRows pairs the submitted rows of the list n with the stored ones and
// applies the list's switches. Positions don't matter: a submitted row pairs
// with a stored row, not already paired, whose server part is the same (see
// serverPart). A row that pairs with none is a new row if its server part is
// that of a row without read-only values, and otherwise one the server never
// sent. Paired rows are then compared in full, so a read-only value missing
// from one is still reported, and lists inside them follow the same rule; a
// new row is compared against nothing.
func (n *roNode) compareRows(row *roNode, stored, submitted []any, at string, out *[]Violation) {
	sw := n.switches()
	none := partKey(row.serverPart(value{}))
	stParts := make([]string, len(stored))
	for j, st := range stored {
		stParts[j] = partKey(row.serverPart(value{st, true}))
	}
	paired := make([]bool, len(stored))

	for i, sub := range submitted {
		rowAt := fmt.Sprintf("%s/%d", at, i)
		part := partKey(row.serverPart(value{sub, true}))
		j := -1
		for k, p := range stParts {
			if !paired[k] && p == part {
				j = k
				break
			}
		}
		switch {
		case j >= 0:
			paired[j] = true
			row.compare(value{stored[j], true}, value{sub, true}, rowAt, out)
		case part == none:
			if !sw.addable {
				*out = append(*out, Violation{Path: rowAt, Message: msgRowAdded})
				continue
			}
			row.compare(value{}, value{sub, true}, rowAt, out)
		default:
			*out = append(*out, Violation{Path: rowAt, Message: msgRowUnknown})
		}
	}

	if !sw.removable && slices.Contains(paired, false) {
		*out = append(*out, Violation{Path: at, Message: msgRowsRemoved})
	}
}

// serverPart returns the read-only values in v, lists inside left aside, as
// one value to pair rows by. A read-only value v lacks is taken as its schema
// default, when it has one, on both sides: a row the server stored without a
// value pairs with the same row sent back with that value's default, as the
// browser fills defaults in. It is nil when v holds none and none has a
// default.
func (n *roNode) serverPart(v value) any {
	if n.readOnly() {
		if v.ok {
			return map[string]any{"": v.v}
		}
		if def, ok := n.defaultValue(); ok {
			return map[string]any{"": def}
		}
		return nil
	}
	if n.row() != nil {
		if _, isList := v.v.([]any); isList || !v.ok {
			return nil
		}
	}
	obj, _ := v.v.(map[string]any) // not an object: no read-only values of its own
	part := make(map[string]any)
	for _, key := range keysOf(obj, n.namedKeys()) {
		child := n.property(key)
		if child == nil {
			continue
		}
		childValue, ok := obj[key]
		if p := child.serverPart(value{childValue, ok}); p != nil {
			part[key] = p
		}
	}
	if len(part) == 0 {
		return nil
	}
	return part
}

// partKey returns a server part as a string two parts compare equal by. It
// can't fail for values decoded from JSON; if it did, the rows would pair on
// "" and the value-by-value comparison of paired rows would still refuse them.
func partKey(part any) string {
	b, err := json.Marshal(part)
	if err != nil {
		return ""
	}
	return string(b)
}

// keysOf returns the keys of the objects, each once, sorted so violations come
// in a stable order.
func keysOf(objs ...map[string]any) []string {
	seen := make(map[string]bool)
	var keys []string
	for _, obj := range objs {
		for k := range obj {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	sort.Strings(keys)
	return keys
}

// equalJSON reports whether a and b are the same JSON value: 1 and "1" differ,
// 1 and 1.0 don't, and objects and lists compare in full. A value that can't
// be encoded equals nothing, so the comparison fails closed.
func equalJSON(a, b any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}
