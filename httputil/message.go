// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package httputil

import (
	"encoding/json"
	"maps"
	"strings"
)

// Message is a translatable text: a stable key, the English default, and
// named values for the {{name}} placeholders in both.
//
// The placeholder syntax is the default interpolation syntax of i18next and
// similar libraries, so a client can pass Key and Params straight to its
// translator. When encoded as JSON, DefaultValue has its placeholders filled
// in, so a client with no translations can show it as is.
type Message struct {
	Key          string            `json:"key"`
	DefaultValue string            `json:"defaultValue"`
	Params       map[string]string `json:"params,omitempty"`
}

// WithParams returns a copy of m with Params set to the entries of params
// whose names appear as placeholders in DefaultValue. Entries the text does
// not use are dropped, so one set of params can fill several messages.
func (m Message) WithParams(params map[string]string) Message {
	var used map[string]string
	for name, value := range params {
		if strings.Contains(m.DefaultValue, "{{"+name+"}}") {
			if used == nil {
				used = make(map[string]string, len(params))
			}
			used[name] = value
		}
	}
	m.Params = used
	return m
}

// clone returns m with its own copy of Params, so a caller can't change the
// map held by an Error or a declaration.
func (m Message) clone() Message {
	m.Params = maps.Clone(m.Params)
	return m
}

// String returns DefaultValue with its placeholders filled in from Params.
func (m Message) String() string {
	return interpolate(m.DefaultValue, m.Params)
}

// MarshalJSON encodes m with DefaultValue interpolated and Params kept, so a
// client can either show the English text or translate Key with Params.
func (m Message) MarshalJSON() ([]byte, error) {
	type plain Message
	return json.Marshal(plain{Key: m.Key, DefaultValue: m.String(), Params: maps.Clone(m.Params)})
}

// interpolate replaces each {{name}} in s with params[name] in a single pass,
// so a value that itself contains a placeholder is not expanded again.
// Placeholders with no matching param are left as they are.
func interpolate(s string, params map[string]string) string {
	if len(params) == 0 {
		return s
	}
	var b strings.Builder
	for {
		start := strings.Index(s, "{{")
		if start < 0 {
			break
		}
		end := strings.Index(s[start+2:], "}}")
		if end < 0 {
			break
		}
		end += start + 2
		b.WriteString(s[:start])
		if value, ok := params[s[start+2:end]]; ok {
			b.WriteString(value)
		} else {
			b.WriteString(s[start : end+2])
		}
		s = s[end+2:]
	}
	b.WriteString(s)
	return b.String()
}
