package notifications

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
		{name: "missing path", config: Config{}, wantErr: ErrConfigPathRequired},
		{name: "valid path", config: Config{Path: "/tmp/notification.json"}},
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
