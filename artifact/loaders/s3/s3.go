// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package s3 provides an artifact Loader that reads objects from an S3 bucket
// (or an S3-compatible store).
package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"

	"github.com/OpenNSW/core/artifact"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Loader reads artifact bytes from a single S3 bucket. It is safe for
// concurrent use: all fields are set at construction and never mutated.
type Loader struct {
	client *s3.Client
	bucket string
	prefix string
}

// New validates cfg, builds an S3 client from it, and constructs a Loader. It
// returns an error if the configuration is invalid or the AWS config fails to
// load, matching the temporal.NewClient(cfg) shape. It takes a context because
// AWS configuration loading is context-aware.
func New(ctx context.Context, cfg Config) (*Loader, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Region),
	}
	if cfg.AccessKey != "" && cfg.SecretKey != "" {
		creds := credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, "")
		opts = append(opts, awsconfig.WithCredentialsProvider(creds))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("s3 loader: load AWS config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
			o.UsePathStyle = true
		}
	})

	slog.Info("s3 artifact loader initialized", "bucket", cfg.Bucket, "region", cfg.Region)
	return &Loader{client: client, bucket: cfg.Bucket, prefix: strings.Trim(cfg.Prefix, "/")}, nil
}

func (l *Loader) Load(ctx context.Context, p string) ([]byte, error) {
	key, err := l.resolve(p)
	if err != nil {
		return nil, err
	}

	output, err := l.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(l.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if _, ok := errors.AsType[*types.NoSuchKey](err); ok {
			return nil, fmt.Errorf("%w: s3 object %s not found in bucket %s", artifact.ErrNotFound, key, l.bucket)
		}
		return nil, fmt.Errorf("s3 get object %s from bucket %s: %w", key, l.bucket, err)
	}
	defer func() { _ = output.Body.Close() }()

	data, err := io.ReadAll(output.Body)
	if err != nil {
		return nil, fmt.Errorf("read s3 object body %s from bucket %s: %w", key, l.bucket, err)
	}
	return data, nil
}

// resolve joins p onto the loader's Prefix and guards against a key that
// escapes it (e.g. via ".."), so one deployment's loader cannot read another's
// objects in a shared bucket.
func (l *Loader) resolve(p string) (string, error) {
	full := path.Join(l.prefix, p)
	if l.prefix == "" {
		if full == ".." || strings.HasPrefix(full, "../") {
			return "", fmt.Errorf("%w: key %q escapes bucket root", artifact.ErrNotFound, p)
		}
	} else if full != l.prefix && !strings.HasPrefix(full, l.prefix+"/") {
		return "", fmt.Errorf("%w: key %q escapes prefix %q", artifact.ErrNotFound, p, l.prefix)
	}
	if full == "" || full == "." {
		return "", fmt.Errorf("%w: empty key", artifact.ErrNotFound)
	}
	return full, nil
}
