// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package temporal

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// clientTLSConfig builds the TLS configuration for the connection to the
// Temporal server. It reads the CA file and the client key pair once, so a
// missing or malformed file fails at startup, and a replaced file is read at
// the next start.
func clientTLSConfig(c TLSConfig) (*tls.Config, error) {
	// Config.Validate runs the same check. It is repeated here because NewClient
	// can be given a Config that was never validated, and a keyFile without a
	// certFile would otherwise be ignored.
	if err := c.Validate(); err != nil {
		return nil, err
	}

	cfg := &tls.Config{ServerName: c.ServerName}

	if c.CAFile != "" {
		pem, err := os.ReadFile(c.CAFile)
		if err != nil {
			return nil, fmt.Errorf("temporal tls: read caFile: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("temporal tls: caFile %s contains no PEM certificates", c.CAFile)
		}
		cfg.RootCAs = pool
	}

	if c.CertFile != "" {
		cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("temporal tls: load client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}

	return cfg, nil
}
