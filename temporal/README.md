# temporal

Thin factory for creating a configured [Temporal](https://temporal.io/) client. Used by both the macro workflow runner and the taskflow micro-workflow subsystem.

## Usage

```go
import "github.com/OpenNSW/core/temporal"

client, err := temporal.NewClient(temporal.Config{
    Host:      "localhost",
    Port:      7233,
    Namespace: "default",
})
if err != nil {
    log.Fatal(err)
}
defer client.Close()
```

The returned `client.Client` is the standard Temporal Go SDK client. Pass it to `workflow.NewTemporalManager` and `taskflow/orchestrator.NewTaskManager`.

## Config

| Field       | YAML key    | Type         | Description                                             |
|-------------|-------------|--------------|----------------------------------------------------------|
| `Host`      | `host`      | `string`     | Temporal server hostname (required)                     |
| `Port`      | `port`      | `int`        | Temporal server port, e.g. `7233` (required)             |
| `Namespace` | `namespace` | `string`     | Temporal namespace, e.g. `"default"` (required)          |
| `TLS`       | `tls`       | `*TLSConfig` | Connect over TLS or mutual TLS (optional, see [TLS](#tls)) |
| `Encryption` | `encryption` | `*EncryptionConfig` | Encrypt payloads (optional, see [Payload encryption](#payload-encryption)) |

`Host`, `Port` and `Namespace` are required — `Validate()` rejects an empty `Host`/`Namespace` or a `Port` of `0`. There are no implicit defaults; callers must supply explicit values (e.g. `7233` and `"default"` for local development against `temporal server start-dev`).

`Config` carries `yaml` struct tags, so it can be embedded in a larger application config struct and populated generically (e.g. via `yaml.Unmarshal`):

```yaml
temporal:
  host: localhost
  port: 7233
  namespace: default
```

## TLS

Without a `tls` block the client connects to the Temporal server in plaintext. With one, it connects over TLS, and presents a client certificate when the block names one (mutual TLS):

```yaml
temporal:
  host: temporal.example.svc
  port: 7233
  namespace: default
  tls:
    caFile: /etc/temporal-client/ca.crt     # CA that signs the Temporal server's certificate
    certFile: /etc/temporal-client/tls.crt  # client certificate, for mutual TLS
    keyFile: /etc/temporal-client/tls.key   # client key, for mutual TLS
    serverName: temporal.example.svc        # name on the Temporal server's certificate
```

| Field        | YAML key     | Description                                                                         |
|--------------|--------------|--------------------------------------------------------------------------------------|
| `CAFile`     | `caFile`     | PEM CA certificates for the Temporal server's certificate. Empty: the system roots. |
| `CertFile`   | `certFile`   | PEM client certificate. Set together with `KeyFile`, or neither.                     |
| `KeyFile`    | `keyFile`    | PEM client key. Set together with `CertFile`, or neither.                            |
| `ServerName` | `serverName` | Name the Temporal server's certificate must carry. Empty: gRPC uses `Host`.          |

For TLS against a Temporal server whose certificate chains to the system roots, `tls: {}` is enough.

`host:port` is the server's Frontend service, the gRPC endpoint clients connect to, so these settings pair with the server's `tls.frontend` configuration.

`NewClient` reads the CA file and the client key pair once, at startup, and returns an error if a file is missing or malformed, or if the certificate and key don't match. A renewed certificate, key or CA file is used after the next restart.

## Payload encryption

Without an `encryption` block, payloads (workflow and activity inputs and results, signals, queries) are stored in Temporal as plain JSON. With one, the client and its workers encrypt every payload with AES-256-GCM before it leaves the process:

```yaml
temporal:
  encryption:
    keyId: k1
    key: "<base64 of 32 random bytes, e.g. openssl rand -base64 32>"
```

| Field   | YAML key | Description                                                        |
|---------|----------|--------------------------------------------------------------------|
| `KeyID` | `keyId`  | Name of the key, stored with each payload (required).              |
| `Key`   | `key`    | Base64-encoded 32-byte AES-256 key (required). Keep it in a secret. |

- Each payload is encrypted whole, metadata included, with a fresh nonce, and the key ID is authenticated with it.
- Decoding refuses a payload that isn't encrypted, was encrypted under another key, or was altered. A payload written to Temporal by anything without the key, such as a signal sent from the CLI, fails to decode.
- Failure messages and stack traces are encrypted too (the SDK's `EncodeCommonAttributes`), since error text can carry the same data.
- Every client and worker on a namespace must use the same key. Workflows started under one key can't be read under another.
- The Temporal UI and CLI show the encrypted payloads unless they are given a codec server with the key.
- Workflow and activity IDs, task queues, and search attributes are not payloads, so they stay readable.

## Local development

Run a local Temporal server with the [Temporal CLI](https://docs.temporal.io/cli/):

```sh
temporal server start-dev
```

This starts a server at `localhost:7233` in the `default` namespace with an in-memory store and an embedded UI at `http://localhost:8233`.
