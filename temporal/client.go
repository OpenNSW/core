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
	return client.Dial(optionsFromConfig(cfg))
}

func optionsFromConfig(cfg Config) client.Options {
	return client.Options{
		HostPort:  net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Namespace: cfg.Namespace,
		Logger:    temporallog.NewStructuredLogger(slog.Default()),
	}
}
