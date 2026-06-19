// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package auth

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// SecretRef is a secret-bearing configuration value: a literal, or a reference
// whose scheme prefix names where the value comes from. It is the raw string as
// written in config — it unmarshals from and marshals to a plain JSON string, so
// only the single prefixed-string form is supported; there is intentionally no
// object form.
//
//	"plain-value"        // literal (the default, backward compatible)
//	"file:/path/to/file" // read from a file (trailing whitespace trimmed)
//	"literal:file:foo"   // explicit literal escape hatch
//
// A value whose prefix is not a known scheme (including one with no colon at all)
// is treated as a literal. Resolution — the I/O — is a separate step; see Resolve.
type SecretRef string

// secretSchemes maps a reference scheme prefix to the function that resolves it.
// Each source is an independent entry, so a new source (e.g. "vault") can
// be added here without touching parsing, the authenticators, or the manager.
var secretSchemes = map[string]func(ref string) (string, error){
	"file":    resolveFile,
	"literal": func(ref string) (string, error) { return ref, nil },
}

// Resolve reads the concrete value for the reference. This is the single I/O seam:
// the only place that reads files and the only place that can fail. An unreadable
// or empty file is a loud error — a reference never silently resolves to the empty
// string. A literal is returned as-is; an empty literal (or the zero value) is
// allowed for backward compatibility.
func (s SecretRef) Resolve() (string, error) {
	if scheme, ref, found := strings.Cut(string(s), ":"); found {
		if resolve, ok := secretSchemes[scheme]; ok {
			return resolve(ref)
		}
	}
	return string(s), nil
}

func resolveFile(path string) (string, error) {
	val, err := readSecretFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read secret file %q: %w", path, err)
	}
	return val, nil
}

// maxSecretFileSize bounds the size of a secret file. Secrets (tokens, API keys)
// are small; anything larger is likely a misconfiguration or an adversarial path
// (e.g. a device node).
const maxSecretFileSize = 4096 // 4 KB

// readSecretFile reads and validates a secret file: it must be a regular file
// (rejecting directories, named pipes, device nodes) no larger than
// maxSecretFileSize, and its trimmed contents must be non-empty.
//
// The file is opened first and every check and the read operate on that single
// descriptor, so a concurrent swap of the path (e.g. via a symlink) cannot slip a
// different file in between the check and the read (TOCTOU). The read is also
// capped independently of the stat'd size, in case the file grows afterwards.
func readSecretFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file")
	}
	if fi.Size() > maxSecretFileSize {
		return "", fmt.Errorf("file size %d exceeds maximum of %d bytes", fi.Size(), maxSecretFileSize)
	}

	data, err := io.ReadAll(io.LimitReader(f, maxSecretFileSize+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxSecretFileSize {
		return "", fmt.Errorf("file exceeds maximum of %d bytes", maxSecretFileSize)
	}
	val := strings.TrimSpace(string(data))
	if val == "" {
		return "", fmt.Errorf("file is empty")
	}
	return val, nil
}
