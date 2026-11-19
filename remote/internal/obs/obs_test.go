// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package obs

import (
	"errors"
	"fmt"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedactURL(t *testing.T) {
	cause := errors.New("connection refused")

	tests := []struct {
		name    string
		url     string
		wantURL string
	}{
		{"query", "https://api.example.com/v1/items?token=secret", "https://api.example.com/v1/items"},
		{"empty query", "https://api.example.com/v1/items?", "https://api.example.com/v1/items"},
		{"fragment", "https://api.example.com/v1/items#secret", "https://api.example.com/v1/items"},
		{"userinfo", "https://user:secret@api.example.com/v1", "https://api.example.com/v1"},
		{"no query", "https://api.example.com/v1/items", "https://api.example.com/v1/items"},
		{"unparseable", "https://api.example.com/%zz?token=secret", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RedactURL(&url.Error{Op: "Get", URL: tt.url, Err: cause})
			assert.Equal(t, fmt.Sprintf("Get %q: connection refused", tt.wantURL), err.Error())

			var ue *url.Error
			require.ErrorAs(t, err, &ue)
			assert.Equal(t, "Get", ue.Op)
			assert.Equal(t, tt.wantURL, ue.URL)
			assert.ErrorIs(t, err, cause)
			assert.NotContains(t, err.Error(), "secret")
		})
	}

	t.Run("an already-wrapped error drops the wrapper rather than leak", func(t *testing.T) {
		// fmt.Errorf formats its message when it is created, so the wrapper's text
		// carries the full URL however the *url.Error inside is changed.
		in := fmt.Errorf("wrapped: %w", &url.Error{Op: "Get", URL: "https://api.example.com/v1?token=secret", Err: cause})

		err := RedactURL(in)
		assert.Equal(t, `Get "https://api.example.com/v1": connection refused`, err.Error())
		assert.ErrorIs(t, err, cause)
		assert.Contains(t, in.Error(), "secret", "precondition: the wrapper's own message still has the URL")
	})

	t.Run("non-URL error passes through", func(t *testing.T) {
		assert.Same(t, cause, RedactURL(cause))
	})
}
