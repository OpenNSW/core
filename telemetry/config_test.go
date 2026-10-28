// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package telemetry

import (
	"strings"
	"testing"

	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{name: "disabled", cfg: Config{}},
		{
			name: "collector base",
			cfg:  Config{Endpoint: "http://otel-collector:4318", ServiceName: "svc"},
		},
		{
			name: "traces endpoint only",
			cfg:  Config{Traces: SignalConfig{Endpoint: "http://data-prepper:21890/v1/traces"}, ServiceName: "svc"},
		},
		{
			name:    "endpoint not a url",
			cfg:     Config{Endpoint: "otel-collector:4318", ServiceName: "svc"},
			wantErr: "Endpoint must be a valid absolute http(s) URL",
		},
		{
			name:    "signal endpoint not a url",
			cfg:     Config{Endpoint: "http://otel-collector:4318", Logs: SignalConfig{Endpoint: "ftp://logs"}, ServiceName: "svc"},
			wantErr: "Logs.Endpoint must be a valid absolute http(s) URL",
		},
		{
			name:    "missing service name",
			cfg:     Config{Endpoint: "http://otel-collector:4318"},
			wantErr: "ServiceName is required",
		},
		{
			name:    "missing service name with signal endpoint",
			cfg:     Config{Metrics: SignalConfig{Endpoint: "http://otel-collector:4318/v1/metrics"}},
			wantErr: "ServiceName is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestConfigIgnoresOwnedEnv(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://otel-collector:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://otel-collector:4318/v1/traces")
	t.Setenv("OTEL_SERVICE_NAME", "from-env")

	if (Config{}).Enabled() {
		t.Fatal("Enabled() = true from OTEL_* env, want false")
	}
	err := Config{Endpoint: "http://otel-collector:4318"}.Validate()
	if err == nil || !strings.Contains(err.Error(), "ServiceName is required") {
		t.Fatalf("Validate() = %v, want ServiceName required despite OTEL_SERVICE_NAME", err)
	}
}

func TestConfigEndpoints(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want endpoints
	}{
		{name: "none", cfg: Config{}},
		{
			name: "base",
			cfg:  Config{Endpoint: "http://otel-collector:4318/"},
			want: endpoints{
				traces:  "http://otel-collector:4318/v1/traces",
				metrics: "http://otel-collector:4318/v1/metrics",
				logs:    "http://otel-collector:4318/v1/logs",
			},
		},
		{
			name: "base with path",
			cfg:  Config{Endpoint: "http://openobserve:5080/api/default"},
			want: endpoints{
				traces:  "http://openobserve:5080/api/default/v1/traces",
				metrics: "http://openobserve:5080/api/default/v1/metrics",
				logs:    "http://openobserve:5080/api/default/v1/logs",
			},
		},
		{
			name: "base given as a signal URL",
			cfg:  Config{Endpoint: "https://otel.example/base/v1/traces/?x=1"},
			want: endpoints{
				traces:  "https://otel.example/base/v1/traces",
				metrics: "https://otel.example/base/v1/metrics",
				logs:    "https://otel.example/base/v1/logs",
			},
		},
		{
			name: "signal endpoint overrides base",
			cfg:  Config{Endpoint: "http://otel-collector:4318", Traces: SignalConfig{Endpoint: "http://tempo:4318/custom"}},
			want: endpoints{
				traces:  "http://tempo:4318/custom",
				metrics: "http://otel-collector:4318/v1/metrics",
				logs:    "http://otel-collector:4318/v1/logs",
			},
		},
		{
			name: "signal endpoints only",
			cfg:  Config{Traces: SignalConfig{Endpoint: "http://data-prepper:21890/v1/traces"}, Logs: SignalConfig{Endpoint: "http://data-prepper:21892/v1/logs"}},
			want: endpoints{
				traces: "http://data-prepper:21890/v1/traces",
				logs:   "http://data-prepper:21892/v1/logs",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.cfg.endpoints()
			if err != nil {
				t.Fatalf("endpoints: %v", err)
			}
			if got != tt.want {
				t.Fatalf("endpoints = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestConfigResourceServiceNameOverridesEnv(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "from-env")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=from-attrs,deployment.environment=staging")
	res, err := Config{ServiceName: "from-config"}.resource(t.Context())
	if err != nil {
		t.Fatalf("resource: %v", err)
	}
	if v, _ := res.Set().Value(semconv.ServiceNameKey); v.AsString() != "from-config" {
		t.Fatalf("service.name = %q, want from-config", v.AsString())
	}
	if v, _ := res.Set().Value("deployment.environment"); v.AsString() != "staging" {
		t.Fatalf("deployment.environment = %q, want staging from OTEL_RESOURCE_ATTRIBUTES", v.AsString())
	}
}
