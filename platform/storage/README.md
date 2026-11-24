# storage

The file storage a single window serves uploads and downloads from. It picks one backend from configuration and puts
the same interfaces in front of it, so the rest of the application never depends on which one is in use:

- **A backend of its own** from the [`storage`](../../storage/README.md) library: local disk or S3.
- **A proxy** onto another service that owns the files, reached through a [`remote`](../../remote/README.md) service.

Callers never see a stored file's key. An upload returns a file token issued to the caller, and a download resolves a
token back to the key for that caller. [`fileaccess`](fileaccess/README.md) issues and checks the tokens, and
[`filetoken`](filetoken/README.md) seals them.

## Usage

```go
stack, err := storage.New(ctx, cfg.Storage, remoteManager,
    storage.WithInternalUploadTypes(byClientID)) // allowedInternalUploadTypes, keyed by client ID

keys, err := filetoken.ParseKeyset(cfg.Storage.TokenKeyset)
codec, err := filetoken.NewCodec(keys, nil)
access, err := fileaccess.New(codec, cfg.Storage.TokenTTL())

files := storage.NewFileHandler(stack.Service, access,
    storage.WithUploads(stack.Uploads), storage.WithMachineUploads(stack.MachineUploads))

mux.Handle(storage.UploadRoute, withAuth(withScope("storage:write")(http.HandlerFunc(files.Upload))))
mux.Handle(storage.DownloadRoute, withAuth(withScope("storage:read")(http.HandlerFunc(files.Download))))
if files.HasMachineUploads() {
    mux.Handle(storage.InternalUploadRoute, withAuth(withScope("storage:internalwrite")(http.HandlerFunc(files.InternalUpload))))
}
if stack.LocalContent != nil { // local disk only: stands in for S3's upload and download URLs
    stack.LocalContent.RegisterRoutes(mux)
}
```

Authentication and scopes are the routes' middleware. `FileHandler` only reads the `platform/authn` principal the
middleware attached.

## The stack

`New` returns a `Stack`:

| Field | What it is |
|---|---|
| `Service` | The `Service` interface: `Upload` (reserve a key and get an upload URL), `Download`, `GetDownloadURL`, `DownloadURL`, `Delete`, and `Save` for content the server produces itself |
| `Handler` | The storage library's `HTTPHandler`, or `ProxyHandler` in proxy mode |
| `LocalContent` | The upload and download content routes that stand in for S3 on local disk; nil otherwise |
| `Uploads` | Prepares users' uploads: `Service`, limited to `proxy.allowedUploadTypes` in proxy mode |
| `MachineUploads` | Prepares machine clients' uploads on the internal route; nil in proxy mode |

## Routes

| Route | Handler | Returns |
|---|---|---|
| `POST /api/v1/storage` (`UploadRoute`) | `FileHandler.Upload` | A file token for the caller and the URL to upload the file to |
| `GET /api/v1/storage/{key}` (`DownloadRoute`) | `FileHandler.Download` | A time-limited download URL for the token in the path, when it expires, and the file's extension, MIME type and disposition |
| `POST /api/v1/internal/storage` (`InternalUploadRoute`) | `FileHandler.InternalUpload` | `Upload` for files a machine client generates, which may be of types users can't upload |

A user's upload token can only be submitted as a form's file field, so it never expires. A machine client's token can
also open the file.

An upload request is at most 1 MiB. By default it reads as it always has: a field it does not declare and anything
after the JSON value are ignored, and a request that can't be read is a 400. `WithStrictRequestBodies()` makes it read
as `httputil.DecodeJSON` does by default: those are a 400 that names an unknown field, and a request over the limit is
a 413.

## Configuration

`Config` inlines the storage library's settings (`type`, `local`, `s3`) and adds:

| Key | Meaning |
|---|---|
| `type: proxy` | Use a proxy onto another service instead of a backend of its own |
| `allowedUploadTypes` | MIME types clients may upload; anything else is refused with 415 |
| `allowedInternalUploadTypes` | Extra types each machine client may upload on the internal route, by its name in the catalog's clients. Optional |
| `uploadTypeExtensions` | The extensions a file of each type may end in, in order of preference. A name ending in another is refused with 400; a name with none gets the first |
| `inlineTypes` | Types a download opens in the browser; everything else is saved |
| `maxUploadBytes` | The largest upload accepted, in bytes |
| `proxy` | Proxy mode: the owning `service` (its ID in the `remote` services registry), its `uploadPath`, `internalUploadPath`, `downloadPath` and `deletePath` (the last two contain `{key}`), and the `allowedUploadTypes` this deployment's users may send |
| `tokenKeyset` | Required. The keys file tokens are sealed with: comma-separated `kid:key` pairs, newest first |
| `tokenTTLSeconds` | How long a user's readable token lasts. Defaults to 3600 |

The backend keys (`allowedUploadTypes`, `uploadTypeExtensions`, `inlineTypes`, `maxUploadBytes`) are required for `local` and `s3` and not read in proxy mode, where the owning service applies its own. `Config.Validate` checks the section as a whole.
