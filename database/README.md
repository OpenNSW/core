# database

A thin wrapper around [GORM](https://gorm.io) and the PostgreSQL driver that handles connection setup, pool configuration, health checks, and operates with configurable query logging.

## Quick start

```go
cfg := database.Config{
    Host:     "localhost",
    Port:     5432,
    Username: "myuser",
    Password: "mypassword",
    Name:     "mydb",
    SSLMode:  "disable",
}

db, err := database.New(cfg)
if err != nil {
    log.Fatal(err)
}
defer database.Close(db)
```

`db` is a `*gorm.DB` ready to use for all GORM operations.

## Configuration

| Field | Type | Required | Description |
|---|---|---|---|
| `Host` | `string` | ✅ | Database host |
| `Port` | `int` | | Port number. Omit to use the driver default |
| `Username` | `string` | ✅ | Database user |
| `Password` | `string` | ✅ | Database password. Special characters are URL-encoded automatically |
| `Name` | `string` | ✅ | Database name |
| `SSLMode` | `string` | | PostgreSQL SSL mode (`disable`, `require`, `verify-full`, …) |
| `MaxIdleConns` | `int` | | Maximum idle connections in the pool |
| `MaxOpenConns` | `int` | | Maximum open connections in the pool |
| `MaxConnLifetimeSeconds` | `int` | | Maximum connection lifetime in seconds |
| `LogLevel` | `LogLevel` | | Query log verbosity (see below). Defaults to `LogError` |

### Log levels

Control how much GORM logs about the queries it runs.

| Constant | Behaviour |
|---|---|
| `database.LogSilent` | No output at all |
| `database.LogError` | Errors only **(default)** |
| `database.LogWarn` | Errors + slow queries |
| `database.LogInfo` | Every SQL statement — useful in development |

```go
// Development: log every query
cfg := database.Config{
    // ...
    LogLevel: database.LogInfo,
}

// Production: log errors only (or nothing)
cfg := database.Config{
    // ...
    LogLevel: database.LogError, // same as omitting the field
}
```

## API

### `New(cfg Config) (*gorm.DB, error)`

Validates the config, opens a connection, configures the pool, and pings the server. Returns an error if any step fails. On success it logs a single `INFO` line through `log/slog`.

### `Close(db *gorm.DB) error`

Closes the underlying `sql.DB`. Safe to call with a `nil` argument (no-op). Typically deferred in `main`:

```go
db, err := database.New(cfg)
// ...
defer func() {
    if err := database.Close(db); err != nil {
        log.Printf("closing database: %v", err)
    }
}()
```

### `HealthCheck(db *gorm.DB) error`

Pings the database and returns a non-nil error if the connection is unhealthy. Intended for use in a `/healthz` or `/readyz` HTTP handler:

```go
func readyzHandler(db *gorm.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if err := database.HealthCheck(db); err != nil {
            http.Error(w, "database unavailable", http.StatusServiceUnavailable)
            return
        }
        w.WriteHeader(http.StatusOK)
    }
}
```

## Connection pool tuning

```go
cfg := database.Config{
    // ...
    MaxIdleConns:           5,
    MaxOpenConns:           25,
    MaxConnLifetimeSeconds: 300, // 5 minutes
}
```

Any pool field left at `0` is skipped so the driver's own default applies.

## Testing

Unit tests cover `Config.Validate`, `Config.DSN`, `gormLogLevel` mapping, and the lifecycle functions (`New`, `Close`, `HealthCheck`) without requiring a running database. Run them with:

```bash
go test ./database/...
```

Integration tests that need a real PostgreSQL instance can be gated with a build tag or by checking an environment variable:

```go
func TestIntegration(t *testing.T) {
    dsn := os.Getenv("TEST_DATABASE_URL")
    if dsn == "" {
        t.Skip("TEST_DATABASE_URL not set")
    }
    // ...
}
```
