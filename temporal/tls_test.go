// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package temporal

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientTLSConfigSystemRoots(t *testing.T) {
	cfg, err := clientTLSConfig(TLSConfig{})
	if err != nil {
		t.Fatalf("clientTLSConfig() error = %v", err)
	}
	if cfg.RootCAs != nil {
		t.Fatalf("RootCAs set, want nil (system roots)")
	}
	if len(cfg.Certificates) != 0 {
		t.Fatalf("Certificates set without a client certificate")
	}
}

func TestClientTLSConfigCAAndServerName(t *testing.T) {
	ca := newTestCA(t)
	cfg, err := clientTLSConfig(TLSConfig{CAFile: ca.file, ServerName: "temporal.internal"})
	if err != nil {
		t.Fatalf("clientTLSConfig() error = %v", err)
	}
	if cfg.RootCAs == nil {
		t.Fatalf("RootCAs not set from caFile")
	}
	if cfg.ServerName != "temporal.internal" {
		t.Fatalf("ServerName = %q, want %q", cfg.ServerName, "temporal.internal")
	}
}

func TestClientTLSConfigErrors(t *testing.T) {
	ca := newTestCA(t)
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	ca.writeClientCertificate(certFile, keyFile)
	otherCert, otherKey := filepath.Join(dir, "other.crt"), filepath.Join(dir, "other.key")
	ca.writeClientCertificate(otherCert, otherKey)
	notPEM := filepath.Join(dir, "not-pem.crt")
	if err := os.WriteFile(notPEM, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		cfg     TLSConfig
		wantErr string
	}{
		{"missing caFile", TLSConfig{CAFile: filepath.Join(dir, "missing.crt")}, "read caFile"},
		{"caFile without certificates", TLSConfig{CAFile: notPEM}, "contains no PEM certificates"},
		{"missing keyFile", TLSConfig{CertFile: certFile, KeyFile: filepath.Join(dir, "missing.key")}, "load client certificate"},
		{"key from another pair", TLSConfig{CertFile: certFile, KeyFile: otherKey}, "load client certificate"},
		{"certFile without keyFile", TLSConfig{CertFile: certFile}, "must be set together"},
		{"keyFile without certFile", TLSConfig{KeyFile: keyFile}, "must be set together"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := clientTLSConfig(tt.cfg)
			if err == nil {
				t.Fatalf("clientTLSConfig() error = nil, want one containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("clientTLSConfig() error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestClientTLSConfigClientCertificate(t *testing.T) {
	ca := newTestCA(t)
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	want := ca.writeClientCertificate(certFile, keyFile)

	cfg, err := clientTLSConfig(TLSConfig{CAFile: ca.file, CertFile: certFile, KeyFile: keyFile})
	if err != nil {
		t.Fatalf("clientTLSConfig() error = %v", err)
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("Certificates has %d entries, want 1", len(cfg.Certificates))
	}
	if !bytes.Equal(cfg.Certificates[0].Certificate[0], want) {
		t.Fatalf("Certificates holds a different certificate than certFile")
	}
}
