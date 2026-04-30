package temporal

import (
	"testing"
)

func TestOptionsFromConfigMapping(t *testing.T) {
	cfg := Config{Host: "localhost", Port: 7233, Namespace: "default"}
	opts := optionsFromConfig(cfg)

	if opts.HostPort != "localhost:7233" {
		t.Fatalf("HostPort = %q, want %q", opts.HostPort, "localhost:7233")
	}
	if opts.Namespace != "default" {
		t.Fatalf("Namespace = %q, want %q", opts.Namespace, "default")
	}
}

func TestOptionsFromConfigOverrides(t *testing.T) {
	cfg := Config{Host: "temporal.example", Port: 7233, Namespace: "staging"}
	opts := optionsFromConfig(cfg)

	if opts.HostPort != "temporal.example:7233" {
		t.Fatalf("HostPort = %q, want %q", opts.HostPort, "temporal.example:7233")
	}
	if opts.Namespace != "staging" {
		t.Fatalf("Namespace = %q, want %q", opts.Namespace, "staging")
	}
}
