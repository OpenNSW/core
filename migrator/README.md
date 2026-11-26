# migrator

Applies versioned SQL migrations to PostgreSQL or SQLite from a directory of `.sql` files, and records each applied version in a `__migrations` table. The `.sql` files are the only source of the schema: nothing is generated from Go types.

It comes as a command, `migrate`, and as the library the command is built on.

## The migrate command

```sh
go install github.com/OpenNSW/core/migrator/cmd/migrate@latest
```

Pin a version in builds and images, so every environment applies migrations with the same migrator.

| Command           | What it does                                                                                        |
|-------------------|-----------------------------------------------------------------------------------------------------|
| `up`              | Applies every pending migration in version order, each in its own transaction                       |
| `down`            | Rolls back the most recently applied migration                                                      |
| `status`          | Prints each migration file with its version, name, `applied` or `pending`, and when it was applied  |
| `generate <name>` | Creates the next `<version>_<name>.sql` file with empty `@UP` and `@DOWN` blocks; needs no database |

### Configuration

The command reads the YAML file named by `CONFIG_PATH` (default `./config.yaml`), and uses only its `db` and `migrationDir` fields, so it can share an application's own config file. `db` has the shape of [`database.Config`](../database/README.md), and `{{env:NAME}}` / `{{file:/path}}` placeholders are resolved as [`configyaml`](../configyaml/README.md) resolves them.

```yaml
db:
  driver: postgres
  postgres:
    host: localhost
    port: 5432
    user: app
    password: "{{env:DB_PASSWORD}}"
    name: app_db
    sslMode: require
migrationDir: ./migrations
```

| Field                 | Default                    |
|-----------------------|----------------------------|
| `db.driver`           | `sqlite`                   |
| `db.sqlite.path`      | `./agency_applications.db` |
| `db.postgres.sslMode` | `require`                  |
| `migrationDir`        | `./migrations`             |

The drivers are `postgres` and `sqlite`; any other driver is refused. `generate` touches no database, so `CONFIG_PATH=/dev/null migrate generate <name>` works without a config file.

## Migration files

Each file is named `<version>_<name>.sql` (for example `000007_add_users_custom_data.sql`) and holds an `-- @UP` block and an optional `-- @DOWN` block:

```sql
-- @UP
ALTER TABLE users ADD COLUMN custom_data TEXT;

-- @DOWN
ALTER TABLE users DROP COLUMN custom_data;
```

`@UP` needs at least one statement. `@DOWN` is optional, so a migration can be irreversible. Versions must be unique and contiguous, with no gaps; `migrate generate <name>` picks the next version.

### Driver-specific SQL

Most migrations are portable SQL that runs on both drivers. When a statement only works on one, such as a PostgreSQL `USING GIN` index or a SQLite `PRAGMA`, nest a `-- @postgres` or `-- @sqlite` marker inside the `@UP` or `@DOWN` block it belongs to:

```sql
-- @UP
ALTER TABLE users ADD COLUMN custom_data JSONB;

-- @postgres
CREATE INDEX IF NOT EXISTS idx_users_custom_data ON users USING GIN (custom_data);

-- @DOWN
ALTER TABLE users DROP COLUMN custom_data;

-- @postgres
DROP INDEX IF EXISTS idx_users_custom_data;
```

- A driver marker applies to every following line until the next marker. A new `@UP` or `@DOWN` block always starts portable again.
- On `up`, the portable SQL runs first, then the current driver's. On `down`, the driver's SQL runs first, then the portable SQL, so an index added for one driver is dropped before the column it depends on. Write teardown SQL idempotently (`DROP INDEX IF EXISTS`), so it stays safe when a cascade already removed the object.
- A migration can consist only of driver-specific blocks: they count towards the statement `@UP` needs.
- Any other `--@` line, such as the typo `-- @postgress`, is a parse error. The `--@` prefix is reserved for these markers; other SQL comments are unaffected.
- Markers are matched ignoring case and spaces, so `--@up` and `-- @UP` are the same.

## The tracking table

`up`, `down` and `status` create `__migrations` if it does not exist. It holds one row per applied migration:

| Column       | Content                                  |
|--------------|------------------------------------------|
| `version`    | The file's version number, primary key   |
| `name`       | The file's name after the version        |
| `applied_at` | When the migration was applied, in UTC   |

`up` applies each file whose version has no row, and adds the row in the same transaction. `down` runs the `@DOWN` SQL of the highest applied version and deletes its row; it fails if that migration has no `@DOWN` SQL for the current driver.

## Using the library

```go
import (
    "github.com/OpenNSW/core/database"
    "github.com/OpenNSW/core/migrator"
    _ "github.com/jackc/pgx/v5/stdlib"
)

db, err := database.New(ctx, cfg) // any *sql.DB works
if err != nil {
    return err
}
defer db.Close()

m, err := migrator.New(db, "./migrations", "postgres") // or "sqlite"
if err != nil {
    return err
}
if err := m.Up(); err != nil {
    return err
}
```

`Up`, `Down`, `Status` and `Generate` print their progress to standard output. `Generate` needs no database, so `migrator.New(nil, dir, driver)` is enough for it.
