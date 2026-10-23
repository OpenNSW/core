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

// Stat returns the type and size the file was stored with. It returns an error
// wrapping ErrNotFound for a file the bucket does not hold.
func (d *S3Driver) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	head, err := d.Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(d.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var notFound *types.NotFound
		if errors.As(err, &notFound) {
			return ObjectInfo{}, fmt.Errorf("%w: %s", ErrNotFound, key)
		}
		return ObjectInfo{}, fmt.Errorf("failed to look up %s in S3: %w", key, err)
	}
	return ObjectInfo{ContentType: aws.ToString(head.ContentType), Size: aws.ToInt64(head.ContentLength)}, nil
}

// GetDownloadURL returns a presigned URL for downloading the file with the
// given disposition. For DispositionAttachment it signs
// response-content-disposition=attachment into the URL, so the bucket answers
// with a header that makes a browser save the file instead of rendering it on
// the bucket's origin; for DispositionInline it signs no override, and the
// bucket serves the object as it was stored.
func (d *S3Driver) GetDownloadURL(ctx context.Context, key, disposition string) (string, error) {
	if !ValidDisposition(disposition) {
		return "", fmt.Errorf("unknown disposition %q", disposition)
	}
	input := &s3.GetObjectInput{
		Bucket: aws.String(d.Bucket),
		Key:    aws.String(key),
	}
	if disposition == DispositionAttachment {
		input.ResponseContentDisposition = aws.String(disposition)
	}
	presignedReq, err := d.PresignClient.PresignGetObject(ctx, input, s3.WithPresignExpires(d.presignTTL))
	if err != nil {
		return "", fmt.Errorf("failed to presign URL: %w", err)
	}
	return presignedReq.URL, nil
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
