package drivers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// sign creates an HMAC-SHA256 signature from given parts
func sign(secret string, parts ...any) string {
	strParts := make([]string, len(parts))
	for i, p := range parts {
		strParts[i] = fmt.Sprintf("%v", p)
	}

	payload := strings.Join(strParts, "\x00")

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))

	return hex.EncodeToString(mac.Sum(nil))
}

// verify checks token validity using constant-time comparison
func verify(token, secret string, parts ...any) bool {
	if token == "" || secret == "" {
		return false
	}

	expected := sign(secret, parts...)
	return hmac.Equal([]byte(token), []byte(expected))
}

// GenerateToken creates an HMAC-SHA256 token signing multiple constraints.
func GenerateToken(key, secret string, expiresAt int64, contentType string, maxSizeBytes int64) string {
	return sign(secret, key, expiresAt, contentType, maxSizeBytes)
}

// VerifyToken checks if a provided token matches the expected signature for given constraints.
func VerifyToken(key, token, secret string, expiresAt int64, contentType string, maxSizeBytes int64) bool {
	return verify(token, secret, key, expiresAt, contentType, maxSizeBytes)
}

// GenerateDownloadToken creates an HMAC-SHA256 token specifically for download links (only signs key and expiration).
func GenerateDownloadToken(key, secret string, expiresAt int64) string {
	return sign(secret, key, expiresAt)
}

// VerifyDownloadToken checks if a provided download token matches the expected signature.
func VerifyDownloadToken(key, token, secret string, expiresAt int64) bool {
	return verify(token, secret, key, expiresAt)
}
