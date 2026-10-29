// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package temporal

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testCA issues certificates for the tests and writes them as PEM files.
type testCA struct {
	t    *testing.T
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	// file is the CA certificate's PEM file.
	file string
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key := newKey(t)
	tmpl := &x509.Certificate{
		SerialNumber:          newSerial(t),
		Subject:               pkix.Name{CommonName: "temporal test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA certificate: %v", err)
	}
	ca := &testCA{t: t, cert: cert, key: key, file: filepath.Join(t.TempDir(), "ca.crt")}
	writePEM(t, ca.file, "CERTIFICATE", der)
	return ca
}

func (ca *testCA) pool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	return pool
}

// serverCertificate issues a certificate for localhost and 127.0.0.1.
func (ca *testCA) serverCertificate() tls.Certificate {
	ca.t.Helper()
	der, key := ca.issue(&x509.Certificate{
		Subject:     pkix.Name{CommonName: "localhost"},
		DNSNames:    []string{"localhost"},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// writeClientCertificate issues a client certificate, writes it and its key to
// certFile and keyFile, and returns the certificate's DER bytes.
func (ca *testCA) writeClientCertificate(certFile, keyFile string) []byte {
	ca.t.Helper()
	der, key := ca.issue(&x509.Certificate{
		Subject:     pkix.Name{CommonName: "temporal test client"},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		ca.t.Fatalf("marshal client key: %v", err)
	}
	writePEM(ca.t, certFile, "CERTIFICATE", der)
	writePEM(ca.t, keyFile, "EC PRIVATE KEY", keyDER)
	return der
}

func (ca *testCA) issue(tmpl *x509.Certificate) ([]byte, *ecdsa.PrivateKey) {
	ca.t.Helper()
	key := newKey(ca.t)
	tmpl.SerialNumber = newSerial(ca.t)
	tmpl.NotBefore = time.Now().Add(-time.Hour)
	tmpl.NotAfter = time.Now().Add(time.Hour)
	tmpl.KeyUsage = x509.KeyUsageDigitalSignature
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		ca.t.Fatalf("issue certificate: %v", err)
	}
	return der, key
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func newSerial(t *testing.T) *big.Int {
	t.Helper()
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		t.Fatalf("generate serial: %v", err)
	}
	return serial
}

func writePEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	data := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
