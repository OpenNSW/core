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
	c, err := client.Dial(optionsFromConfig(cfg))
	if err != nil {
		return nil, err
	}
	slog.Info("temporal client connected", "host", cfg.Host, "namespace", cfg.Namespace)
	return c, nil
}

func optionsFromConfig(cfg Config) client.Options {
	return client.Options{
		HostPort:  net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Namespace: cfg.Namespace,
		Logger:    temporallog.NewStructuredLogger(slog.Default()),
	}
}
