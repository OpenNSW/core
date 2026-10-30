// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package plugins

import (
	"errors"
	"net/http"

	"go.temporal.io/sdk/temporal"

	"github.com/OpenNSW/core/remote"
)

// The types a Permanent error is given, so an admin reading a parked step can tell what kind of
// failure it was. A plugin may use its own.
const (
	// ErrorTypeConfig is a step template, task template or plugin configuration that is missing,
	// malformed or names something that isn't there.
	ErrorTypeConfig = "InvalidConfig"
	// ErrorTypeInput is step data the plugin can't use: a missing field, a mapping error, a value
	// of the wrong shape.
	ErrorTypeInput = "InvalidInput"
	// ErrorTypeRejected is a remote service refusing the request with a status sending it again
	// can't change.
	ErrorTypeRejected = "RemoteRejected"
)

// PermanentError is a failure that retrying can't fix. Make one with Permanent.
type PermanentError struct {
	// Type names the kind of failure (see the ErrorType constants).
	Type string
	Err  error
}

func (e *PermanentError) Error() string { return e.Err.Error() }

func (e *PermanentError) Unwrap() error { return e.Err }

// Permanent marks err as a failure that retrying can't fix, so the step parks for an admin at once
// instead of being retried. errType names the kind of failure (see the ErrorType constants). The
// mark survives wrapping with %w: the orchestrator finds it when the error leaves the Activity.
//
// Return it for errors the same input will always hit: bad configuration, bad step data, and a
// remote service refusing the request. Leave transient errors (timeouts, connection failures,
// 5xx, 429, database errors) unmarked, and the step is retried.
func Permanent(errType string, err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{Type: errType, Err: err}
}

// IsPermanent reports whether err, or an error it wraps, was marked Permanent or is a non-retryable
// Temporal ApplicationError.
func IsPermanent(err error) bool {
	_, ok := PermanentType(err)
	return ok
}

// PermanentType returns the type of the Permanent error, or non-retryable Temporal ApplicationError,
// that err is or wraps, and false when it wraps neither.
func PermanentType(err error) (string, bool) {
	var permErr *PermanentError
	if errors.As(err, &permErr) {
		return permErr.Type, true
	}
	var appErr *temporal.ApplicationError
	if errors.As(err, &appErr) && appErr.NonRetryable() {
		return appErr.Type(), true
	}
	return "", false
}

// RetryableStatus reports whether a request that got HTTP status code can succeed when it is sent
// again: 408, 429 and every 5xx.
func RetryableStatus(code int) bool {
	return code == http.StatusRequestTimeout || code == http.StatusTooManyRequests || code >= 500
}

// ClassifyRemote returns err marked Permanent when it is a remote service's error response whose
// status RetryableStatus rejects (any other 4xx), and err unchanged otherwise. A transport error
// (timeout, connection failure) stays retryable. Use it on the errors of a remote.Manager or
// remote.Client call.
func ClassifyRemote(err error) error {
	var remoteErr *remote.RemoteError
	if errors.As(err, &remoteErr) && !RetryableStatus(remoteErr.StatusCode) {
		return Permanent(ErrorTypeRejected, err)
	}
	return err
}
