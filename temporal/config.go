package temporal

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/OpenNSW/nsw/internal/validation"
)

// Config holds configuration required to connect to Temporal.
//
// This is owned by the temporal package (similar to other internal packages),
// so the package controls the shape/semantics of its configuration.
//
// Host/Port are kept separate to make configuration via environment variables
// easier and more explicit.
type Config struct {
	Host string
	// Port is the parsed TCP port number, populated by Validate().
	Port int
	// PortRaw is the raw port value (typically from the TEMPORAL_PORT env var).
	PortRaw   string
	Namespace string
}

// Validate ensures the Temporal configuration is usable.
func (c *Config) Validate() error {
	c.Host = strings.TrimSpace(c.Host)
	c.Namespace = strings.TrimSpace(c.Namespace)
	c.PortRaw = strings.TrimSpace(c.PortRaw)

	if c.Host == "" {
		return fmt.Errorf("TEMPORAL_HOST is required")
	}
	portStr := c.PortRaw
	if portStr == "" {
		return fmt.Errorf("TEMPORAL_PORT is required")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("invalid TEMPORAL_PORT: %w", err)
	}
	if err := validation.TCPPort("TEMPORAL_PORT", port); err != nil {
		return err
	}
	if c.Namespace == "" {
		return fmt.Errorf("TEMPORAL_NAMESPACE is required")
	}

	c.Port = port
	return nil
}
