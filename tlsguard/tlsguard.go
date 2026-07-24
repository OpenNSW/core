// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package tlsguard gates the "skip TLS certificate verification" escape hatch
// behind an explicit development-environment signal, so an insecure-TLS flag
// left enabled can never silently ship to production.
package tlsguard

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// EnvKey is the environment variable consulted to decide whether the process is
// running in development.
const EnvKey = "APP_ENV"

// IsDevEnvironment reports whether the process is explicitly running in the
// development environment (APP_ENV=development, case-insensitive). Anything else
// — including an unset value — is treated as non-development so production fails
// closed. This is the single signal that permits TLS verification to be skipped.
func IsDevEnvironment() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(EnvKey)), "development")
}

// Guard decides whether an InsecureSkipTLSVerify request may proceed. It returns
// nil (with a prominent warning) only in the development environment; otherwise
// it returns an error so startup aborts before an insecure TLS client is ever
// built — preventing a forged-certificate authentication bypass from silently
// shipping to production. Callers invoke it only when the corresponding insecure
// flag is set. purpose names the flag/path for the log and error message.
func Guard(purpose string) error {
	if IsDevEnvironment() {
		slog.Warn("TLS certificate verification DISABLED (APP_ENV=development); never use in production", "purpose", purpose)
		return nil
	}
	return fmt.Errorf("%s: insecure TLS verification requested but APP_ENV is not \"development\" (unset or any other value is treated as production); refusing to start — provide a trusted certificate chain, or set APP_ENV=development for a non-production run", purpose)
}
