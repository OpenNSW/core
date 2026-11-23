// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import (
	"context"
	"fmt"
	"mime"

	corestorage "github.com/OpenNSW/core/storage"

	"github.com/OpenNSW/core/platform/authn"
)

// Uploader reserves a key for a file and returns the URL to upload it to.
// Service satisfies it, and so does what limits the types of an upload.
type Uploader interface {
	Upload(ctx context.Context, filename string, size int64, mime string) (*corestorage.FileMetadata, error)
}

// typePolicy decides whether the caller in ctx may upload a file of the type
// the upload names, as sent.
type typePolicy func(ctx context.Context, mimeType string) bool

// limitedUploader refuses an upload whose type the policy does not allow, with
// corestorage.ErrContentTypeNotAllowed, before next sees it.
type limitedUploader struct {
	next   Uploader
	allows typePolicy
}

func limitTypes(next Uploader, allows typePolicy) Uploader {
	return limitedUploader{next: next, allows: allows}
}

func (u limitedUploader) Upload(ctx context.Context, filename string, size int64, mimeType string) (*corestorage.FileMetadata, error) {
	if !u.allows(ctx, mimeType) {
		return nil, fmt.Errorf("%w: %q", corestorage.ErrContentTypeNotAllowed, mimeType)
	}
	return u.next.Upload(ctx, filename, size, mimeType)
}

// anyCaller allows exactly the given types, whoever calls, comparing the whole
// string as core/storage does.
func anyCaller(types []string) typePolicy {
	set := typeSet(types)
	return func(_ context.Context, mimeType string) bool {
		_, ok := set[mimeType]
		return ok
	}
}

// machineClient allows base to every caller, and to a machine client the extra
// types granted to its client ID as well. It compares the media type alone,
// without parameters and case, so "text/html; charset=utf-8" is text/html.
func machineClient(base []string, extra map[string][]string) typePolicy {
	allowed := typeSet(base)
	extras := make(map[string]map[string]struct{}, len(extra))
	for clientID, types := range extra {
		extras[clientID] = typeSet(types)
	}
	return func(ctx context.Context, mimeType string) bool {
		mt, ok := mediaType(mimeType)
		if !ok {
			return false
		}
		if _, ok := allowed[mt]; ok {
			return true
		}
		p, ok := authn.FromContext(ctx)
		if !ok || p.ClientID == "" {
			return false
		}
		_, ok = extras[p.ClientID][mt]
		return ok
	}
}

// newMachineUploads returns the Uploader behind the internal upload route:
// what users may upload (cfg.AllowedUploadTypes) plus the extra types each
// machine client is granted, by client ID. Prepare success and failure are
// recorded by FileHandler.serveUpload (system slog and PRESIGN_UPLOAD audit);
// a finished PUT is recorded by ConfirmUpload (CONFIRM_UPLOAD).
//
// It prepares uploads with a core Service of its own over the same driver and
// without an upload allowlist, because core compares the whole MIME string with
// its list and refuses a type that is both uploadable and opened inline, while a
// generated document is sent as "text/html; charset=utf-8" and listed in
// inlineTypes. The limit is applied here, on the media type. How a stored file is
// served is still decided by the deployment's primary Service from inlineTypes.
func newMachineUploads(driver corestorage.StorageDriver, cfg Config, extra map[string][]string) Uploader {
	svc := corestorage.NewService(driver,
		corestorage.WithMaxUploadSize(cfg.MaxUploadBytes),
		corestorage.WithUploadTypeExtensions(cfg.UploadTypeExtensions),
	)
	return limitTypes(svc, machineClient(cfg.AllowedUploadTypes, extra))
}

// typeSet returns the set of the given media types.
func typeSet(types []string) map[string]struct{} {
	set := make(map[string]struct{}, len(types))
	for _, t := range types {
		set[t] = struct{}{}
	}
	return set
}

// mediaType returns the lower-case type/subtype of a Content-Type value, without
// its parameters. It reports false for a value that names no media type.
func mediaType(contentType string) (string, bool) {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil || mt == "" {
		return "", false
	}
	return mt, true
}
