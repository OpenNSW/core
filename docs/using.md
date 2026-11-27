# Using OpenNSW Core

This repository is an open Go **software toolkit**: importable modules for
building a National Single Window / paperless-trade (or similar) application.
It is **not** a deployable country product, and it does **not** require a
separate demo or sample application to use or to assess as open software.
Your application owns HTTP routes, configuration, migrations, and process
supervision.

## Modules

Most directories under this repository are **separate Go modules** (each has its
own `go.mod`). The root module `github.com/OpenNSW/core` contains only:

- [`artifact`](../artifact/README.md) — versioned configuration registry
- [`taskflow`](../taskflow/README.md) — human-in-the-loop task orchestration
- [`workflow`](../workflow/README.md) — JSON DAG interpreter on Temporal

Everything else is imported by its full module path, for example
`github.com/OpenNSW/core/httputil` or `github.com/OpenNSW/core/database`. The
package table in the [root README](../README.md) lists every package and links
to its README.

## Install into your application

From your application's module directory:

```sh
# Root module (artifact, taskflow, workflow) — pin main or a commit
go get github.com/OpenNSW/core@main

# Separately versioned modules — pin a released tag when one exists
go get github.com/OpenNSW/core/httputil@v0.2.0
go get github.com/OpenNSW/core/database@v0.3.0
go get github.com/OpenNSW/core/temporal@v0.1.0
```

List published tags for a module with:

```sh
git ls-remote --tags https://github.com/OpenNSW/core.git 'httputil/v*'
```

Go module tags use the directory prefix (`httputil/v0.2.0`). See
[Releases](releases.md).

## Supported way to assemble an application

1. Load config (often with [`configyaml`](../configyaml/README.md) and
   [`secret`](../secret/README.md)).
2. Open a database with [`database`](../database/README.md) when your stores need
   one.
3. Connect to Temporal with [`temporal`](../temporal/README.md) when you use
   `workflow` or `taskflow`.
4. Load artifacts, register task plugins, start macro and micro Temporal workers
   (`workflow`, `taskflow`).
5. Protect HTTP routes with [`authn`](../authn/README.md) / [`authz`](../authz/README.md),
   answer JSON with [`httputil`](../httputil/README.md), and optionally mount
   [`platform/storage`](../platform/storage/README.md) or payment webhooks.

A condensed wiring example lives in the [root README](../README.md#full-wiring-example).
Package READMEs document each package's API. Taskflow also has:

- [Integration guide](../taskflow/docs/integration-guide.md)
- [Frontend guide](../taskflow/docs/frontend-guide.md)
- [Plugin author guide](../taskflow/docs/plugin-author-guide.md)
- [Architecture](../taskflow/docs/architecture.md)

## External systems your application may need

| System | When you need it | Smallest local run |
| --- | --- | --- |
| Go toolchain | Always (see each module's `go` line in `go.mod`) | [go.dev/dl](https://go.dev/dl/) |
| Temporal server | Using `workflow` or `taskflow` workers | `temporal server start-dev` — Frontend `localhost:7233`, namespace `default`, UI `http://localhost:8233` ([`temporal` README](../temporal/README.md)) |
| PostgreSQL, MySQL, or SQLite | Using `database` or a GORM/SQL store in your app | SQLite `:memory:` or a file path for local work; Postgres/MySQL when you configure those drivers ([`database` README](../database/README.md)) |
| Object storage | Using `storage` in production-like setups | `storage.TypeLocal` for local files; S3-compatible endpoint optional ([`storage` README](../storage/README.md)) |
| OIDC/OAuth2 IdP | Using `authn` against real tokens | Any IdP that serves JWKS; configure `JWKSURL`, `Issuer`, `Audience` |

This repository does **not** ship a product Compose stack. Downstream apps that
import these modules provide their own deployable layout. For Digital Public
Goods documentation (Indicator 5), “launch and run the software” means install
the Go modules and run the documented tests and package flows in this repo—not
boot a national single-window product from here.

## Behaviour callers can rely on

- **Workflow** interprets a JSON graph (START, TASK, GATEWAY, SPLIT, END) as a
  Temporal workflow and activates tasks through callbacks you supply.
- **Taskflow** runs short Temporal workflows for interactive steps, loads step
  templates from the artifact registry, and completes back into the parent
  workflow.
- **Artifact** loads versioned definitions (workflows, forms, templates) by ID
  from a configured loader (local, GitHub, or S3).
- **Authn / authz** validate JWTs and enforce scopes without embedding your
  domain policy.
- **Httputil** writes JSON and correlation-tagged errors, and can decode JSON
  request bodies with a size limit.
- **Database / storage / payment / notification / remote** are factories and
  interfaces: you plug drivers, gateways, and providers; the packages do not
  hard-code a single vendor as mandatory.

Details and edge cases are in each package README and its tests.
