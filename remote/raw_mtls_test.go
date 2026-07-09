// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package remote

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_RawRequest_SendsBodyVerbatimWithContentType(t *testing.T) {
	var gotContentType, gotSOAPAction, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotSOAPAction = r.Header.Get("SOAPAction")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<ok/>"))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	resp, err := client.RawRequest(context.Background(), RawRequest{
		Method:      "POST",
		Path:        "/soap",
		ContentType: "text/xml; charset=utf-8",
		Body:        []byte("<Envelope/>"),
		Headers:     map[string]string{"SOAPAction": `""`},
	})
	require.NoError(t, err)

	// The raw body must not be JSON-wrapped, and the caller's Content-Type must
	// win over the JSON default.
	assert.Equal(t, "<Envelope/>", gotBody)
	assert.Equal(t, "text/xml; charset=utf-8", gotContentType)
	assert.Equal(t, `""`, gotSOAPAction)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "<ok/>", string(resp.Body))
	assert.Equal(t, "text/xml", resp.Header.Get("Content-Type"))
}

func TestClient_RawRequest_Non2xxIsNotAnError(t *testing.T) {
	// SOAP faults arrive as HTTP 500 with a meaningful body; RawRequest must
	// hand both back rather than swallowing the body in an error.
	const fault = `<Envelope><Body><Fault><faultstring>boom</faultstring></Fault></Body></Envelope>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(fault))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	resp, err := client.RawRequest(context.Background(), RawRequest{Method: "POST", Path: "/", Body: []byte("<x/>")})
	require.NoError(t, err)
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Equal(t, fault, string(resp.Body))
}

func TestClient_RawRequest_CapsResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := make([]byte, 1024*1024)
		for range 11 { // 11 MiB > 10 MiB cap
			_, _ = w.Write(chunk)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL)
	resp, err := client.RawRequest(context.Background(), RawRequest{Method: "GET", Path: "/"})
	require.NoError(t, err)
	assert.Len(t, resp.Body, maxRawResponseBytes)
}

// writeClientCertPairInto generates a self-signed certificate with the given
// CommonName and writes (or overwrites) the PEM cert + key files in dir,
// returning their paths.
func writeClientCertPairInto(t *testing.T, dir, commonName string) (certFile, keyFile string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)

	certFile = filepath.Join(dir, "client.crt")
	keyFile = filepath.Join(dir, "client.key")
	require.NoError(t, os.WriteFile(certFile,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(keyFile,
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600))
	return certFile, keyFile
}

func writeClientCertPair(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	return writeClientCertPairInto(t, t.TempDir(), "test-nppo")
}

// writeTLSServices writes a services.json with a single service carrying a tls
// block, and returns its path.
func writeTLSServices(t *testing.T, url, certFile, keyFile string) string {
	t.Helper()
	body := fmt.Sprintf(
		`{"version":"1.0","services":[{"id":"svc","url":%q,"tls":{"client_cert_file":%q,"client_key_file":%q}}]}`,
		url, certFile, keyFile,
	)
	path := filepath.Join(t.TempDir(), "services.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestManager_LoadServices_MTLSPresentsClientCertificate(t *testing.T) {
	var gotClientCert string
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			gotClientCert = r.TLS.PeerCertificates[0].Subject.CommonName
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<ok/>"))
	}))
	server.TLS = &tls.Config{ClientAuth: tls.RequireAnyClientCert}
	server.StartTLS()
	defer server.Close()

	certFile, keyFile := writeClientCertPair(t)
	manager := NewManager()
	require.NoError(t, manager.LoadServices(writeTLSServices(t, server.URL, certFile, keyFile)))

	// The production tls.Config trusts only system roots, so teach this test
	// client to trust the httptest server's self-signed certificate.
	client, err := manager.GetClient("svc")
	require.NoError(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	client.httpClient.Transport.(*http.Transport).TLSClientConfig.RootCAs = pool

	resp, err := manager.CallRaw(context.Background(), "svc", RawRequest{Method: "POST", Path: "/", Body: []byte("<x/>")})
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "test-nppo", gotClientCert)
}

func TestManager_MissingCertFileFailsPerCallNotAtBoot(t *testing.T) {
	// mTLS material is operator-supplied and may be absent (e.g. dev setups):
	// loading the services must still succeed, and the failure must surface on
	// first use of this service — at the handshake — and only for this service.
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server.TLS = &tls.Config{ClientAuth: tls.RequireAnyClientCert}
	server.StartTLS()
	defer server.Close()

	path := writeTLSServices(t, server.URL, "/nonexistent/client.crt", "/nonexistent/client.key")
	manager := NewManager()
	require.NoError(t, manager.LoadServices(path))

	client, err := manager.GetClient("svc")
	require.NoError(t, err, "a missing PEM must not fail the client build")
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	client.httpClient.Transport.(*http.Transport).TLSClientConfig.RootCAs = pool

	_, err = manager.CallRaw(context.Background(), "svc", RawRequest{Method: "GET", Path: "/"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loading client certificate")
}

func TestManager_CertRotationPickedUpWithoutRestart(t *testing.T) {
	var mu sync.Mutex
	var gotCNs []string
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			mu.Lock()
			gotCNs = append(gotCNs, r.TLS.PeerCertificates[0].Subject.CommonName)
			mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	server.TLS = &tls.Config{ClientAuth: tls.RequireAnyClientCert}
	server.StartTLS()
	defer server.Close()

	dir := t.TempDir()
	certFile, keyFile := writeClientCertPairInto(t, dir, "before-rotation")

	manager := NewManager()
	require.NoError(t, manager.LoadServices(writeTLSServices(t, server.URL, certFile, keyFile)))
	client, err := manager.GetClient("svc")
	require.NoError(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	transport := client.httpClient.Transport.(*http.Transport)
	transport.TLSClientConfig.RootCAs = pool

	_, err = manager.CallRaw(context.Background(), "svc", RawRequest{Method: "GET", Path: "/"})
	require.NoError(t, err)

	// Rotate the material on disk and force a fresh connection (a real rotation
	// is picked up as pooled connections naturally close and re-dial).
	writeClientCertPairInto(t, dir, "after-rotation")
	transport.CloseIdleConnections()

	_, err = manager.CallRaw(context.Background(), "svc", RawRequest{Method: "GET", Path: "/"})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"before-rotation", "after-rotation"}, gotCNs)
}

func TestClient_WithClientCertificate_PreservesCustomTransportTLSConfig(t *testing.T) {
	// An APM agent or corporate-proxy setup may have replaced
	// http.DefaultTransport with one carrying its own TLSClientConfig (custom
	// RootCAs, stricter MinVersion). Adding a client certificate must keep
	// those settings, and never lower the minimum TLS version.
	pool := x509.NewCertPool()
	prev := http.DefaultTransport
	http.DefaultTransport = &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS13,
	}}
	defer func() { http.DefaultTransport = prev }()

	client := NewClient("http://local", WithClientCertificate(tls.Certificate{}))
	got := client.httpClient.Transport.(*http.Transport).TLSClientConfig
	assert.Same(t, pool, got.RootCAs)
	assert.Equal(t, uint16(tls.VersionTLS13), got.MinVersion, "pre-set stricter MinVersion must not be lowered")
	assert.Len(t, got.Certificates, 1)
}

func TestClient_WithClientCertificate_NonTransportDefaultDoesNotPanic(t *testing.T) {
	prev := http.DefaultTransport
	http.DefaultTransport = roundTripperFunc(func(r *http.Request) (*http.Response, error) { return nil, nil })
	defer func() { http.DefaultTransport = prev }()

	client := NewClient("http://local", WithClientCertificate(tls.Certificate{}))
	got := client.httpClient.Transport.(*http.Transport).TLSClientConfig
	assert.Equal(t, uint16(tls.VersionTLS12), got.MinVersion)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestManager_LoadServices_TLSRequiresBothFiles(t *testing.T) {
	body := `{"version":"1.0","services":[{"id":"svc","url":"http://local","tls":{"client_cert_file":"/some.crt"}}]}`
	path := filepath.Join(t.TempDir(), "services.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	manager := NewManager()
	err := manager.LoadServices(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires both client_cert_file and client_key_file")
}

func TestManager_CallRaw_UnknownService(t *testing.T) {
	manager := NewManager()
	_, err := manager.CallRaw(context.Background(), "ghost", RawRequest{Method: "GET", Path: "/"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `service "ghost" not found`)
}
