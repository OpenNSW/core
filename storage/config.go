// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import (
	"fmt"

	"github.com/OpenNSW/core/storage/drivers"
)

// Supported Config.Type values.
const (
	TypeLocal = "local"
	TypeS3    = "s3"
)

// Config selects a storage backend via Type and carries each backend's own
// settings. Only the config for the selected Type is read. Rather than
// flattening every backend's settings into one struct, Config embeds each
// driver's own Config (drivers.LocalConfig, drivers.S3Config) verbatim — so
// each driver keeps ownership of its config shape and validation.
//
// Config and the backend Config types it embeds carry yaml struct tags, so
// it can be embedded in a larger application config struct and populated
// generically (e.g. via yaml.Unmarshal) rather than constructed by hand.
type Config struct {
	// Type is the storage backend: "local" or "s3".
	Type string `yaml:"type"`

	// Local is used when Type == "local".
	Local drivers.LocalConfig `yaml:"local"`
	// S3 is used when Type == "s3".
	S3 drivers.S3Config `yaml:"s3"`

	// PresignTTLSeconds is how long a presigned upload/download URL stays
	// valid, in seconds. Required for both backends. Expressed in seconds
	// (rather than time.Duration) so it decodes cleanly from YAML.
	PresignTTLSeconds int `yaml:"presignTTLSeconds"`
}

// Validate reports misconfiguration before NewStorageFromConfig is called,
// delegating to the selected backend's own Validate.
func (c Config) Validate() error {
	switch c.Type {
	case TypeLocal:
		if err := c.Local.Validate(); err != nil {
			return err
		}
	case TypeS3:
		if err := c.S3.Validate(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("storage: unsupported Type %q (want %q or %q)", c.Type, TypeLocal, TypeS3)
	}

	if c.PresignTTLSeconds <= 0 {
		return fmt.Errorf("storage: PresignTTLSeconds must be greater than zero")
	}

	return nil
}
