package database

import "testing"

// TestClose_NilDB verifies Close does not panic or error on a nil *gorm.DB.
func TestClose_NilDB(t *testing.T) {
	if err := Close(nil); err != nil {
		t.Errorf("Close(nil) returned unexpected error: %v", err)
	}
}

// TestHealthCheck_NilDB verifies HealthCheck returns a meaningful error for nil.
func TestHealthCheck_NilDB(t *testing.T) {
	err := HealthCheck(nil)
	if err == nil {
		t.Fatal("expected an error for nil db, got nil")
	}
	const want = "database is nil"
	if err.Error() != want {
		t.Errorf("got %q, want %q", err.Error(), want)
	}
}

// TestNew_InvalidConfig verifies New rejects a config that fails Validate
// without ever attempting a real connection.
// We rely on the fact that an empty DSN causes gorm.Open to fail fast, so
// no actual database is needed for this test.
func TestNew_InvalidConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name:    "missing host",
			cfg:     Config{Username: "u", Password: "p", Name: "db"},
			wantErr: "DB_HOST is required",
		},
		{
			name:    "missing username",
			cfg:     Config{Host: "localhost", Password: "p", Name: "db"},
			wantErr: "DB_USERNAME is required",
		},
		{
			name:    "missing password",
			cfg:     Config{Host: "localhost", Username: "u", Name: "db"},
			wantErr: "DB_PASSWORD is required",
		},
		{
			name:    "missing name",
			cfg:     Config{Host: "localhost", Username: "u", Password: "p"},
			wantErr: "DB_NAME is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.cfg)
			if err == nil {
				t.Fatal("expected an error but got nil")
			}
			if err.Error() != tt.wantErr {
				t.Errorf("got %q, want %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestNew_ValidConfigNoServer confirms that a fully valid config still produces
// a connection error (not a validation error) when no real server is reachable.
// This keeps the test hermetic while proving the validation path is cleared.
func TestNew_ValidConfigNoServer(t *testing.T) {
	cfg := Config{
		Host:     "127.0.0.1",
		Port:     1, // nothing listens here
		Username: "user",
		Password: "password",
		Name:     "testdb",
		SSLMode:  "disable",
	}

	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected a connection error when no server is running, got nil")
	}

	// The error must NOT be a validation error — it should come from the
	// connect/ping stage, proving Validate() passed.
	validationErrors := []string{
		"DB_HOST is required",
		"DB_USERNAME is required",
		"DB_PASSWORD is required",
		"DB_NAME is required",
	}
	for _, ve := range validationErrors {
		if err.Error() == ve {
			t.Errorf("got a validation error when one was not expected: %v", err)
		}
	}
}

// TestNew_LogLevelDoesNotPanic verifies every LogLevel value can be passed to
// New without panicking (the mapping to GORM's level happens before Open).
func TestNew_LogLevelDoesNotPanic(t *testing.T) {
	levels := []LogLevel{LogSilent, LogError, LogWarn, LogInfo}

	cfg := Config{
		Host:     "127.0.0.1",
		Port:     1,
		Username: "user",
		Password: "password",
		Name:     "testdb",
		SSLMode:  "disable",
	}

	for _, lvl := range levels {
		cfg.LogLevel = lvl
		// We don't care about the error (no server); we just want no panic.
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("LogLevel %d caused a panic: %v", lvl, r)
				}
			}()
			//nolint:errcheck
			New(cfg) //nolint:errcheck
		}()
	}
}
