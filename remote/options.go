package remote

import (
	"time"

	"github.com/OpenNSW/nsw/pkg/remote/auth"
)

type Option func(*Client)

func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		c.httpClient.Timeout = timeout
	}
}

func WithAuthenticator(a auth.Authenticator) Option {
	return func(c *Client) {
		c.authenticator = a
	}
}
