# storage

File storage abstraction with presigned URL support. Backends are pluggable — swap between local filesystem (development) and AWS S3 (production) without changing application code.

## Usage

```go
import (
    "github.com/OpenNSW/core/storage"
    "github.com/OpenNSW/core/storage/drivers"
)

driver, err := storage.NewStorageFromConfig(ctx, storage.Config{
    Type: storage.TypeS3,
    S3: drivers.S3Config{
        Bucket: "my-uploads",
        Region: "ap-southeast-2",
    },
    PresignTTLSeconds: 900,
})
svc := storage.NewService(driver)
```

Use `storage.TypeLocal` for development — stores files under `Config.Local.BaseDir`, served from `Config.Local.PublicURL`, with uploads signed by `Config.Local.PutSecret`.

`Config` embeds each driver's own config type (`drivers.LocalConfig`, `drivers.S3Config`) verbatim, rather than flattening every backend's settings into one struct — so each driver keeps ownership of its config shape and validation. All three carry `yaml` struct tags, so `Config` can be embedded in a larger application config struct and populated generically (e.g. via `yaml.Unmarshal`, or [`configyaml.LoadAndExpand`](../configyaml/README.md) for `{{env:}}`/`{{file:}}` secret placeholders):

```yaml
storage:
  type: s3
  s3:
    bucket: my-uploads
    region: ap-southeast-2
    accessKey: "{{env:AWS_ACCESS_KEY_ID}}"
    secretKey: "{{env:AWS_SECRET_ACCESS_KEY}}"
  presignTTLSeconds: 900
```

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

`Config.Type` selects the backend (`storage.TypeS3` / `storage.TypeLocal`); only the matching nested config is read. `PresignTTLSeconds` (top-level, required for both backends) controls how long a presigned upload/download URL stays valid.

### S3 (`Config.S3`, `drivers.S3Config`, `yaml:"s3"`)

| Field                     | YAML key                  | Description                                                                                                 |
|---------------------------|---------------------------|-------------------------------------------------------------------------------------------------------------|
| `Endpoint`                | `endpoint`                | Optional custom endpoint URL for S3-compatible stores (e.g. MinIO or LocalStack). Empty targets AWS S3      |
| `Bucket`                  | `bucket`                  | S3 bucket name                                                                                              |
| `Region`                  | `region`                  | AWS region (e.g. `ap-southeast-2`)                                                                          |
| `AccessKey` / `SecretKey` | `accessKey` / `secretKey` | Static credentials; must be set together. Empty uses the default AWS credential chain                       |
| `PublicURL`               | `publicURL`               | Optional base URL files are served from (e.g. a CDN in front of the bucket)                                 |

### Local filesystem (`Config.Local`, `drivers.LocalConfig`, `yaml:"local"`)

| Field       | YAML key    | Description                                        |
|-------------|-------------|----------------------------------------------------|
| `BaseDir`   | `baseDir`   | Directory to store files under (created if absent) |
| `PublicURL` | `publicURL` | Base URL files are served from                     |
| `PutSecret` | `putSecret` | Signs presigned upload URLs                        |

### Upgrading from the flattened `Config`

`Config` used to carry every backend's fields flattened at the top level. If you're updating existing construction code:

| Old field       | New field                            |
|-----------------|---------------------------------------|
| `LocalBaseDir`   | `Local.BaseDir`                      |
| `LocalPublicURL` | `Local.PublicURL`                    |
| `LocalPutSecret` | `Local.PutSecret`                    |
| `S3Endpoint`     | `S3.Endpoint`                        |
| `S3Bucket`       | `S3.Bucket`                          |
| `S3Region`       | `S3.Region`                          |
| `S3AccessKey`    | `S3.AccessKey`                       |
| `S3SecretKey`    | `S3.SecretKey`                       |
| `S3PublicURL`    | `S3.PublicURL`                       |
| `S3UseSSL`       | Removed — it was never read anywhere |
| `PresignTTL` (`time.Duration`) | `PresignTTLSeconds` (`int`, whole seconds) |
