package remote

import (
	"errors"
	"fmt"
)

var (
	ErrRequestFailed      = errors.New("remote: request failed")
	ErrTimeout            = errors.New("remote: request timed out")
	ErrServiceUnavailable = errors.New("remote: service unavailable")
	ErrUnauthorized       = errors.New("remote: unauthorized access")
	ErrBadRequest         = errors.New("remote: invalid request")
	ErrNotFound           = errors.New("remote: resource not found")
)

type RemoteError struct {
	StatusCode int
	Message    string
	Wrapped    error
}

func (e *RemoteError) Error() string {
	if e.Wrapped != nil {
		return fmt.Sprintf("remote error (%d): %s: %v", e.StatusCode, e.Message, e.Wrapped)
	}
	return fmt.Sprintf("remote error (%d): %s", e.StatusCode, e.Message)
}

func (e *RemoteError) Unwrap() error {
	return e.Wrapped
}
