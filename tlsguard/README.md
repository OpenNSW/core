# TLS Guard Package

`github.com/OpenNSW/core/tlsguard` gates the "skip TLS certificate verification"
escape hatch behind an explicit development-environment signal, so an
insecure-TLS flag left enabled can never silently ship to production.

## Contract

The single signal is the `APP_ENV` environment variable:

| `APP_ENV`                | Insecure TLS |
|:-------------------------|:-------------|
| `development` (any case) | allowed, with a prominent warning |
| unset / any other value  | refused (treated as production)   |

The default is **non-development**: unset — or any value other than
`development` — is treated as production and fails closed. This is deliberate so
that a deployment that forgets to unset an insecure flag refuses to start rather
than trusting a forged certificate.

## Usage

Call `Guard` only when an insecure-TLS flag is set, and wire its error into
startup so the process aborts before an insecure client is ever built:

```go
if cfg.InsecureSkipTLSVerify {
    if err := tlsguard.Guard("AUTH_JWKS_INSECURE_SKIP_VERIFY"); err != nil {
        return nil, err // fail closed outside APP_ENV=development
    }
    // ... build the insecure transport (development only) ...
}
```

`IsDevEnvironment()` exposes the same check as a boolean. `purpose` names the
flag/path and appears in both the warning log and the error message.
