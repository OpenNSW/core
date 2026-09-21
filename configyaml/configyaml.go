// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package configyaml loads a YAML config file into a Go struct, resolving
// any "{{env:NAME}}" (read from an env var) or "{{file:/path}}" (read from a
// mounted file) placeholder found in it via github.com/OpenNSW/core/secret.
//
// Resolution runs generically over the parsed YAML tree before it is decoded
// into any typed struct, so the config types passed to LoadAndExpand never
// need to know which of their own fields might be sourced from an env var or
// a file — that's a property of how the YAML file was authored, not of the
// Go type reading it. This pairs with the `yaml` struct tags already carried
// by config types such as database.Config, temporal.Config, and cors.Config:
// embed those (or your own) in an application config struct and load it with
// LoadAndExpand.
package configyaml

import (
	"fmt"
	"os"
	"regexp"

	"github.com/OpenNSW/core/secret"
	"gopkg.in/yaml.v3"
)

// placeholderPattern matches a scalar value that is *entirely* a
// "{{scheme:ref}}" placeholder, e.g. "{{env:DB_PASSWORD}}" or
// "{{file:/run/secrets/token}}". A value that merely contains "{{...}}"
// alongside other text is left untouched — the whole scalar must be the
// placeholder.
var placeholderPattern = regexp.MustCompile(`^\{\{\s*(.+?)\s*\}\}$`)

// LoadAndExpand reads the YAML file at path, resolves every "{{env:NAME}}" /
// "{{file:/path}}" placeholder in it, and decodes the result into v (a
// pointer, as for yaml.Unmarshal).
func LoadAndExpand(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading config file %s: %w", path, err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parsing config file %s: %w", path, err)
	}
	if err := expandPlaceholders(&root, "", make(map[*yaml.Node]bool)); err != nil {
		return fmt.Errorf("resolving config file %s: %w", path, err)
	}

	if err := root.Decode(v); err != nil {
		return fmt.Errorf("parsing config file %s: %w", path, err)
	}
	return nil
}

// expandPlaceholders walks the parsed (but not yet decoded) YAML tree and
// resolves every scalar value shaped like "{{env:NAME}}" or "{{file:/path}}"
// through secret.SecretRef, in place. path is the dotted/indexed YAML path
// to node, used only for error messages. seen tracks the anchor targets
// currently being expanded via an AliasNode on the active call stack, so a
// self-referential anchor (e.g. "&node {self: *node}") is reported as an
// error instead of recursing forever.
func expandPlaceholders(node *yaml.Node, path string, seen map[*yaml.Node]bool) error {
	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			if err := expandPlaceholders(child, path, seen); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		// Content alternates key0, value0, key1, value1, ...
		for i := 0; i+1 < len(node.Content); i += 2 {
			childPath := node.Content[i].Value
			if path != "" {
				childPath = path + "." + childPath
			}
			if err := expandPlaceholders(node.Content[i+1], childPath, seen); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for i, child := range node.Content {
			if err := expandPlaceholders(child, fmt.Sprintf("%s[%d]", path, i), seen); err != nil {
				return err
			}
		}
	case yaml.AliasNode:
		// node.Alias points at the anchor's *yaml.Node, which the normal
		// document-order walk above will also reach (and mutate in place) when
		// it visits the anchor definition itself. Recursing here too makes
		// resolution independent of whether the anchor or the alias comes
		// first in the document; it's a no-op on a node already resolved,
		// since a resolved scalar's Tag is no longer "!!str".
		if seen[node.Alias] {
			return fmt.Errorf("%s: cyclic YAML alias (anchor refers to itself)", path)
		}
		seen[node.Alias] = true
		err := expandPlaceholders(node.Alias, path, seen)
		delete(seen, node.Alias) // pop: a DAG reusing the same anchor from a sibling branch is not a cycle
		return err
	case yaml.ScalarNode:
		if node.Tag != "!!str" {
			return nil
		}
		m := placeholderPattern.FindStringSubmatch(node.Value)
		if m == nil {
			return nil
		}
		resolved, err := secret.SecretRef(m[1]).Resolve()
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		node.Value = resolved
		// Clear the explicit !!str tag/style so the final Decode re-infers
		// the real type (int, bool, ...) from the resolved value, in case a
		// non-string field (e.g. a port number) was templated too.
		node.Tag = ""
		node.Style = 0
	}
	return nil
}
