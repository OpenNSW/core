// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package drivers

import (
	"strconv"
	"testing"
)

func TestTokens_UploadTokenIsNotADownloadToken(t *testing.T) {
	const (
		secret      = "secret"
		key         = "550e8400-e29b-41d4-a716-446655440000.pdf"
		expiresAt   = int64(1791000000)
		contentType = "application/pdf"
		maxSize     = int64(2 << 30)
	)
	upload := GenerateToken(key, secret, expiresAt, contentType, maxSize)

	// Without an operation label, the upload payload "key\x00exp\x00ct\x00max"
	// is also the download payload for this key and expiry, with the rest as
	// the disposition.
	craftedDisposition := contentType + "\x00" + strconv.FormatInt(maxSize, 10)
	if VerifyDownloadToken(key, upload, secret, expiresAt, craftedDisposition) {
		t.Fatal("an upload token verified as a download token")
	}
}

func TestTokens_RoundTrip(t *testing.T) {
	const (
		secret = "secret"
		key    = "550e8400-e29b-41d4-a716-446655440000.pdf"
	)
	if !VerifyToken(key, GenerateToken(key, secret, 1791000000, "application/pdf", 1024), secret, 1791000000, "application/pdf", 1024) {
		t.Error("upload token did not verify")
	}
	for _, disposition := range []string{DispositionInline, DispositionAttachment} {
		if !VerifyDownloadToken(key, GenerateDownloadToken(key, secret, 1791000000, disposition), secret, 1791000000, disposition) {
			t.Errorf("download token for %s did not verify", disposition)
		}
	}
}

func TestTokens_DownloadTokenIsBoundToItsDisposition(t *testing.T) {
	const (
		secret    = "secret"
		key       = "550e8400-e29b-41d4-a716-446655440000.xml"
		expiresAt = int64(1791000000)
	)
	token := GenerateDownloadToken(key, secret, expiresAt, DispositionAttachment)

	if VerifyDownloadToken(key, token, secret, expiresAt, DispositionInline) {
		t.Error("a token issued for an attachment verified for inline")
	}
	if VerifyDownloadToken(key, token, secret, expiresAt, "") {
		t.Error("a token issued for an attachment verified with no disposition")
	}
}
