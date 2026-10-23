// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package temporal

import (
	"log/slog"
	"net"
	"strconv"

	"go.temporal.io/sdk/client"
	temporallog "go.temporal.io/sdk/log"
)

// NewClient creates a shared Temporal client for all workflow runtimes.
func NewClient(cfg Config) (client.Client, error) {
	opts, err := optionsFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	c, err := client.Dial(opts)
	if err != nil {
		return nil, err
	}
	slog.Info("temporal client connected", "host", cfg.Host, "namespace", cfg.Namespace, "tls", cfg.TLS != nil, "encryption", cfg.Encryption != nil)
	return c, nil
}

func optionsFromConfig(cfg Config) (client.Options, error) {
	opts := client.Options{
		HostPort:  net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Namespace: cfg.Namespace,
		Logger:    temporallog.NewStructuredLogger(slog.Default()),
	}
	if cfg.TLS != nil {
		tlsConfig, err := clientTLSConfig(*cfg.TLS)
		if err != nil {
			return client.Options{}, err
		}
		opts.ConnectionOptions.TLS = tlsConfig
	}
	if cfg.Encryption != nil {
		dataConverter, failureConverter, err := encryptingConverters(*cfg.Encryption)
		if err != nil {
			return client.Options{}, err
		}
		opts.DataConverter = dataConverter
		opts.FailureConverter = failureConverter
	}
	return opts, nil
}
