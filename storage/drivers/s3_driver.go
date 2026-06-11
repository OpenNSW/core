// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package drivers

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Driver implements StorageDriver for S3-compatible storage.
// Save streams via PutObject (request body is capped at 32MB by the HTTP handler).
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

// presignGet returns a presigned GET URL for the key; used by both GenerateURL and GetDownloadURL.
func (d *S3Driver) presignGet(ctx context.Context, key string) (string, error) {
	ttl := d.presignTTL
	presignedReq, err := d.PresignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(d.Bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("failed to presign URL: %w", err)
	}
	return presignedReq.URL, nil
}

func (d *S3Driver) GetDownloadURL(ctx context.Context, key string) (string, error) {
	u, err := d.presignGet(ctx, key)
	if err != nil {
		return "", err
	}
	return d.rewriteHost(u)
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
	u, err := d.presignPut(ctx, key, contentType, maxSizeBytes)
	if err != nil {
		return "", err
	}
	return d.rewriteHost(u)
}

// rewriteHost replaces the host (and scheme) of rawURL with the host from PublicURL.
// This lets the SDK sign against the real S3 endpoint while returning a browser-reachable
// proxy URL.
func (d *S3Driver) rewriteHost(rawURL string) (string, error) {
	if d.PublicURL == "" {
		return rawURL, nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse raw URL: %w", err)
	}
	pub, err := url.Parse(d.PublicURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse public URL: %w", err)
	}
	u.Scheme = pub.Scheme
	u.Host = pub.Host
	if pub.Path != "" {
		pubPath := pub.Path
		if pubPath[len(pubPath)-1] == '/' {
			pubPath = pubPath[:len(pubPath)-1]
		}
		u.Path = pubPath + u.Path
		if u.RawPath != "" {
			pubRaw := pub.RawPath
			if pubRaw == "" {
				pubRaw = pub.EscapedPath()
			}
			if pubRaw[len(pubRaw)-1] == '/' {
				pubRaw = pubRaw[:len(pubRaw)-1]
			}
			u.RawPath = pubRaw + u.RawPath
		}
	}
	return u.String(), nil
}
