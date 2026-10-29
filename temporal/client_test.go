// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package temporal

import (
	"bytes"
	"context"
	"crypto/tls"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

func TestOptionsFromConfigMapping(t *testing.T) {
	cfg := Config{Host: "localhost", Port: 7233, Namespace: "default"}
	opts, err := optionsFromConfig(cfg)
	if err != nil {
		t.Fatalf("optionsFromConfig() error = %v", err)
	}

	if opts.HostPort != "localhost:7233" {
		t.Fatalf("HostPort = %q, want %q", opts.HostPort, "localhost:7233")
	}
	if opts.Namespace != "default" {
		t.Fatalf("Namespace = %q, want %q", opts.Namespace, "default")
	}
	if opts.ConnectionOptions.TLS != nil {
		t.Fatalf("ConnectionOptions.TLS set without a tls block, want a plaintext connection")
	}
}

func TestOptionsFromConfigOverrides(t *testing.T) {
	cfg := Config{Host: "temporal.example", Port: 7233, Namespace: "staging"}
	opts, err := optionsFromConfig(cfg)
	if err != nil {
		t.Fatalf("optionsFromConfig() error = %v", err)
	}

	if opts.HostPort != "temporal.example:7233" {
		t.Fatalf("HostPort = %q, want %q", opts.HostPort, "temporal.example:7233")
	}
	if opts.Namespace != "staging" {
		t.Fatalf("Namespace = %q, want %q", opts.Namespace, "staging")
	}
}

func TestOptionsFromConfigTLS(t *testing.T) {
	ca := newTestCA(t)
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	ca.writeClientCertificate(certFile, keyFile)

	cfg := Config{
		Host:      "temporal.example",
		Port:      7233,
		Namespace: "default",
		TLS:       &TLSConfig{CAFile: ca.file, CertFile: certFile, KeyFile: keyFile},
	}
	opts, err := optionsFromConfig(cfg)
	if err != nil {
		t.Fatalf("optionsFromConfig() error = %v", err)
	}

	tlsConfig := opts.ConnectionOptions.TLS
	if tlsConfig == nil {
		t.Fatalf("ConnectionOptions.TLS not set for a tls block")
	}
	if tlsConfig.RootCAs == nil {
		t.Fatalf("RootCAs not set from caFile")
	}
	if len(tlsConfig.Certificates) != 1 {
		t.Fatalf("Certificates not set for certFile/keyFile")
	}
}

func TestOptionsFromConfigTLSError(t *testing.T) {
	cfg := Config{
		Host:      "temporal.example",
		Port:      7233,
		Namespace: "default",
		TLS:       &TLSConfig{CAFile: filepath.Join(t.TempDir(), "missing.crt")},
	}
	if _, err := optionsFromConfig(cfg); err == nil {
		t.Fatalf("optionsFromConfig() error = nil for a missing caFile")
	}
}

// serverStub answers the capabilities call the Temporal SDK makes when it
// dials, and records the client certificate that call came with.
type serverStub struct {
	workflowservice.UnimplementedWorkflowServiceServer
	clientCert chan []byte
}

func (s *serverStub) GetSystemInfo(ctx context.Context, _ *workflowservice.GetSystemInfoRequest) (*workflowservice.GetSystemInfoResponse, error) {
	if p, ok := peer.FromContext(ctx); ok {
		if info, ok := p.AuthInfo.(credentials.TLSInfo); ok && len(info.State.PeerCertificates) > 0 {
			select {
			case s.clientCert <- info.State.PeerCertificates[0].Raw:
			default:
			}
		}
	}
	return &workflowservice.GetSystemInfoResponse{}, nil
}

// startMutualTLSServer starts a Temporal server stub that requires a client
// certificate signed by ca. It returns the stub's port and the stub.
func startMutualTLSServer(t *testing.T, ca *testCA) (int, *serverStub) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{ca.serverCertificate()},
		ClientCAs:    ca.pool(),
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	})))
	stub := &serverStub{clientCert: make(chan []byte, 1)}
	workflowservice.RegisterWorkflowServiceServer(srv, stub)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().(*net.TCPAddr).Port, stub
}

func TestNewClientMutualTLS(t *testing.T) {
	ca := newTestCA(t)
	port, stub := startMutualTLSServer(t, ca)
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	clientCert := ca.writeClientCertificate(certFile, keyFile)

	c, err := NewClient(Config{
		Host:      "127.0.0.1",
		Port:      port,
		Namespace: "default",
		TLS:       &TLSConfig{CAFile: ca.file, CertFile: certFile, KeyFile: keyFile},
	})
	if err != nil {
		t.Fatalf("NewClient() with a client certificate error = %v", err)
	}
	defer c.Close()

	select {
	case got := <-stub.clientCert:
		if !bytes.Equal(got, clientCert) {
			t.Fatalf("the Temporal server saw a different client certificate than certFile")
		}
	default:
		t.Fatalf("the Temporal server saw no client certificate")
	}
}

func TestDialFailsWithoutRequiredClientCertificate(t *testing.T) {
	ca := newTestCA(t)
	port, _ := startMutualTLSServer(t, ca)

	opts, err := optionsFromConfig(Config{
		Host:      "127.0.0.1",
		Port:      port,
		Namespace: "default",
		TLS:       &TLSConfig{CAFile: ca.file},
	})
	if err != nil {
		t.Fatalf("optionsFromConfig() error = %v", err)
	}
	opts.ConnectionOptions.GetSystemInfoTimeout = 2 * time.Second

	c, err := client.Dial(opts)
	if err == nil {
		c.Close()
		t.Fatalf("Dial() without a client certificate succeeded against a server that requires one")
	}
	// The Temporal server refuses the handshake with a certificate-required
	// alert, but depending on timing the client reports that alert or the
	// closed connection. An x509 error would mean the client rejected the
	// server's certificate instead, so client authentication was never reached.
	if strings.Contains(err.Error(), "x509") {
		t.Fatalf("Dial() error = %q, want a refusal by the Temporal server, not by the client", err)
	}
}
