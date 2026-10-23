// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package temporal

import (
	"encoding/base64"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestConfigValidateDefaultsOK(t *testing.T) {
	cfg := Config{Host: "localhost", Port: 7233, Namespace: "default"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateMissingHost(t *testing.T) {
	cfg := Config{Host: "", Port: 7233, Namespace: "default"}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error")
	}
}

func TestConfigValidateInvalidPort(t *testing.T) {
	cfg := Config{Host: "localhost", Port: 0, Namespace: "default"}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error")
	}
}

func TestConfigValidatePortTooHigh(t *testing.T) {
	cfg := Config{Host: "localhost", Port: 65536, Namespace: "default"}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error")
	}
}

func TestConfigValidateMissingNamespace(t *testing.T) {
	cfg := Config{Host: "localhost", Port: 7233, Namespace: ""}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error")
	}
}

func TestConfigValidateTLS(t *testing.T) {
	tests := []struct {
		name    string
		tls     *TLSConfig
		wantErr bool
	}{
		{"plaintext", nil, false},
		{"TLS with system roots", &TLSConfig{}, false},
		{"TLS with a CA and server name", &TLSConfig{CAFile: "ca.crt", ServerName: "temporal.internal"}, false},
		{"mutual TLS", &TLSConfig{CAFile: "ca.crt", CertFile: "tls.crt", KeyFile: "tls.key"}, false},
		{"certFile without keyFile", &TLSConfig{CertFile: "tls.crt"}, true},
		{"keyFile without certFile", &TLSConfig{KeyFile: "tls.key"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{Host: "localhost", Port: 7233, Namespace: "default", TLS: tt.tls}
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfigTLSFromYAML(t *testing.T) {
	const doc = `
host: temporal.example.svc
port: 7233
namespace: default
tls:
  caFile: /etc/temporal-client/ca.crt
  certFile: /etc/temporal-client/tls.crt
  keyFile: /etc/temporal-client/tls.key
  serverName: temporal.example.svc
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(doc), &cfg); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	want := TLSConfig{
		CAFile:     "/etc/temporal-client/ca.crt",
		CertFile:   "/etc/temporal-client/tls.crt",
		KeyFile:    "/etc/temporal-client/tls.key",
		ServerName: "temporal.example.svc",
	}
	if cfg.TLS == nil || *cfg.TLS != want {
		t.Fatalf("TLS = %+v, want %+v", cfg.TLS, want)
	}

	var plain Config
	if err := yaml.Unmarshal([]byte("host: localhost\nport: 7233\nnamespace: default\n"), &plain); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	if plain.TLS != nil {
		t.Fatalf("TLS = %+v without a tls block, want nil", plain.TLS)
	}
}

func TestConfigValidateEncryption(t *testing.T) {
	key32 := base64.StdEncoding.EncodeToString(make([]byte, 32))
	tests := []struct {
		name    string
		enc     *EncryptionConfig
		wantErr string
	}{
		{"no encryption", nil, ""},
		{"a 32-byte key", &EncryptionConfig{KeyID: "k1", Key: key32}, ""},
		{"missing keyId", &EncryptionConfig{Key: key32}, "keyId is required"},
		{"key not base64", &EncryptionConfig{KeyID: "k1", Key: "not base64!"}, "not valid base64"},
		{"16-byte key", &EncryptionConfig{KeyID: "k1", Key: base64.StdEncoding.EncodeToString(make([]byte, 16))}, "must be 32 bytes, got 16"},
		{"empty key", &EncryptionConfig{KeyID: "k1"}, "must be 32 bytes, got 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{Host: "localhost", Port: 7233, Namespace: "default", Encryption: tt.enc}
			err := cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestConfigEncryptionFromYAML(t *testing.T) {
	const doc = `
host: localhost
port: 7233
namespace: default
encryption:
  keyId: k1
  key: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(doc), &cfg); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	want := EncryptionConfig{KeyID: "k1", Key: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}
	if cfg.Encryption == nil || *cfg.Encryption != want {
		t.Fatalf("Encryption = %+v, want %+v", cfg.Encryption, want)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
