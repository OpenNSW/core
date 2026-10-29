// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package providers

import "crypto/x509"

// SetSMTPRootCAs makes p trust the certificates in pool, so a test can run a
// TLS server with a self-signed certificate.
func SetSMTPRootCAs(p *SMTPProvider, pool *x509.CertPool) { p.rootCAs = pool }
