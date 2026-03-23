package remote

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRemoteError_Error(t *testing.T) {
	t.Run("without wrapped error", func(t *testing.T) {
		err := &RemoteError{
			StatusCode: 404,
			Message:    "Not Found",
		}
		expected := "remote error (404): Not Found"
		assert.Equal(t, expected, err.Error())
	})

	t.Run("with wrapped error", func(t *testing.T) {
		err := &RemoteError{
			StatusCode: 503,
			Message:    "Backend Overloaded",
			Wrapped:    ErrServiceUnavailable,
		}
		expected := fmt.Sprintf("remote error (503): Backend Overloaded: %v", ErrServiceUnavailable)
		assert.Equal(t, expected, err.Error())
	})
}

func TestRemoteError_Unwrap(t *testing.T) {
	wrapped := errors.New("underlying problem")
	err := &RemoteError{
		StatusCode: 500,
		Message:    "Server Error",
		Wrapped:    wrapped,
	}

	// Test Unwrap method directly
	assert.Equal(t, wrapped, err.Unwrap())

	// Test integration with standard errors.Is
	assert.True(t, errors.Is(err, wrapped))
}

func TestRemoteError_Is(t *testing.T) {
	err := &RemoteError{
		StatusCode: 401,
		Message:    "Invalid Token",
		Wrapped:    ErrUnauthorized,
	}

	// Should match the wrapped sentinel error
	assert.True(t, errors.Is(err, ErrUnauthorized))

	// Should NOT match a different sentinel error
	assert.False(t, errors.Is(err, ErrNotFound))
}

func TestSentinelErrors(t *testing.T) {
	// Simple verification that sentinel errors are defined and distinct
	assert.NotNil(t, ErrRequestFailed)
	assert.NotNil(t, ErrTimeout)
	assert.NotEqual(t, ErrTimeout, ErrRequestFailed)

	assert.Contains(t, ErrTimeout.Error(), "timed out")
}
