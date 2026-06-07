package validation

import "fmt"

// TCPPort validates that port is a valid TCP/UDP port number.
//
// Note: the well-known port range (0-1023) being "reserved" only matters when
// *binding/listening* on a local port (often requires elevated privileges on
// Unix-like systems). For client connections, any valid port in 1-65535 is
// acceptable.
func TCPPort(name string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("%s must be between 1 and 65535", name)
	}
	return nil
}
