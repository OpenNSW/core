// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package s3

import (
	"fmt"

	"github.com/OpenNSW/core/internal/validation"
)

// Config holds configuration for the S3 artifact loader.
//
// This is owned by the s3 package (mirroring temporal.Config and the local
// loader), so the package controls the shape and semantics of its own
// configuration. Client construction follows the same approach as the storage
// package: the default AWS credential chain unless static credentials are
// supplied, and an optional custom endpoint for S3-compatible stores.
type Config struct {
	// Bucket is the S3 bucket that artifact paths are read from. Required.
	Bucket string
	// Region is the AWS region of the bucket. Required.
	Region string
	// Endpoint is an optional custom endpoint URL for S3-compatible stores
	// (e.g. MinIO or LocalStack). When set, path-style addressing is used.
	// Empty targets AWS S3.
	Endpoint string
	// AccessKey and SecretKey are optional static credentials. They must be set
	// together; when both are empty the default AWS credential chain is used.
	AccessKey string
	SecretKey string
	// Prefix is an optional in-bucket key prefix that every path is resolved
	// against — the S3 analog of local.Root and github.BasePath. It lets one
	// bucket hold configs for several deployments (e.g. "deployment-a"), each
	// wired as its own loader + registry. Empty means the bucket root.
	Prefix string
}

// Validate ensures the S3 loader configuration is usable. It reports
// misconfiguration at construction time rather than deferring it to the first
// Load call.
func (c Config) Validate() error {
	if c.Bucket == "" {
		return fmt.Errorf("s3 loader: Bucket is required")
	}
	if c.Region == "" {
		return fmt.Errorf("s3 loader: Region is required")
	}
	if (c.AccessKey == "") != (c.SecretKey == "") {
		return fmt.Errorf("s3 loader: AccessKey and SecretKey must be set together")
	}
	if c.Endpoint != "" {
		if err := validation.HTTPURL("s3 loader: Endpoint", c.Endpoint); err != nil {
			return err
		}
	}
	return nil
}
