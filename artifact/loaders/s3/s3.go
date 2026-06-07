package s3

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/OpenNSW/core/artifact"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type S3Loader struct {
	Client *s3.Client
	Bucket string
}

func New(client *s3.Client, bucket string) *S3Loader {
	return &S3Loader{
		Client: client,
		Bucket: bucket,
	}
}

func (l *S3Loader) Load(ctx context.Context, path string) ([]byte, error) {
	output, err := l.Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(l.Bucket),
		Key:    aws.String(path),
	})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, fmt.Errorf("%w: s3 object %s not found in bucket %s", artifact.ErrNotFound, path, l.Bucket)
		}
		return nil, fmt.Errorf("s3 get object %s from bucket %s: %w", path, l.Bucket, err)
	}
	defer func() { _ = output.Body.Close() }()

	data, err := io.ReadAll(output.Body)
	if err != nil {
		return nil, fmt.Errorf("read s3 object body %s from bucket %s: %w", path, l.Bucket, err)
	}
	return data, nil
}
