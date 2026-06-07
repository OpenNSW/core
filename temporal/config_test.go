package temporal

import "testing"

func TestConfigValidateDefaultsOK(t *testing.T) {
	cfg := Config{Host: "localhost", Port: 7233, Namespace: "default"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateMissingHost(t *testing.T) {
	cfg := Config{Host: "", Port: 7233, Namespace: "default"}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error")
	}
}

func TestConfigValidateInvalidPort(t *testing.T) {
	cfg := Config{Host: "localhost", Port: 0, Namespace: "default"}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error")
	}
}

func TestConfigValidatePortTooHigh(t *testing.T) {
	cfg := Config{Host: "localhost", Port: 65536, Namespace: "default"}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error")
	}
}

func TestConfigValidateMissingNamespace(t *testing.T) {
	cfg := Config{Host: "localhost", Port: 7233, Namespace: ""}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() expected error")
	}
}
