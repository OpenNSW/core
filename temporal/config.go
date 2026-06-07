package temporal

import (
	"fmt"

	"github.com/OpenNSW/core/internal/validation"
)

// Config holds configuration required to connect to Temporal.
//
// This is owned by the temporal package (similar to other internal packages),
// so the package controls the shape/semantics of its configuration.
//
// Host/Port are kept separate to make configuration via environment variables
// easier and more explicit.
type Config struct {
	Host      string
	Port      int
	Namespace string
}

// Validate ensures the Temporal configuration is usable.
func (c Config) Validate() error {
	if c.Host == "" {
		return fmt.Errorf("TEMPORAL_HOST is required")
	}
	if err := validation.TCPPort("TEMPORAL_PORT", c.Port); err != nil {
		return err
	}
	if c.Namespace == "" {
		return fmt.Errorf("TEMPORAL_NAMESPACE is required")
	}
	return nil
}
