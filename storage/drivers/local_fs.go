package drivers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ErrInvalidPath is returned when a key or resolved path is invalid (e.g. path traversal).
// Callers can use errors.Is(err, drivers.ErrInvalidPath) to detect validation failures.
var ErrInvalidPath = errors.New("invalid path: traversal or invalid key not allowed")

var (
	errInvalidKey  = errors.New("invalid key: path traversal not allowed")
	errPathOutside = errors.New("path outside base directory")
)

// LocalFSDriver implements StorageDriver for local disk with directory hashing
type LocalFSDriver struct {
	BaseDir    string
	PublicURL  string
	secretKey  string
	presignTTL time.Duration
}

// NewLocalFSDriver creates a new LocalFSDriver.
// baseDir is where files will be stored.
// publicURL is the base URL used to generate public links (e.g., /api/storage).
// secretKey is the secret used for HMAC signing of local-put upload URLs.
// presignTTL is the default time-to-live for presigned URLs.
func NewLocalFSDriver(baseDir, publicURL, secretKey string, presignTTL time.Duration) (*LocalFSDriver, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create base directory: %w", err)
	}
	if presignTTL == 0 {
		presignTTL = DefaultPresignTTL
	}
	return &LocalFSDriver{BaseDir: baseDir, PublicURL: publicURL, secretKey: secretKey, presignTTL: presignTTL}, nil
}

// getHashedPath generates a two-level deep path for a key to avoid flat directory issues.
func (d *LocalFSDriver) getHashedPath(key string) string {
	if len(key) < 4 {
		return key
	}
	return filepath.Join(key[0:2], key[2:4], key)
}

// resolveAndValidate returns the absolute path for key and ensures it is under BaseDir.
// Uses EvalSymlinks on the base so symlinks cannot be used to escape the root.
func (d *LocalFSDriver) resolveAndValidate(key string) (fullAbs string, err error) {
	if strings.Contains(key, "..") || strings.Contains(key, "/") || strings.Contains(key, "\\") {
		return "", fmt.Errorf("invalid key: %w", errors.Join(ErrInvalidPath, errInvalidKey))
	}
	baseAbs, err := filepath.Abs(d.BaseDir)
	if err != nil {
		return "", fmt.Errorf("base directory resolution: %w", err)
	}
	baseResolved := baseAbs
	if resolved, evalErr := filepath.EvalSymlinks(baseAbs); evalErr == nil {
		baseResolved = resolved
	}
	hashed := d.getHashedPath(key)
	fullPath := filepath.Join(baseResolved, hashed)
	fullAbs, err = filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("path resolution: %w", err)
	}
	rel, err := filepath.Rel(baseResolved, fullAbs)
	if err != nil {
		return "", fmt.Errorf("path resolution: %w", err)
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path outside base: %w", errors.Join(ErrInvalidPath, errPathOutside))
	}
	return fullAbs, nil
}

func (d *LocalFSDriver) Save(ctx context.Context, key string, body io.Reader, contentType string) error {
	fullAbs, err := d.resolveAndValidate(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(fullAbs), 0755); err != nil {
		return fmt.Errorf("failed to create hashed directory: %w", err)
	}

	file, err := os.Create(fullAbs)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer func() { _ = file.Close() }()

	if _, err := io.Copy(file, body); err != nil {
		_ = file.Close()
		_ = os.Remove(fullAbs)
		return fmt.Errorf("failed to save file content: %w", err)
	}

	metaPath := fullAbs + ".meta"
	if err := os.WriteFile(metaPath, []byte(contentType), 0644); err != nil {
		_ = os.Remove(fullAbs)
		return fmt.Errorf("failed to save metadata: %w", err)
	}

	return nil
}

func (d *LocalFSDriver) Get(ctx context.Context, key string) (io.ReadCloser, string, error) {
	fullAbs, err := d.resolveAndValidate(key)
	if err != nil {
		return nil, "", err
	}
	f, err := os.Open(fullAbs)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get file: %w", err)
	}

	metaPath := fullAbs + ".meta"
	contentType := DefaultMime
	if metaBytes, err := os.ReadFile(metaPath); err == nil {
		contentType = string(metaBytes)
	}

	return f, contentType, nil
}

func (d *LocalFSDriver) Delete(ctx context.Context, key string) error {
	fullAbs, err := d.resolveAndValidate(key)
	if err != nil {
		return err
	}
	_ = os.Remove(fullAbs + ".meta")
	if err := os.Remove(fullAbs); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete file: %w", err)
	}
	return nil
}

func (d *LocalFSDriver) GetDownloadURL(_ context.Context, key string) (string, error) {
	if d.PublicURL == "" {
		return key, nil
	}

	ttl := d.presignTTL
	expiresAt := time.Now().Add(ttl).Unix()
	token := GenerateDownloadToken(key, d.secretKey, expiresAt)

	// Returns a URL with security token and expiration
	v := url.Values{}
	v.Set("token", token)
	v.Set("expiresAt", strconv.FormatInt(expiresAt, 10))

	return fmt.Sprintf("%s/api/v1/storage/%s/content?%s", d.PublicURL, key, v.Encode()), nil
}

// VerifyDownloadToken checks if a provided download token is valid and not expired.
func (d *LocalFSDriver) VerifyDownloadToken(key, token string, expiresAt int64) bool {
	return VerifyDownloadToken(key, token, d.secretKey, expiresAt)
}

// GetUploadURL returns a presigned URL pointing to a local PUT handler.
// Note: This method does NOT create the file on disk. It only signs the security constraints
// (key, expiration, size limit). The actual resource allocation (file creation) happens in
// Save() when the PUT request is eventually processed, matching S3's deferred behavior.
func (d *LocalFSDriver) GetUploadURL(_ context.Context, key string, contentType string, maxSizeBytes int64) (string, error) {
	if d.PublicURL == "" {
		return "", fmt.Errorf("public URL not configured for local storage")
	}

	ttl := d.presignTTL
	expiresAt := time.Now().Add(ttl).Unix()
	token := GenerateToken(key, d.secretKey, expiresAt, contentType, maxSizeBytes)

	// Returns a URL pointing back to our local PUT handler with security constraints encoded
	v := url.Values{}
	v.Set("token", token)
	v.Set("expiresAt", strconv.FormatInt(expiresAt, 10))
	v.Set("contentType", contentType)
	v.Set("maxSizeBytes", strconv.FormatInt(maxSizeBytes, 10))

	return fmt.Sprintf("%s/api/v1/storage/%s/content?%s",
		d.PublicURL, key, v.Encode()), nil
}

// VerifyToken checks if a token is valid for a given key and constraints using the driver's secret.
func (d *LocalFSDriver) VerifyToken(key, token string, expiresAt int64, contentType string, maxSizeBytes int64) bool {
	return VerifyToken(key, token, d.secretKey, expiresAt, contentType, maxSizeBytes)
}
