// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package storage

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/OpenNSW/core/storage/drivers"
)

// NewStorageFromConfig creates a storage instance based on the provided configuration.
func NewStorageFromConfig(ctx context.Context, cfg Config) (StorageDriver, error) {
	presignTTL := time.Duration(cfg.PresignTTLSeconds) * time.Second

	switch strings.TrimSpace(cfg.Type) {
	case TypeLocal:
		slog.InfoContext(ctx, "initializing local storage", "dir", cfg.Local.BaseDir)
		return drivers.NewLocalFSDriver(cfg.Local.BaseDir, cfg.Local.PublicURL, cfg.Local.PutSecret, presignTTL)
	case TypeS3:
		slog.InfoContext(ctx, "initializing S3 storage", "endpoint", cfg.S3.Endpoint, "bucket", cfg.S3.Bucket)

		opts := []func(*awsconfig.LoadOptions) error{
			awsconfig.WithRegion(cfg.S3.Region),
		}

		if cfg.S3.AccessKey != "" && cfg.S3.SecretKey != "" {
			creds := credentials.NewStaticCredentialsProvider(cfg.S3.AccessKey, cfg.S3.SecretKey, "")
			opts = append(opts, awsconfig.WithCredentialsProvider(creds))
		}

		awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
		if err != nil {
			return nil, fmt.Errorf("failed to load AWS config: %w", err)
		}

		client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
			if cfg.S3.Endpoint != "" {
				o.BaseEndpoint = aws.String(cfg.S3.Endpoint)
			}
			o.UsePathStyle = true
			// Allow uploads over HTTP (e.g. local MinIO) where TLS is unavailable.
			// Without this, the SDK may require a seekable stream to compute checksums
			// upfront if the reader is wrapped.
			o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenSupported
		})

		return drivers.NewS3Driver(client, cfg.S3.Bucket, cfg.S3.PublicURL, presignTTL), nil
	default:
		return nil, fmt.Errorf("unsupported storage type: %s", cfg.Type)
	}
}
