// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

// MockDriver implements StorageDriver for testing
type MockDriver struct {
	SavedKey       string
	SavedBody      []byte
	GenerateURLErr error
	GetErr         error
	DeleteErr      error
	DeleteCalled   bool
	DeleteKey      string
}

func (m *MockDriver) Save(ctx context.Context, key string, body io.Reader, contentType string) error {
	m.SavedKey = key
	content, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	m.SavedBody = content
	return nil
}

func (m *MockDriver) Get(ctx context.Context, key string) (io.ReadCloser, string, error) {
	if m.GetErr != nil {
		return nil, "", m.GetErr
	}
	return io.NopCloser(bytes.NewReader(m.SavedBody)), "application/test", nil
}

func (m *MockDriver) Delete(ctx context.Context, key string) error {
	m.DeleteCalled = true
	m.DeleteKey = key
	if m.DeleteErr != nil {
		return m.DeleteErr
	}
	return nil
}

func (m *MockDriver) GetDownloadURL(ctx context.Context, key string) (string, error) {
	if m.GenerateURLErr != nil {
		return "", m.GenerateURLErr
	}
	return "/test/download/" + key, nil
}

func (m *MockDriver) GetUploadURL(ctx context.Context, key string, contentType string, maxSizeBytes int64) (string, error) {
	if m.GenerateURLErr != nil {
		return "", m.GenerateURLErr
	}
	return "/test/upload/" + key, nil
}

type mockAuditor struct {
	events []AuditEvent
}

func (m *mockAuditor) AuditStorage(_ context.Context, e AuditEvent) {
	m.events = append(m.events, e)
}

func TestUploadService(t *testing.T) {
	mock := &MockDriver{}
	service := NewService(mock)

	ctx := context.Background()
	filename := "test.jpg"
	size := int64(1024)

	metadata, err := service.Upload(ctx, filename, size, "image/jpeg")
	if err != nil {
		t.Fatalf("Upload failed: %v", err)
	}

	if metadata.Name != filename {
		t.Errorf("expected name %s, got %s", filename, metadata.Name)
	}

	if metadata.Size != size {
		t.Errorf("expected size %d, got %d", size, metadata.Size)
	}

	if metadata.UploadURL != "/test/upload/"+metadata.Key {
		t.Errorf("unexpected upload URL: %s", metadata.UploadURL)
	}
}

func TestUpload_Audit_Success(t *testing.T) {
	mock := &MockDriver{}
	auditor := &mockAuditor{}
	service := NewService(mock)
	service.Auditor = auditor

	ctx := context.Background()
	filename := "test.jpg"
	size := int64(1024)
	mime := "image/jpeg"

	metadata, err := service.Upload(ctx, filename, size, mime)
	if err != nil {
		t.Fatalf("Upload failed: %v", err)
	}

	if len(auditor.events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(auditor.events))
	}
	ev := auditor.events[0]
	if ev.Action != AuditActionGetUploadURL || ev.Key != metadata.Key || ev.Filename != filename || ev.MimeType != mime || ev.Size != size || ev.Failure {
		t.Errorf("unexpected audit event: %+v", ev)
	}
}

func TestUpload_Audit_DriverError(t *testing.T) {
	driverErr := errors.New("driver presign failed")
	mock := &MockDriver{GenerateURLErr: driverErr}
	auditor := &mockAuditor{}
	service := NewService(mock)
	service.Auditor = auditor

	ctx := context.Background()
	_, err := service.Upload(ctx, "test.jpg", 1024, "image/jpeg")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if len(auditor.events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(auditor.events))
	}
	ev := auditor.events[0]
	if ev.Action != AuditActionGetUploadURL || !ev.Failure || ev.Error != driverErr.Error() {
		t.Errorf("unexpected audit event: %+v", ev)
	}
}

func TestUploadService_Download(t *testing.T) {
	mock := &MockDriver{
		SavedBody: []byte("test content"),
	}
	service := NewService(mock)

	ctx := context.Background()
	reader, contentType, err := service.Download(ctx, "test-key")
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}
	defer reader.Close()

	if contentType != "application/test" {
		t.Errorf("expected content type application/test, got %s", contentType)
	}

	content, _ := io.ReadAll(reader)
	if !bytes.Equal(content, mock.SavedBody) {
		t.Error("downloaded content does not match saved body")
	}
}

func TestDownload_Audit_Success(t *testing.T) {
	mock := &MockDriver{
		SavedBody: []byte("test content"),
	}
	auditor := &mockAuditor{}
	service := NewService(mock)
	service.Auditor = auditor

	ctx := context.Background()
	reader, _, err := service.Download(ctx, "test-key")
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}
	defer reader.Close()

	if len(auditor.events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(auditor.events))
	}
	ev := auditor.events[0]
	if ev.Action != AuditActionDownload || ev.Key != "test-key" || ev.MimeType != "application/test" || ev.Failure {
		t.Errorf("unexpected audit event: %+v", ev)
	}
}

func TestDownload_Audit_DriverError(t *testing.T) {
	driverErr := errors.New("driver read failed")
	mock := &MockDriver{GetErr: driverErr}
	auditor := &mockAuditor{}
	service := NewService(mock)
	service.Auditor = auditor

	ctx := context.Background()
	_, _, err := service.Download(ctx, "test-key")
	if !errors.Is(err, driverErr) {
		t.Fatalf("expected error %v, got %v", driverErr, err)
	}

	if len(auditor.events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(auditor.events))
	}
	ev := auditor.events[0]
	if ev.Action != AuditActionDownload || ev.Key != "test-key" || !ev.Failure || ev.Error != driverErr.Error() {
		t.Errorf("unexpected audit event: %+v", ev)
	}
}

func TestUploadService_GetDownloadURL_Success(t *testing.T) {
	mock := &MockDriver{}
	service := NewService(mock)

	ctx := context.Background()
	const key = "test-key"

	url, err := service.GetDownloadURL(ctx, key)
	if err != nil {
		t.Fatalf("GetDownloadURL failed: %v", err)
	}

	if url != "/test/download/"+key {
		t.Errorf("unexpected URL: %s", url)
	}
}

func TestGetDownloadURL_Audit_Success(t *testing.T) {
	mock := &MockDriver{}
	auditor := &mockAuditor{}
	service := NewService(mock)
	service.Auditor = auditor

	ctx := context.Background()
	const key = "test-key"

	url, err := service.GetDownloadURL(ctx, key)
	if err != nil {
		t.Fatalf("GetDownloadURL failed: %v", err)
	}

	if url != "/test/download/"+key {
		t.Errorf("unexpected URL: %s", url)
	}

	if len(auditor.events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(auditor.events))
	}
	ev := auditor.events[0]
	if ev.Action != AuditActionDownload || ev.Key != key || ev.Failure {
		t.Errorf("unexpected audit event: %+v", ev)
	}
}

func TestUploadService_GetDownloadURL_Error(t *testing.T) {
	expectedErr := io.ErrUnexpectedEOF
	mock := &MockDriver{GenerateURLErr: expectedErr}
	service := NewService(mock)

	_, err := service.GetDownloadURL(context.Background(), "test-key")
	if err == nil {
		t.Fatal("expected error from GetDownloadURL, got nil")
	}
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestGetDownloadURL_Audit_DriverError(t *testing.T) {
	driverErr := errors.New("driver presign failed")
	mock := &MockDriver{GenerateURLErr: driverErr}
	auditor := &mockAuditor{}
	service := NewService(mock)
	service.Auditor = auditor

	ctx := context.Background()
	_, err := service.GetDownloadURL(ctx, "test-key")
	if !errors.Is(err, driverErr) {
		t.Fatalf("expected error %v, got %v", driverErr, err)
	}

	if len(auditor.events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(auditor.events))
	}
	ev := auditor.events[0]
	if ev.Action != AuditActionDownload || ev.Key != "test-key" || !ev.Failure || ev.Error != driverErr.Error() {
		t.Errorf("unexpected audit event: %+v", ev)
	}
}

func TestDelete_Audit_Success(t *testing.T) {
	mock := &MockDriver{}
	auditor := &mockAuditor{}
	service := NewService(mock)
	service.Auditor = auditor

	ctx := context.Background()
	const key = "test-key"

	if err := service.Delete(ctx, key); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if !mock.DeleteCalled || mock.DeleteKey != key {
		t.Errorf("expected Delete(%q), got called=%v key=%q", key, mock.DeleteCalled, mock.DeleteKey)
	}
	if len(auditor.events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(auditor.events))
	}
	ev := auditor.events[0]
	if ev.Action != AuditActionDelete || ev.Key != key || ev.Failure {
		t.Errorf("unexpected audit event: %+v", ev)
	}
}

func TestDelete_Audit_DriverError(t *testing.T) {
	driverErr := errors.New("driver delete failed")
	mock := &MockDriver{DeleteErr: driverErr}
	auditor := &mockAuditor{}
	service := NewService(mock)
	service.Auditor = auditor

	ctx := context.Background()
	err := service.Delete(ctx, "test-key")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, driverErr) {
		t.Fatalf("expected error %v, got %v", driverErr, err)
	}

	if len(auditor.events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(auditor.events))
	}
	ev := auditor.events[0]
	if ev.Action != AuditActionDelete || ev.Key != "test-key" || !ev.Failure || ev.Error != driverErr.Error() {
		t.Errorf("unexpected audit event: %+v", ev)
	}
}
