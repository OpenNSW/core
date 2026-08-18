// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package refid

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration for the ID generation system.
// It is typically loaded from a YAML file via LoadConfig, but can also be
// constructed programmatically (e.g. in tests or for embedded defaults).
type Config struct {
	// Issuers is the ordered list of issuer definitions. Each issuer owns one
	// or more ID type formats.
	Issuers []IssuerConfig `yaml:"issuers"`

	// Lists is a map of named value sets. Segments of type "list" reference
	// these by name, and the value supplied by the caller at generation time is
	// validated against the corresponding slice.
	Lists map[string][]string `yaml:"lists"`
}

// IssuerConfig groups all ID format definitions for a single issuer.
type IssuerConfig struct {
	// Issuer is the unique identifier for this issuing authority, e.g. "RTA".
	// It is also available as the {issuer} placeholder in scope key templates.
	Issuer string `yaml:"issuer"`

	// Formats is the list of ID type formats owned by this issuer.
	Formats []FormatConfig `yaml:"formats"`
}

// FormatConfig describes how a single ID type is assembled from an ordered
// list of segments.
type FormatConfig struct {
	// IDType uniquely identifies this format within the issuer, e.g.
	// "application_id" or "permit_id". It is also available as the {idType}
	// placeholder in scope key templates.
	IDType string `yaml:"idType"`

	// Segments is the ordered list of typed segments whose rendered outputs are
	// concatenated to form the final ID string.
	Segments []SegmentConfig `yaml:"segments"`
}

// SegmentConfig is the raw configuration for a single segment. Fields are
// interpreted according to Type; unused fields are ignored.
type SegmentConfig struct {
	// Type is one of: "literal", "list", "date", "sequence".
	Type string `yaml:"type"`

	// Value is the fixed text for a literal segment.
	Value string `yaml:"value,omitempty"`

	// List is the name of a list defined in Config.Lists, used by list segments.
	List string `yaml:"list,omitempty"`

	// Param is the key the caller must supply in their params map, used by list
	// segments to look up the caller-provided value.
	Param string `yaml:"param,omitempty"`

	// Layout is a Go reference-date format string (e.g. "20060102"), used by
	// date segments.
	Layout string `yaml:"layout,omitempty"`

	// ScopeKey is a template string for sequence segments. Placeholders are
	// resolved at generation time. See the Registry documentation for the full
	// placeholder reference.
	ScopeKey string `yaml:"scopeKey,omitempty"`

	// Padding is the minimum number of digits for a sequence segment's counter.
	// The counter is zero-padded to this width. If the counter exceeds the
	// maximum value representable with Padding digits, ErrCounterOverflow is
	// returned. Defaults to 1 when not set.
	Padding int `yaml:"padding,omitempty"`
}

// LoadConfig reads and parses a YAML configuration file at the given path.
// It performs no semantic validation; call NewRegistry to validate the config
// against the full constraint set.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("refid: failed to read config file %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("refid: failed to parse config file %q: %w", path, err)
	}

	return cfg, nil
}
