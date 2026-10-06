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

Use `storage.TypeLocal` for development. It stores files under `Config.Local.BaseDir` and stands in for S3's presigned URLs with URLs of its own, signed by `Config.Local.PutSecret`. They point at `{RoutePrefix}/{key}/content` on `Config.Local.PublicURL`; the prefix defaults to `/api/v1/storage`. Serve those routes with `storage.LocalContentHandler` on the server at that origin:

```go
if local, ok := driver.(*drivers.LocalFSDriver); ok {
    storage.NewLocalContentHandler(local).RegisterRoutes(mux) // PUT and GET local.ContentPattern()
}
```

The routes need no auth middleware. Like an S3 presigned URL, each request has to carry the token the driver signed for that key.

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
url, err := svc.GetDownloadURL(ctx, fileKey)
// url — presigned GET URL, valid for PresignTTLSeconds
```

### Delete

```go
err := svc.Delete(ctx, fileKey)
```

## Implementing a custom driver

```go
type StorageDriver interface {
    Save(ctx context.Context, key string, body io.Reader, contentType string) error
    Get(ctx context.Context, key string) (io.ReadCloser, string, error)
    Delete(ctx context.Context, key string) error
    GetDownloadURL(ctx context.Context, key string) (string, error)
    GetUploadURL(ctx context.Context, key string, contentType string, maxSizeBytes int64) (string, error)
}
```

Register your driver by passing it directly to `storage.NewService(driver)`.

## Config reference

`Config.Type` selects the backend (`storage.TypeS3` / `storage.TypeLocal`); only the matching nested config is read. `PresignTTLSeconds` (top-level, required for both backends) controls how long a presigned upload/download URL stays valid.

### S3 (`Config.S3`, `drivers.S3Config`, `yaml:"s3"`)

| Field                     | YAML key                  | Description                                                                                                                                          |
|---------------------------|---------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Endpoint`                | `endpoint`                | Optional custom endpoint URL for S3-compatible stores (e.g. MinIO or LocalStack). Empty targets AWS S3                                               |
| `Bucket`                  | `bucket`                  | S3 bucket name                                                                                                                                       |
| `Region`                  | `region`                  | AWS region (e.g. `ap-southeast-2`)                                                                                                                   |
| `AccessKey` / `SecretKey` | `accessKey` / `secretKey` | Static credentials; must be set together. Empty uses the default AWS credential chain                                                                |
| `PublicURL`               | `publicURL`               | Optional base URL files are served from (e.g. a CDN in front of the bucket). Currently unused: download URLs are always presigned against the bucket |

### Local filesystem (`Config.Local`, `drivers.LocalConfig`, `yaml:"local"`)

| Field         | YAML key      | Description                                                                                                                         |
|---------------|---------------|-------------------------------------------------------------------------------------------------------------------------------------|
| `BaseDir`     | `baseDir`     | Directory to store files under (created if absent)                                                                                  |
| `PublicURL`   | `publicURL`   | Origin of the server that serves `LocalContentHandler` (e.g. `http://localhost:8080`)                                               |
| `RoutePrefix` | `routePrefix` | Path the content routes sit under, e.g. `/files`. Optional; defaults to `/api/v1/storage`. Routes are `{RoutePrefix}/{key}/content` |
| `PutSecret`   | `putSecret`   | Signs the driver's upload and download URLs                                                                                         |

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

### Upgrading: the local content routes have their own handler

`HTTPHandler.UploadContentLocal` and `HTTPHandler.DownloadContent` are gone. `storage.LocalContentHandler` serves them instead, on the path the local driver builds its URLs from:

```go
// Before
mux.HandleFunc("PUT /api/v1/storage/{key}/content", handler.UploadContentLocal)
mux.HandleFunc("GET /api/v1/storage/{key}/content", handler.DownloadContent)

// After
storage.NewLocalContentHandler(localDriver).RegisterRoutes(mux)
```

The routes stay at `/api/v1/storage/{key}/content` unless you set `Local.RoutePrefix`. `GetDownloadURL` on the local driver now returns an error when `PublicURL` is empty, as `GetUploadURL` already did. Before, it returned the bare key.
