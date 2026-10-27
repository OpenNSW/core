// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

// Config configures OTLP/HTTP export. It is the only source for the settings
// it has fields for: the matching OTEL_* environment variables
// (OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_EXPORTER_OTLP_<SIGNAL>_ENDPOINT,
// OTEL_EXPORTER_OTLP_HEADERS, OTEL_SERVICE_NAME) are ignored, so the config
// alone says where data goes. To take a value from the environment, reference
// it from the config file, e.g. endpoint: "{{env:OTEL_EXPORTER_OTLP_ENDPOINT}}".
//
// Settings Config has no field for, such as OTEL_EXPORTER_OTLP_TIMEOUT,
// OTEL_EXPORTER_OTLP_COMPRESSION, the TLS certificate variables and
// OTEL_RESOURCE_ATTRIBUTES, are still read from the environment.
type Config struct {
	// Endpoint is the OTLP/HTTP base URL without a signal path
	// (e.g. http://otel-collector:4318). /v1/traces, /v1/metrics and /v1/logs
	// are appended for each signal that has no endpoint of its own. A full
	// .../v1/<signal> URL is accepted and stripped to the base.
	Endpoint string `yaml:"endpoint"`
	// Traces, Metrics and Logs configure one signal each. A signal with
	// neither its own endpoint nor Endpoint is not exported.
	Traces  SignalConfig `yaml:"traces"`
	Metrics SignalConfig `yaml:"metrics"`
	Logs    SignalConfig `yaml:"logs"`
	// ServiceName is the service.name resource attribute. Required when any
	// signal is exported.
	ServiceName string `yaml:"serviceName"`
	// Headers are sent with each OTLP export, e.g. an Authorization header.
	Headers map[string]string `yaml:"headers"`
}

// SignalConfig configures export of one signal.
type SignalConfig struct {
	// Endpoint is the signal's full OTLP/HTTP URL, path included
	// (e.g. http://tempo:4318/v1/traces), used as-is. It overrides
	// Config.Endpoint for this signal.
	Endpoint string `yaml:"endpoint"`
}

// endpoints holds the resolved export URL of each signal; "" means the signal
// is not exported.
type endpoints struct {
	traces, metrics, logs string
}

// Enabled reports whether any signal is exported.
func (c Config) Enabled() bool {
	for _, s := range []string{c.Endpoint, c.Traces.Endpoint, c.Metrics.Endpoint, c.Logs.Endpoint} {
		if strings.TrimSpace(s) != "" {
			return true
		}
	}
	return false
}

// Validate checks the config when export is on; with export off it needs
// nothing.
func (c Config) Validate() error {
	if !c.Enabled() {
		return nil
	}
	if _, err := c.endpoints(); err != nil {
		return err
	}
	if strings.TrimSpace(c.ServiceName) == "" {
		return errors.New("telemetry: ServiceName is required when export is on")
	}
	return nil
}

// endpoints resolves each signal's export URL from its own endpoint or the
// base Endpoint.
func (c Config) endpoints() (endpoints, error) {
	base, err := c.baseURL()
	if err != nil {
		return endpoints{}, err
	}
	resolve := func(field, override, signal string) (string, error) {
		if raw := strings.TrimSpace(override); raw != "" {
			if _, err := parseHTTPURL(field, raw); err != nil {
				return "", err
			}
			return raw, nil
		}
		if base == "" {
			return "", nil
		}
		return base + "/v1/" + signal, nil
	}
	var e endpoints
	if e.traces, err = resolve("Traces.Endpoint", c.Traces.Endpoint, "traces"); err != nil {
		return endpoints{}, err
	}
	if e.metrics, err = resolve("Metrics.Endpoint", c.Metrics.Endpoint, "metrics"); err != nil {
		return endpoints{}, err
	}
	if e.logs, err = resolve("Logs.Endpoint", c.Logs.Endpoint, "logs"); err != nil {
		return endpoints{}, err
	}
	return e, nil
}

// resource builds the service resource. OTEL_RESOURCE_ATTRIBUTES is read for
// extra attributes, but service.name always comes from Config.ServiceName.
func (c Config) resource(ctx context.Context) (*resource.Resource, error) {
	opts := []resource.Option{resource.WithFromEnv(), resource.WithTelemetrySDK()}
	if name := strings.TrimSpace(c.ServiceName); name != "" {
		opts = append(opts, resource.WithAttributes(semconv.ServiceName(name)))
	}
	res, err := resource.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("telemetry: create resource: %w", err)
	}
	return res, nil
}

// baseURL returns the OTLP/HTTP root used to build signal URLs, or "" when
// Endpoint is empty.
func (c Config) baseURL() (string, error) {
	raw := strings.TrimSpace(c.Endpoint)
	if raw == "" {
		return "", nil
	}
	u, err := parseHTTPURL("Endpoint", raw)
	if err != nil {
		return "", err
	}
	path := strings.TrimSuffix(u.Path, "/")
	for _, suffix := range []string{"/v1/metrics", "/v1/traces", "/v1/logs"} {
		if before, ok := strings.CutSuffix(path, suffix); ok {
			path = before
			break
		}
	}
	u.Path = path
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimSuffix(u.String(), "/"), nil
}

func parseHTTPURL(field, raw string) (*url.URL, error) {
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("telemetry: %s must be a valid absolute http(s) URL", field)
	}
	return u, nil
}
