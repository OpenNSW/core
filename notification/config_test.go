// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package notification

import (
	"errors"
	"testing"
)

func TestConfig_Validate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		config  Config
		wantErr error
	}{
		{name: "no providers", config: Config{}, wantErr: ErrProvidersRequired},
		{name: "empty providers map", config: Config{Providers: map[ChannelType]map[string]any{}}, wantErr: ErrProvidersRequired},
		{name: "valid providers", config: Config{Providers: map[ChannelType]map[string]any{
			ChannelEmail: {"host": "localhost"},
		}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.config.Validate()
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("got %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
