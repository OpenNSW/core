// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package obs holds the logging helpers shared by remote and remote/auth: the
// log attribute keys, and redaction of URLs carried in errors.
package obs

import (
	"errors"
	"log/slog"
	"net/url"
	"strconv"
)

// Log attribute keys. The HTTP ones follow OpenTelemetry semantic conventions, so
// log records line up with the spans and metrics an instrumented transport emits.
const (
	AttrService       = "remote.service"
	AttrMethod        = "http.request.method"
	AttrServerAddress = "server.address"
	AttrServerPort    = "server.port"
	AttrURLPath       = "url.path"
	AttrStatusCode    = "http.response.status_code"
	AttrResendCount   = "http.request.resend_count"
	AttrErrorType     = "error.type"
)

// RedactURL returns the *url.Error in err's chain with the query string, fragment
// and userinfo removed from its URL. http.Client.Do and url.Parse report failures
// as a *url.Error whose message carries the full URL; net/http strips only the
// password, but the query may carry credentials too. An unparseable URL is
// replaced with "". An err with no *url.Error is returned unchanged.
//
// The result keeps the Op and cause, so errors.As and errors.Is still work, but
// not whatever wrapped the *url.Error: a wrapper such as fmt.Errorf's formats its
// message when it is created, so it still carries the URL and cannot be kept.
// Redacting the *url.Error in place would keep the wrapper and its leak. Call
// RedactURL on the error as returned by net/http or net/url, before wrapping it.
func RedactURL(err error) error {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return err
	}
	safe := ""
	if u, perr := url.Parse(ue.URL); perr == nil {
		u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment, u.User = "", false, "", "", nil
		safe = u.String()
	}
	return &url.Error{Op: ue.Op, URL: safe, Err: ue.Err}
}

// ServerAttrs returns u's server.address (the host name or IP, without the port)
// and server.port, as OpenTelemetry HTTP client spans record them. A URL with no
// explicit port gets its scheme's default (80 for http, 443 for https); for any
// other scheme the port is left out. The attributes are slog.Attr values in a
// []any, ready to pass to slog.Logger.With.
func ServerAttrs(u *url.URL) []any {
	attrs := []any{slog.String(AttrServerAddress, u.Hostname())}
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}
	if n, err := strconv.Atoi(port); err == nil {
		attrs = append(attrs, slog.Int(AttrServerPort, n))
	}
	return attrs
}
