// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

// FileMetadata represents the metadata of an uploaded file, enriched with ownership and workflow identifiers.
type FileMetadata struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Key       string         `json:"key"`
	URL       string         `json:"url,omitempty"`
	UploadURL string         `json:"upload_url,omitempty"`
	Size      int64          `json:"size"`
	MimeType  string         `json:"mime_type"`
	Ownership map[string]any `json:"ownership,omitempty"`
}
