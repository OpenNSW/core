package storage

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/OpenNSW/core/storage/drivers"
)

// NewStorageFromConfig creates a storage instance based on the provided configuration.
func NewStorageFromConfig(ctx context.Context, cfg Config) (StorageDriver, error) {
	switch strings.TrimSpace(cfg.Type) {
	case "local":
		slog.Info("Initializing local storage", "dir", cfg.LocalBaseDir)
		return drivers.NewLocalFSDriver(cfg.LocalBaseDir, cfg.LocalPublicURL, cfg.LocalPutSecret, cfg.PresignTTL)
	case "s3":
		slog.Info("Initializing S3 storage", "endpoint", cfg.S3Endpoint, "bucket", cfg.S3Bucket)

		opts := []func(*awsconfig.LoadOptions) error{
			awsconfig.WithRegion(cfg.S3Region),
		}

		if cfg.S3AccessKey != "" && cfg.S3SecretKey != "" {
			creds := credentials.NewStaticCredentialsProvider(cfg.S3AccessKey, cfg.S3SecretKey, "")
			opts = append(opts, awsconfig.WithCredentialsProvider(creds))
		}

		awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
		if err != nil {
			return nil, fmt.Errorf("failed to load AWS config: %w", err)
		}

		client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
			if cfg.S3Endpoint != "" {
				o.BaseEndpoint = aws.String(cfg.S3Endpoint)
			}
			o.UsePathStyle = true
			// Allow uploads over HTTP (e.g. local MinIO) where TLS is unavailable.
			// Without this, the SDK may require a seekable stream to compute checksums
			// upfront if the reader is wrapped.
			o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenSupported
		})

		return drivers.NewS3Driver(client, cfg.S3Bucket, cfg.S3PublicURL, cfg.PresignTTL), nil
	default:
		return nil, fmt.Errorf("unsupported storage type: %s", cfg.Type)
	}
}
