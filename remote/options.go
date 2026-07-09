// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package remote

import (
	"crypto/tls"
	"fmt"
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

// WithClientCertificate presents a fixed certificate during the TLS handshake
// (mTLS). For material that rotates on disk, prefer WithClientCertificateFiles.
func WithClientCertificate(cert tls.Certificate) Option {
	return func(c *Client) {
		transport := transportWithTLS()
		transport.TLSClientConfig.Certificates = []tls.Certificate{cert}
		c.httpClient.Transport = transport
	}
}

// WithClientCertificateFiles presents the client certificate at certFile /
// keyFile during the TLS handshake (mTLS). The PEM files are read on each
// handshake — a per-connection, not per-request, cost — so rotated material
// is picked up by new connections with no restart (zero-downtime rotation),
// and a missing or malformed file fails the call with a clear error.
func WithClientCertificateFiles(certFile, keyFile string) Option {
	return func(c *Client) {
		transport := transportWithTLS()
		transport.TLSClientConfig.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			cert, err := tls.LoadX509KeyPair(certFile, keyFile)
			if err != nil {
				return nil, fmt.Errorf("loading client certificate: %w", err)
			}
			return &cert, nil
		}
		c.httpClient.Transport = transport
	}
}

// transportWithTLS returns a transport ready for mTLS configuration. It is
// cloned from http.DefaultTransport so proxy, HTTP/2, and connection-pool
// defaults are preserved — unless something (an APM agent, a test) has
// replaced http.DefaultTransport with a non-*http.Transport, in which case a
// fresh transport is used instead of panicking. Any pre-configured
// TLSClientConfig (custom RootCAs, ...) is kept — Transport.Clone deep-clones
// it — and MinVersion is only ever raised to TLS 1.2, never lowered.
func transportWithTLS() *http.Transport {
	var transport *http.Transport
	if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = defaultTransport.Clone()
	} else {
		transport = &http.Transport{}
	}
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	}
	if transport.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		transport.TLSClientConfig.MinVersion = tls.VersionTLS12
	}
	return transport
}
