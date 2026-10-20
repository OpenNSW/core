// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package drivers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Driver implements StorageDriver for S3-compatible storage.
// Save streams via PutObject with no size limit of its own; presigned uploads
// are limited by the ContentLength signed into the URL.
type S3Driver struct {
	Client        *s3.Client
	PresignClient *s3.PresignClient
	Bucket        string
	PublicURL     string // Optional: Base URL if files are public
	presignTTL    time.Duration
}

func NewS3Driver(client *s3.Client, bucket string, publicURL string, presignTTL time.Duration) *S3Driver {
	if presignTTL == 0 {
		presignTTL = DefaultPresignTTL
	}
	return &S3Driver{
		Client:        client,
		PresignClient: s3.NewPresignClient(client),
		Bucket:        bucket,
		PublicURL:     publicURL,
		presignTTL:    presignTTL,
	}
}

// PresignTTL reports how long the driver's presigned URLs stay valid.
func (d *S3Driver) PresignTTL() time.Duration {
	return d.presignTTL
}

func (d *S3Driver) Save(ctx context.Context, key string, content io.Reader, contentType string) error {
	_, err := d.Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(d.Bucket),
		Key:         aws.String(key),
		Body:        content,
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("failed to upload to S3: %w", err)
	}
	return nil
}

func (d *S3Driver) Get(ctx context.Context, key string) (io.ReadCloser, string, error) {
	resp, err := d.Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(d.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, "", fmt.Errorf("failed to get from S3: %w", err)
	}

	contentType := DefaultMime
	if resp.ContentType != nil {
		contentType = *resp.ContentType
	}

	return resp.Body, contentType, nil
}

func (d *S3Driver) Delete(ctx context.Context, key string) error {
	_, err := d.Client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(d.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("failed to delete from S3: %w", err)
	}
	return nil
}

// presignGet returns a presigned GET URL for the key.
//
// It looks the object up first. A file ForcesDownload names has the URL signed
// to come back as an attachment, so the browser saves it instead of rendering
// it on the bucket's origin. A file that is not there yet gets no URL at all:
// it could be uploaded afterwards and opened through a URL signed without
// that override.
func (d *S3Driver) presignGet(ctx context.Context, key string) (string, error) {
	head, err := d.Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(d.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var notFound *types.NotFound
		var noSuchKey *types.NoSuchKey
		if errors.As(err, &notFound) || errors.As(err, &noSuchKey) {
			return "", fmt.Errorf("%w: %s", ErrNotFound, key)
		}
		return "", fmt.Errorf("failed to look up %s in S3: %w", key, err)
	}

	input := &s3.GetObjectInput{
		Bucket: aws.String(d.Bucket),
		Key:    aws.String(key),
	}
	if ForcesDownload(aws.ToString(head.ContentType)) {
		input.ResponseContentDisposition = aws.String("attachment")
	}
	presignedReq, err := d.PresignClient.PresignGetObject(ctx, input, s3.WithPresignExpires(d.presignTTL))
	if err != nil {
		return "", fmt.Errorf("failed to presign URL: %w", err)
	}
	return presignedReq.URL, nil
}

// GetDownloadURL returns a presigned URL for downloading the file. It returns
// ErrNotFound for a file the bucket does not hold.
func (d *S3Driver) GetDownloadURL(ctx context.Context, key string) (string, error) {
	return d.presignGet(ctx, key)
}

// presignPut returns a presigned PUT URL for the key and constraints.
func (d *S3Driver) presignPut(ctx context.Context, key, contentType string, maxSizeBytes int64) (string, error) {
	ttl := d.presignTTL
	presignedReq, err := d.PresignClient.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(d.Bucket),
		Key:           aws.String(key),
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(maxSizeBytes),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("failed to presign upload URL: %w", err)
	}

	return presignedReq.URL, nil
}

func (d *S3Driver) GetUploadURL(ctx context.Context, key string, contentType string, maxSizeBytes int64) (string, error) {
	return d.presignPut(ctx, key, contentType, maxSizeBytes)
}
