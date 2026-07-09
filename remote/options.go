// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package remote

import (
	"crypto/tls"
	"net/http"
	"time"

	"github.com/OpenNSW/core/remote/auth"
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

// WithClientCertificate presents cert during the TLS handshake (mTLS). The
// transport is cloned from http.DefaultTransport so proxy, HTTP/2, and
// connection-pool defaults are preserved.
func WithClientCertificate(cert tls.Certificate) Option {
	return func(c *Client) {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
		c.httpClient.Transport = transport
	}
}
