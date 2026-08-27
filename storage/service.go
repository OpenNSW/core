// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/OpenNSW/core/shared/audit"
	"github.com/OpenNSW/core/storage/drivers"
	"github.com/google/uuid"
)

// Service coordinates file storage operations and manages metadata
type Service struct {
	Driver  StorageDriver
	Auditor audit.Auditor // optional; nil means no auditing
}

func NewService(driver StorageDriver) *Service {
	return &Service{Driver: driver}
}

// WithAuditor injects an optional auditor for recording storage audit events.
// Passing nil disables auditing.
func (s *Service) WithAuditor(auditor audit.Auditor) {
	s.Auditor = auditor
}

// Upload handles the preparation of a file upload by generating a unique key
// and a presigned/upload URL via the storage driver.
func (s *Service) Upload(ctx context.Context, filename string, size int64, mime string) (*FileMetadata, error) {
	if mime == "" {
		mime = drivers.DefaultMime
	}
	id := uuid.NewString()
	ext := filepath.Ext(filename)
	key := fmt.Sprintf("%s%s", id, ext)

	// Generate a presigned URL for the upload
	uploadURL, err := s.Driver.GetUploadURL(ctx, key, mime, size)
	if err != nil {
		s.audit(ctx, eventTypePresignUpload, audit.ActionCreate, audit.StatusFailure, AuditDetails{
			Key: key, Filename: filename, MimeType: mime, Size: size, Error: err.Error(),
		})
		return nil, fmt.Errorf("failed to generate upload URL: %w", err)
	}

	metadata := &FileMetadata{
		ID:        id,
		Name:      filename,
		Key:       key,
		UploadURL: uploadURL,
		Size:      size,
		MimeType:  mime,
	}

	s.audit(ctx, eventTypePresignUpload, audit.ActionCreate, audit.StatusSuccess, AuditDetails{
		Key: key, Filename: filename, MimeType: mime, Size: size,
	})

	return metadata, nil
}

// Download retrieves the file content and its MIME type
func (s *Service) Download(ctx context.Context, key string) (io.ReadCloser, string, error) {
	rc, mime, err := s.Driver.Get(ctx, key)
	if err != nil {
		s.audit(ctx, eventTypeStorage, audit.ActionRead, audit.StatusFailure, AuditDetails{
			Key: key, Error: err.Error(),
		})
		return nil, "", err
	}
	s.audit(ctx, eventTypeStorage, audit.ActionRead, audit.StatusSuccess, AuditDetails{Key: key, MimeType: mime})
	return rc, mime, nil
}

// GetDownloadURL generates a time-limited or presigned URL for the given key
func (s *Service) GetDownloadURL(ctx context.Context, key string) (string, error) {
	url, err := s.Driver.GetDownloadURL(ctx, key)
	if err != nil {
		s.audit(ctx, eventTypeStorage, audit.ActionRead, audit.StatusFailure, AuditDetails{
			Key: key, Error: err.Error(),
		})
		return "", err
	}
	s.audit(ctx, eventTypeStorage, audit.ActionRead, audit.StatusSuccess, AuditDetails{Key: key})
	return url, nil
}

// Delete removes a file from storage
func (s *Service) Delete(ctx context.Context, key string) error {
	err := s.Driver.Delete(ctx, key)
	if err != nil {
		s.audit(ctx, eventTypeStorage, audit.ActionDelete, audit.StatusFailure, AuditDetails{
			Key: key, Error: err.Error(),
		})
		return fmt.Errorf("failed to delete file: %w", err)
	}
	s.audit(ctx, eventTypeStorage, audit.ActionDelete, audit.StatusSuccess, AuditDetails{Key: key})
	return nil
}

const (
	eventTypeStorage       = "STORAGE"
	eventTypePresignUpload = "PRESIGN_UPLOAD"
)

// audit is a nil-safe helper that emits an event only when an Auditor is set.
func (s *Service) audit(ctx context.Context, eventType string, action audit.Action, status audit.Status, d AuditDetails) {
	if s.Auditor == nil {
		return
	}
	s.Auditor.Audit(ctx, audit.Event{
		Timestamp:  time.Now().UTC(),
		EventType:  eventType,
		Action:     action,
		Status:     status,
		TargetType: "RESOURCE",
		TargetID:   d.Key,
		Details:    d,
	})
}
