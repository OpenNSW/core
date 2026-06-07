package drivers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLocalFSDriver_DirectoryHashing(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "localfs-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	driver, err := NewLocalFSDriver(tempDir, "/uploads", "local-dev-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("failed to create driver: %v", err)
	}

	ctx := context.Background()
	key := "abcdef123456.pdf"
	content := []byte("test content")

	// Test Save
	err = driver.Save(ctx, key, bytes.NewReader(content), "application/pdf")
	if err != nil {
		t.Errorf("Save failed: %v", err)
	}

	// Verify Hashing: key "abcdef123456.pdf" should be at ab/cd/abcdef123456.pdf
	expectedSubPath := filepath.Join("ab", "cd", key)
	fullPath := filepath.Join(tempDir, expectedSubPath)

	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		t.Errorf("file not found at hashed path: %s", fullPath)
	}

	// Test Get
	reader, contentType, err := driver.Get(ctx, key)
	if err != nil {
		t.Errorf("Get failed: %v", err)
	}
	defer reader.Close()

	if contentType != "application/pdf" {
		t.Errorf("expected content type application/pdf, got %s", contentType)
	}

	// Verify GetDownloadURL: should be tokenized and include /uploads
	url, err := driver.GetDownloadURL(ctx, key)
	if err != nil {
		t.Errorf("GetDownloadURL failed: %v", err)
	}
	if !strings.Contains(url, "/uploads") || !strings.Contains(url, "token=") || !strings.Contains(url, "expiresAt=") {
		t.Errorf("unexpected URL format: %s", url)
	}

	// Test Delete
	err = driver.Delete(ctx, key)
	if err != nil {
		t.Errorf("Delete failed: %v", err)
	}

	if _, err := os.Stat(fullPath); !os.IsNotExist(err) {
		t.Error("file still exists after deletion")
	}
}

func TestLocalFSDriver_RejectsPathTraversal(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "localfs-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	driver, err := NewLocalFSDriver(tempDir, "/uploads", "local-dev-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("failed to create driver: %v", err)
	}

	ctx := context.Background()
	badKeys := []string{"../../../etc/passwd", "ab/cd/../../secret", "key\\with\\backslash"}

	for _, key := range badKeys {
		err = driver.Save(ctx, key, bytes.NewReader([]byte("x")), "text/plain")
		if err == nil {
			t.Errorf("Save with key %q should have failed", key)
		}
		_, _, err = driver.Get(ctx, key)
		if err == nil {
			t.Errorf("Get with key %q should have failed", key)
		}
		err = driver.Delete(ctx, key)
		if err == nil {
			t.Errorf("Delete with key %q should have failed", key)
		}
	}
}

// TestLocalFSDriver_ConcurrentWritesSameDir ensures concurrent Save to the same directory (same first 4 chars of key) does not race.
func TestLocalFSDriver_ConcurrentWritesSameDir(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "localfs-concurrent")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	driver, err := NewLocalFSDriver(tempDir, "/uploads", "local-dev-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("failed to create driver: %v", err)
	}

	ctx := context.Background()
	// Keys that hash to same first-level dir "ab": abxxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx.pdf and abyyyyyyyy-yyyy-yyyy-yyyy-yyyyyyyyyyyy.pdf
	key1 := "ab11111111-1111-1111-1111-111111111111.pdf"
	key2 := "ab22222222-2222-2222-2222-222222222222.pdf"

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		err := driver.Save(ctx, key1, bytes.NewReader([]byte("content1")), "application/pdf")
		if err != nil {
			t.Errorf("Save key1: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		err := driver.Save(ctx, key2, bytes.NewReader([]byte("content2")), "application/pdf")
		if err != nil {
			t.Errorf("Save key2: %v", err)
		}
	}()
	wg.Wait()

	// Both files must exist
	r1, ct1, err := driver.Get(ctx, key1)
	if err != nil {
		t.Fatalf("Get key1: %v", err)
	}
	defer r1.Close()
	if ct1 != "application/pdf" {
		t.Errorf("key1 content type: got %s", ct1)
	}
	r2, ct2, err := driver.Get(ctx, key2)
	if err != nil {
		t.Fatalf("Get key2: %v", err)
	}
	defer r2.Close()
	if ct2 != "application/pdf" {
		t.Errorf("key2 content type: got %s", ct2)
	}
}

// TestLocalFSDriver_RejectsKeyWithNullOrSpecialChars ensures keys with null bytes or invalid chars are rejected.
func TestLocalFSDriver_RejectsKeyWithNullOrSpecialChars(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "localfs-null")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	driver, err := NewLocalFSDriver(tempDir, "/uploads", "local-dev-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("failed to create driver: %v", err)
	}

	ctx := context.Background()
	badKeys := []string{
		"550e8400-e29b-41d4-a716-446655440000.pdf\x00",
		"key/with/slash",
		"key\\with\\backslash",
	}

	for _, key := range badKeys {
		err = driver.Save(ctx, key, bytes.NewReader([]byte("x")), "text/plain")
		if err == nil {
			t.Errorf("Save with key %q should have failed", key)
		}
	}
}

// TestLocalFSDriver_PathTraversal verifies that saving with a key that would escape the root is rejected.
func TestLocalFSDriver_PathTraversal(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "localfs-traversal")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	driver, err := NewLocalFSDriver(tempDir, "/uploads", "local-dev-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("failed to create driver: %v", err)
	}

	ctx := context.Background()
	err = driver.Save(ctx, "../traversal.txt", bytes.NewReader([]byte("content")), "text/plain")
	if err == nil {
		t.Fatal("Security Breach: Driver allowed saving a file outside the root directory")
	}
	if !errors.Is(err, ErrInvalidPath) {
		t.Errorf("validation error should wrap ErrInvalidPath for errors.Is: %v", err)
	}
}

// BenchmarkLocalFSDriver_Get benchmarks Get to ensure streaming (no full load into memory).
func BenchmarkLocalFSDriver_Get(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "localfs-bench")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	driver, err := NewLocalFSDriver(tempDir, "/uploads", "local-dev-secret", 15*time.Minute)
	if err != nil {
		b.Fatal(err)
	}

	ctx := context.Background()
	key := "aabbccdd-1234-5678-90ab-cdef00000000.bin"
	// 1MB payload
	payload := bytes.Repeat([]byte("x"), 1024*1024)
	err = driver.Save(ctx, key, bytes.NewReader(payload), DefaultMime)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r, _, err := driver.Get(ctx, key)
		if err != nil {
			b.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, r)
		r.Close()
	}
}

func TestLocalFSDriver_LinkVerification(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "localfs-link")
	defer os.RemoveAll(tempDir)

	secret := "test-secret"
	driver, _ := NewLocalFSDriver(tempDir, "/uploads", secret, 15*time.Minute)
	key := "test-file.pdf"

	// 1. Valid Link
	expiresAt := time.Now().Add(time.Hour).Unix()
	token := GenerateDownloadToken(key, secret, expiresAt)
	if !driver.VerifyDownloadToken(key, token, expiresAt) {
		t.Error("valid token failed verification")
	}

	// 2. Invalid Key
	if driver.VerifyDownloadToken("wrong-key.pdf", token, expiresAt) {
		t.Error("token verified for wrong key")
	}

	// 3. Modified Expiration
	if driver.VerifyDownloadToken(key, token, expiresAt+1) {
		t.Error("token verified despite modified expiration")
	}

	// 4. Invalid Signature
	if driver.VerifyDownloadToken(key, "invalid-token", expiresAt) {
		t.Error("invalid token signature was accepted")
	}
}
