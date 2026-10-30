// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package temporal

import (
	"encoding/base64"
	"fmt"

	"github.com/OpenNSW/core/shared/validation"
)

// Config holds configuration required to connect to Temporal.
//
// This is owned by the temporal package (similar to other internal packages),
// so the package controls the shape/semantics of its configuration.
//
// Host/Port are kept separate to make configuration via environment variables
// easier and more explicit.
type Config struct {
	Host      string `yaml:"host"`
	Port      int    `yaml:"port"`
	Namespace string `yaml:"namespace"`

	// TLS, when set, connects to the Temporal server over TLS. When it is nil
	// the connection is plaintext.
	TLS *TLSConfig `yaml:"tls"`

	// Encryption, when set, encrypts every payload the client and its workers
	// send to the Temporal server, and refuses any payload not encrypted with its
	// key. When it is nil, payloads are stored as plain JSON.
	Encryption *EncryptionConfig `yaml:"encryption"`
}

// TLSConfig configures TLS to the Temporal server, and mutual TLS when the
// server requires client certificates. All files are PEM-encoded.
type TLSConfig struct {
	// CAFile holds the CA certificates that sign the Temporal server's
	// certificate. When empty, the system roots are used.
	CAFile string `yaml:"caFile"`

	// CertFile and KeyFile are the client certificate and key presented to a
	// Temporal server that requires client authentication. Set both or neither.
	CertFile string `yaml:"certFile"`
	KeyFile  string `yaml:"keyFile"`

	// ServerName is the name the Temporal server's certificate must carry. When
	// empty, gRPC uses the host it dials, which is Host.
	ServerName string `yaml:"serverName"`
}

// Validate ensures the Temporal configuration is usable.
func (c Config) Validate() error {
	if c.Host == "" {
		return fmt.Errorf("temporal host is required")
	}
	if err := validation.TCPPort("temporal port", c.Port); err != nil {
		return err
	}
	if c.Namespace == "" {
		return fmt.Errorf("temporal namespace is required")
	}
	if c.TLS != nil {
		if err := c.TLS.Validate(); err != nil {
			return err
		}
	}
	if c.Encryption != nil {
		if err := c.Encryption.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Validate checks that the client certificate and key are configured together.
// It does not read the files: NewClient does, and fails if one is missing or
// malformed.
func (c TLSConfig) Validate() error {
	if (c.CertFile == "") != (c.KeyFile == "") {
		return fmt.Errorf("temporal tls: certFile and keyFile must be set together")
	}
	return nil
}

// EncryptionConfig configures AES-256-GCM encryption of Temporal payloads.
type EncryptionConfig struct {
	// KeyID names the key. It is stored with each payload and authenticated
	// with it, so a payload encrypted under another key is refused.
	KeyID string `yaml:"keyId"`

	// Key is the base64-encoded 32-byte AES-256 key.
	Key string `yaml:"key"`
}

// Validate checks that the key ID is set and the key is 32 bytes of base64.
func (c EncryptionConfig) Validate() error {
	_, err := c.decodeKey()
	return err
}

func (c EncryptionConfig) decodeKey() ([]byte, error) {
	if c.KeyID == "" {
		return nil, fmt.Errorf("temporal encryption: keyId is required")
	}
	key, err := base64.StdEncoding.DecodeString(c.Key)
	if err != nil {
		return nil, fmt.Errorf("temporal encryption: key is not valid base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("temporal encryption: key must be 32 bytes, got %d", len(key))
	}
	return key, nil
}
