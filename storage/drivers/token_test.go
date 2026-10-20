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
	// is also the download payload for this key and expiry.
	craftedKey := key + "\x00" + strconv.FormatInt(expiresAt, 10) + "\x00" + contentType
	if VerifyDownloadToken(craftedKey, upload, secret, maxSize) {
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
	if !VerifyDownloadToken(key, GenerateDownloadToken(key, secret, 1791000000), secret, 1791000000) {
		t.Error("download token did not verify")
	}
}
