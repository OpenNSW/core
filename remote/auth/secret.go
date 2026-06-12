// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Secret represents a configuration value that can be either a literal string,
// or a source-referenced secret (e.g. from an env var or a file).
type Secret struct {
	source string // "env", "file", or "literal"
	value  string
}

// NewSecret constructs a Secret from a raw string reference.
func NewSecret(raw string) Secret {
	if strings.HasPrefix(raw, "env:") {
		return Secret{source: "env", value: strings.TrimPrefix(raw, "env:")}
	}
	if strings.HasPrefix(raw, "file:") {
		return Secret{source: "file", value: strings.TrimPrefix(raw, "file:")}
	}
	return Secret{source: "literal", value: raw}
}

// UnmarshalJSON implements json.Unmarshaler to support parsing both plain/prefixed strings
// and structured objects.
func (s *Secret) UnmarshalJSON(b []byte) error {
	// Try unmarshaling as a plain JSON string
	var str string
	if err := json.Unmarshal(b, &str); err == nil {
		*s = NewSecret(str)
		return nil
	}

	// Try unmarshaling as an object (e.g. {"env": "VAR_NAME"} or {"file": "/path"})
	var obj map[string]string
	if err := json.Unmarshal(b, &obj); err == nil {
		if envVal, ok := obj["env"]; ok {
			s.source = "env"
			s.value = envVal
			return nil
		}
		if fileVal, ok := obj["file"]; ok {
			s.source = "file"
			s.value = fileVal
			return nil
		}
		if litVal, ok := obj["literal"]; ok {
			s.source = "literal"
			s.value = litVal
			return nil
		}
		return fmt.Errorf("invalid secret object: must contain 'env', 'file', or 'literal'")
	}

	return fmt.Errorf("invalid secret value: expected string or object")
}

// MarshalJSON implements json.Marshaler.
func (s Secret) MarshalJSON() ([]byte, error) {
	if s.source == "literal" {
		return json.Marshal(s.value)
	}
	return json.Marshal(map[string]string{
		s.source: s.value,
	})
}

// maxSecretFileSize is the maximum allowed size for a secret file.
// Secrets (tokens, API keys) should be small; anything larger is likely
// a misconfiguration or an adversarial path (e.g. /dev/urandom).
const maxSecretFileSize = 4096 // 4 KB

// Resolve resolves the actual secret value. It reads the file or environment variable
// dynamically on every request.
func (s Secret) Resolve(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	switch s.source {
	case "env":
		val := os.Getenv(s.value)
		if val == "" {
			return "", fmt.Errorf("environment variable %s is not set or empty", s.value)
		}
		return val, nil
	case "file":
		val, err := readSecretFile(s.value)
		if err != nil {
			return "", fmt.Errorf("failed to read secret file %s: %w", s.value, err)
		}
		return val, nil
	default:
		return s.value, nil
	}
}

// Validate checks that the reference exists and is readable. Run on startup to fail loud.
func (s Secret) Validate() error {
	switch s.source {
	case "env":
		val := os.Getenv(s.value)
		if val == "" {
			return fmt.Errorf("environment variable %s is not set or empty", s.value)
		}
	case "file":
		if _, err := readSecretFile(s.value); err != nil {
			return fmt.Errorf("failed to read secret file %s: %w", s.value, err)
		}
	default:
		if s.value == "" {
			return fmt.Errorf("literal secret value is empty")
		}
	}
	return nil
}

// readSecretFile reads and validates a secret file. It ensures the path refers
// to a regular file (rejecting named pipes, directories, device nodes like
// /dev/urandom) and that it does not exceed maxSecretFileSize bytes.
func readSecretFile(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file")
	}
	if fi.Size() > maxSecretFileSize {
		return "", fmt.Errorf("file size %d exceeds maximum allowed size of %d bytes", fi.Size(), maxSecretFileSize)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	val := strings.TrimSpace(string(data))
	if val == "" {
		return "", fmt.Errorf("secret file is empty")
	}
	return val, nil
}

// IsZero returns true if the secret is unconfigured/empty.
func (s Secret) IsZero() bool {
	return s.source == "" && s.value == ""
}
