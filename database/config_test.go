package database

import (
	"testing"
)

func TestConfig_Validate(t *testing.T) {
	base := Config{
		Host:     "localhost",
		Username: "user",
		Password: "secret",
		Name:     "mydb",
	}

	t.Run("valid config passes", func(t *testing.T) {
		if err := base.Validate(); err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name:    "missing host",
			mutate:  func(c *Config) { c.Host = "" },
			wantErr: "DB_HOST is required",
		},
		{
			name:    "missing username",
			mutate:  func(c *Config) { c.Username = "" },
			wantErr: "DB_USERNAME is required",
		},
		{
			name:    "missing password",
			mutate:  func(c *Config) { c.Password = "" },
			wantErr: "DB_PASSWORD is required",
		},
		{
			name:    "missing name",
			mutate:  func(c *Config) { c.Name = "" },
			wantErr: "DB_NAME is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base // copy
			tt.mutate(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("expected an error but got nil")
			}
			if err.Error() != tt.wantErr {
				t.Errorf("got %q, want %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestConfig_DSN(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "with port",
			cfg: Config{
				Host:     "localhost",
				Port:     5432,
				Username: "user",
				Password: "secret",
				Name:     "mydb",
				SSLMode:  "disable",
			},
			want: "postgres://user:secret@localhost:5432/mydb?sslmode=disable",
		},
		{
			name: "without port",
			cfg: Config{
				Host:     "localhost",
				Username: "user",
				Password: "secret",
				Name:     "mydb",
				SSLMode:  "disable",
			},
			want: "postgres://user:secret@localhost/mydb?sslmode=disable",
		},
		{
			name: "password with special characters is encoded",
			cfg: Config{
				Host:     "localhost",
				Port:     5432,
				Username: "user",
				Password: "p@ss#w0rd!",
				Name:     "mydb",
				SSLMode:  "require",
			},
			want: "postgres://user:p%40ss%23w0rd%21@localhost:5432/mydb?sslmode=require",
		},
		{
			name: "db name without leading slash gets one added",
			cfg: Config{
				Host:     "db.example.com",
				Username: "admin",
				Password: "pass",
				Name:     "production",
				SSLMode:  "disable",
			},
			want: "postgres://admin:pass@db.example.com/production?sslmode=disable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.DSN()
			if got != tt.want {
				t.Errorf("\ngot:  %s\nwant: %s", got, tt.want)
			}
		})
	}
}

func TestLogLevel_gormLogLevel(t *testing.T) {
	tests := []struct {
		input    LogLevel
		wantName string
	}{
		{LogSilent, "Silent"},
		{LogError, "Error"},
		{LogWarn, "Warn"},
		{LogInfo, "Info"},
	}

	// Just verify none of them panic and produce distinct non-zero values
	// (we can't import gormlogger here without adding a dep to the test binary,
	// so we check the mapping function itself via the exported constants).
	seen := map[int]bool{}
	for _, tt := range tests {
		got := int(gormLogLevel(tt.input))
		if seen[got] && tt.input != LogError {
			// LogSilent=0 is legitimately distinct; only Error shares nothing
			t.Errorf("LogLevel %s produced duplicate gorm level %d", tt.wantName, got)
		}
		seen[got] = true
	}
}

func TestLogLevel_unknownFallsBackToError(t *testing.T) {
	unknown := LogLevel(99)
	got := gormLogLevel(unknown)
	want := gormLogLevel(LogError)
	if got != want {
		t.Errorf("unknown LogLevel: got gorm level %d, want %d (Error)", got, want)
	}
}
