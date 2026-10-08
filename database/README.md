# database

A small `database/sql` connection factory that handles connection setup, pool configuration, and health checks for PostgreSQL, MySQL, and SQLite. It returns a standard `*sql.DB`, so it works with any library built on `database/sql` (GORM, sqlc, sqlx, bun, ent, …).

## Quick start

```go
import (
    "context"

    "github.com/OpenNSW/core/database"
    _ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver
)

cfg := database.Config{
    Driver: database.Postgres,
    Postgres: &database.PostgresConfig{
        Host:     "localhost",
        Port:     5432,
        User:     "myuser",
        Password: "mypassword",
        Name:     "mydb",
        SSLMode:  "disable",
        Pool:     database.PoolConfig{MaxOpenConns: 25},
    },
}

ctx := context.Background()
db, err := database.New(ctx, cfg)
if err != nil {
    log.Fatal(err)
}
defer db.Close()
```

The same configuration in YAML:

```yaml
database:
  driver: postgres
  postgres:
    host: localhost
    port: 5432
    user: myuser
    password: mypassword
    name: mydb
    sslMode: disable
    pool:
      maxOpenConns: 25
```

## Drivers

`Config.Driver` selects which block is used. Blocks for other drivers are ignored, so a config file can hold several and switch between them by changing `driver`.

The package does not import any database driver, so you only pull in the one you use. Blank-import the driver that matches `Config.Driver`:

| `Driver`            | Block      | Import                                | Notes                          |
|---------------------|------------|---------------------------------------|--------------------------------|
| `database.Postgres` | `postgres` | `_ "github.com/jackc/pgx/v5/stdlib"`  |                                |
| `database.MySQL`    | `mysql`    | `_ "github.com/go-sql-driver/mysql"`  | `parseTime=true` is always set |
| `database.SQLite`   | `sqlite`   | `_ "modernc.org/sqlite"`              | Pure Go, no cgo                |

If the import is missing, `New` returns an error such as `sql: unknown driver "pgx" (forgotten import?)`.

### SQLite

```yaml
database:
  driver: sqlite
  sqlite:
    path: app.db # or ":memory:"
    pool:
      maxOpenConns: 1
```

SQLite allows one writer at a time; concurrent writes through a larger pool fail with `database is locked`. `maxOpenConns: 1` is usually the right choice. With `:memory:`, each connection gets its own database, so a single connection is also required to see the same data.

## Using with GORM

Wrap the returned `*sql.DB` with the GORM dialector for your driver. GORM-specific settings such as the logger and `NowFunc` are configured here:

```go
import (
    "time"

    "gorm.io/driver/postgres"
    "gorm.io/gorm"
    "gorm.io/gorm/logger"
)

sqlDB, err := database.New(ctx, cfg)
if err != nil {
    log.Fatal(err)
}

gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
    Logger:  logger.Default.LogMode(logger.Error),
    NowFunc: func() time.Time { return time.Now().UTC() },
})
if err != nil {
    log.Fatal(err)
}
```

For the other drivers use `mysql.New(mysql.Config{Conn: sqlDB})` (`gorm.io/driver/mysql`) or `sqlite.Dialector{Conn: sqlDB}` (`github.com/glebarez/sqlite`, which uses `modernc.org/sqlite`).

## Configuration

### `Config`

| Field      | YAML       | Type              | Description                                    |
|------------|------------|-------------------|------------------------------------------------|
| `Driver`   | `driver`   | `Driver`          | Required. `postgres`, `mysql`, or `sqlite`     |
| `Postgres` | `postgres` | `*PostgresConfig` | Required when `driver` is `postgres`           |
| `MySQL`    | `mysql`    | `*MySQLConfig`    | Required when `driver` is `mysql`              |
| `SQLite`   | `sqlite`   | `*SQLiteConfig`   | Required when `driver` is `sqlite`             |

### `PostgresConfig`

| Field      | YAML       | Type         | Required | Description                                                  |
|------------|------------|--------------|----------|--------------------------------------------------------------|
| `Host`     | `host`     | `string`     | ✅       | Database host                                                |
| `Port`     | `port`     | `int`        |          | Port number. Omit to use the driver default                  |
| `User`     | `user`     | `string`     | ✅       | Database user                                                |
| `Password` | `password` | `string`     | ✅       | Database password. Special characters are URL-encoded        |
| `Name`     | `name`     | `string`     | ✅       | Database name                                                |
| `SSLMode`  | `sslMode`  | `string`     |          | PostgreSQL SSL mode (`disable`, `require`, `verify-full`, …) |
| `Pool`     | `pool`     | `PoolConfig` |          | Connection pool settings                                     |

### `MySQLConfig`

| Field      | YAML       | Type         | Required | Description                                 |
|------------|------------|--------------|----------|---------------------------------------------|
| `Host`     | `host`     | `string`     | ✅       | Database host                               |
| `Port`     | `port`     | `int`        |          | Port number. Omit to use the driver default |
| `User`     | `user`     | `string`     | ✅       | Database user                               |
| `Password` | `password` | `string`     | ✅       | Database password                           |
| `Name`     | `name`     | `string`     | ✅       | Database name                               |
| `Pool`     | `pool`     | `PoolConfig` |          | Connection pool settings                    |

### `SQLiteConfig`

| Field  | YAML   | Type         | Required | Description                                    |
|--------|--------|--------------|----------|------------------------------------------------|
| `Path` | `path` | `string`     | ✅       | Database file path, or `:memory:`              |
| `Pool` | `pool` | `PoolConfig` |          | Connection pool settings                       |

### `PoolConfig`

| Field                    | YAML                     | Type  | Description                            |
|--------------------------|--------------------------|-------|----------------------------------------|
| `MaxIdleConns`           | `maxIdleConns`           | `int` | Maximum idle connections in the pool   |
| `MaxOpenConns`           | `maxOpenConns`           | `int` | Maximum open connections in the pool   |
| `MaxConnLifetimeSeconds` | `maxConnLifetimeSeconds` | `int` | Maximum connection lifetime in seconds |

Any pool field left at `0` is skipped so the `database/sql` default applies.

## API

### `New(ctx context.Context, cfg Config) (*sql.DB, error)`

Validates the config, opens a connection pool for the selected driver, applies its pool settings, and pings the server. Returns an error if any step fails. On success it logs a single `INFO` line through `log/slog`.

Close the pool with `db.Close()` when the application shuts down.

### `HealthCheck(ctx context.Context, db *sql.DB) error`

Pings the database and returns a non-nil error if the connection is unhealthy. Intended for use in a `/healthz` or `/readyz` HTTP handler:

```go
func readyzHandler(db *sql.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if err := database.HealthCheck(r.Context(), db); err != nil {
            http.Error(w, "database unavailable", http.StatusServiceUnavailable)
            return
        }
        w.WriteHeader(http.StatusOK)
    }
}
```

## Testing

Unit tests cover validation, driver-native PostgreSQL/MySQL DSN parsing, and connection strings for every driver, and exercise `New` and `HealthCheck` against an in-memory SQLite database, so no running server is needed. Run them from the module directory:

```bash
cd database && go test ./...
```

### PostgreSQL and MySQL integration tests

The integration tests exercise `New`, pool configuration, `HealthCheck`, and a real `SELECT 1` against PostgreSQL and MySQL. They are skipped unless their corresponding host variable is set, so normal unit-test runs remain self-contained.

Start local containers in the background:

```bash
docker run -d --rm --name open-nsw-postgres \
  -e POSTGRES_PASSWORD='p@ss:w/rd#?' \
  -e POSTGRES_DB=database_test \
  -p 5432:5432 \
  postgres:16-alpine

docker run -d --rm --name open-nsw-mysql \
  -e MYSQL_ROOT_PASSWORD='p@ss:w/rd#?' \
  -e MYSQL_DATABASE=database_test \
  -e MYSQL_USER=test \
  -e MYSQL_PASSWORD='p@ss:w/rd#?' \
  -p 3306:3306 \
  mysql:8.4
```

Wait until both servers are ready:

```bash
until docker exec open-nsw-postgres pg_isready -U postgres -d database_test >/dev/null 2>&1; do sleep 1; done
until docker exec open-nsw-mysql mysqladmin ping -h 127.0.0.1 -uroot -p'p@ss:w/rd#?' --silent >/dev/null 2>&1; do sleep 1; done
```

Set the integration-test connection variables in another shell:

```bash
export DATABASE_TEST_POSTGRES_HOST=127.0.0.1
export DATABASE_TEST_POSTGRES_PORT=5432
export DATABASE_TEST_POSTGRES_USER=postgres
export DATABASE_TEST_POSTGRES_PASSWORD='p@ss:w/rd#?'
export DATABASE_TEST_POSTGRES_NAME=database_test

export DATABASE_TEST_MYSQL_HOST=127.0.0.1
export DATABASE_TEST_MYSQL_PORT=3306
export DATABASE_TEST_MYSQL_USER=test
export DATABASE_TEST_MYSQL_PASSWORD='p@ss:w/rd#?'
export DATABASE_TEST_MYSQL_NAME=database_test
```

Then run:

```bash
cd database
go test -v -race ./...
```

The CI database-module job starts equivalent PostgreSQL 16 and MySQL 8.4 services and exports the same variables before running the module tests. Stop the local containers when finished:

```bash
docker rm -f open-nsw-postgres open-nsw-mysql
```
