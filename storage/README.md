# storage

File storage abstraction with presigned URL support. Backends are pluggable — swap between local filesystem (development) and AWS S3 (production) without changing application code.

## Usage

```go
import (
    "github.com/OpenNSW/core/storage"
)

driver, err := storage.NewStorageFromConfig(ctx, storage.Config{
    Type: "s3",
    Options: map[string]string{
        "bucket": "my-uploads",
        "region": "ap-southeast-2",
    },
})
svc := storage.NewService(driver)
```

Use `"local"` as `Type` for development (stores files under `Options["base_dir"]`).

## Operations

### Upload (presigned)

```go
meta, err := svc.Upload(ctx, "passport.pdf", fileSize, "application/pdf")
// meta.Key       — opaque storage key; persist this to your database
// meta.UploadURL — presigned PUT URL; return to the client for direct upload
```

The client uploads directly to the storage backend — the file never passes through your application server.

### Download

```go
// Stream the file contents
content, mimeType, err := svc.Download(ctx, fileKey)

// Or get a presigned download URL for the client
meta, err := svc.GetDownloadURL(ctx, fileKey)
// meta.DownloadURL — presigned GET URL valid for a short window
```

### Delete

```go
err := svc.Delete(ctx, fileKey)
```

## Implementing a custom driver

```go
type StorageDriver interface {
    Save(ctx context.Context, key string, r io.Reader, size int64, mimeType string) error
    Get(ctx context.Context, key string) (io.ReadCloser, string, error)
    Delete(ctx context.Context, key string) error
    GetDownloadURL(ctx context.Context, key string) (string, error)
    GetUploadURL(ctx context.Context, key, mimeType string, size int64) (string, error)
}
```

Register your driver by passing it directly to `storage.NewService(driver)`.

## Config reference

### S3

| Option | Description |
|---|---|
| `bucket` | S3 bucket name |
| `region` | AWS region (e.g. `ap-southeast-2`) |
| `endpoint` | Custom endpoint URL (for MinIO or localstack) |
| `access_key_id` | AWS access key (falls back to environment / instance profile) |
| `secret_access_key` | AWS secret key |

### Local filesystem

| Option | Description |
|---|---|
| `base_dir` | Directory to store files under (created if absent) |
