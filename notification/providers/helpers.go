package providers

import (
	"errors"
	"fmt"
	"net"
	"net/url"
)

// validateBaseURL ensures baseURL is absolute and uses HTTPS. Loopback hosts
// (localhost and the 127.0.0.0/8 and ::1 ranges) are exempt to support local
// development.
func validateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid baseURL: %w", err)
	}
	if u.Scheme == "" || u.Hostname() == "" {
		return errors.New("baseURL must be an absolute URL")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	isLoopback := host == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !isLoopback {
		return errors.New("baseURL must use HTTPS (except loopback hosts)")
	}
	return nil
}
