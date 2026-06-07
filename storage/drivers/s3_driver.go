package drivers

import (
	"context"
	"fmt"
	"io"
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
