// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package drivers

import (
	"fmt"

	"github.com/OpenNSW/core/shared/validation"
)

// S3Config holds configuration for the S3 storage backend.
//
// S3Config carries yaml struct tags, so it can be embedded in a larger
// application config struct and populated generically (e.g. via
// yaml.Unmarshal).
type S3Config struct {
	// Endpoint is an optional custom endpoint URL for S3-compatible stores
	// (e.g. MinIO or LocalStack). Empty targets AWS S3.
	Endpoint string `yaml:"endpoint"`
	// Bucket is the S3 bucket that files are stored in. Required.
	Bucket string `yaml:"bucket"`
	// Region is the AWS region of the bucket. Required.
	Region string `yaml:"region"`
	// AccessKey and SecretKey are optional static credentials. They must be
	// set together; when both are empty the default AWS credential chain is
	// used.
	AccessKey string `yaml:"accessKey"`
	SecretKey string `yaml:"secretKey"`
	// PublicURL is an optional base URL files are served from, for a
	// CDN-fronted or otherwise rewritten bucket.
	PublicURL string `yaml:"publicURL"`
}

// Validate ensures the S3 storage configuration is usable.
func (c S3Config) Validate() error {
	if c.Bucket == "" {
		return fmt.Errorf("s3 storage: Bucket is required")
	}
	if c.Region == "" {
		return fmt.Errorf("s3 storage: Region is required")
	}
	if (c.AccessKey == "") != (c.SecretKey == "") {
		return fmt.Errorf("s3 storage: AccessKey and SecretKey must be configured together")
	}
	if c.Endpoint != "" {
		if err := validation.HTTPURL("s3 storage: Endpoint", c.Endpoint); err != nil {
			return err
		}
	}
	if c.PublicURL != "" {
		if err := validation.HTTPURL("s3 storage: PublicURL", c.PublicURL); err != nil {
			return err
		}
	}
	return nil
}
