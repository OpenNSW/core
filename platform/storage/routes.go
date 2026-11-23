// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

// The storage API this application mounts. The routes are written out in
// full, like every other route in app.go, rather than assembled from parts;
// TestRoutes checks that each is a valid ServeMux pattern under RoutePrefix,
// that none conflicts with another or with the local content routes, and that
// each routes the requests it should.
const (
	// RoutePrefix is the path the storage API sits under. It is also given to
	// the local backend, so its content routes (RoutePrefix/{key}/content, for
	// the upload and download URLs it signs) sit beside the routes below
	// rather than wherever core/storage's default puts them. core/storage
	// validates it when the driver is built.
	RoutePrefix = "/api/v1/storage"

	// UploadRoute reserves a key and returns the URL to upload the file to.
	UploadRoute = "POST /api/v1/storage"
	// ConfirmUploadRoute records that the caller finished the presigned PUT
	// for the file reference in {key}. The PUT itself goes to the storage
	// backend (S3 or local content), so the API never sees it.
	ConfirmUploadRoute = "POST /api/v1/storage/{key}/confirm"
	// DownloadRoute returns a time-limited URL to download the file from.
	DownloadRoute = "GET /api/v1/storage/{key}"

	// InternalUploadRoute is UploadRoute for machine clients, which hold a
	// different scope and may upload more types than users. It sits outside
	// RoutePrefix: only TNSW serves it, and TestRoutes checks it mounts beside
	// the routes above.
	InternalUploadRoute = "POST /api/v1/internal/storage"
)
