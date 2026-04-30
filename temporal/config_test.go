package temporal

import "testing"

func TestConfigValidateDefaultsOK(t *testing.T) {
	cfg := Config{Host: "localhost", PortRaw: "7233", Namespace: "default"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if cfg.Port != 7233 {
		t.Fatalf("Port = %d, want %d", cfg.Port, 7233)
	}
}

func TestConfigValidateMissingHost(t *testing.T) {
	cfg := Config{Host: " ", PortRaw: "7233", Namespace: "default"}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error")
	}
}

func TestConfigValidateInvalidPort(t *testing.T) {
	cfg := Config{Host: "localhost", PortRaw: "0", Namespace: "default"}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error")
	}
}

func TestConfigValidateMissingNamespace(t *testing.T) {
	cfg := Config{Host: "localhost", PortRaw: "7233", Namespace: " "}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error")
	}
}
