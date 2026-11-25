# Development Guide

Set up a local environment, run tests, and send changes upstream to
[OpenNSW/core](https://github.com/OpenNSW/core).

## Prerequisites

| Tool | Version / source | Required for |
| --- | --- | --- |
| [Go](https://go.dev/dl/) | Version in each module's `go.mod` (root module: `go 1.26.0`) | Build and test |
| [Git](https://git-scm.com/downloads) | ≥ 2.9 | Clone and pull requests |
| [Make](https://www.gnu.org/software/make/) | Any recent Make | `make setup`, `make test`, … |
| [golangci-lint](https://golangci-lint.run/) | **v2.11.2** (see `Makefile` `GOLANGCI_VERSION` and CI) | Lint / format |
| [gitleaks](https://github.com/gitleaks/gitleaks) | Latest release | Pre-commit secret scan |
| [addlicense](https://github.com/google/addlicense) | Latest (`go install`) | SPDX headers |

Optional:

| Tool | When you need it |
| --- | --- |
| [Temporal CLI](https://docs.temporal.io/cli/) | Optional integration tests that talk to a real Temporal server; local app wiring |
| Docker | Not required for `make test` in this repository |

Ensure `$(go env GOPATH)/bin` is on your `PATH` after installing Go tools.

## Install (clone) and one-time setup

Fork [OpenNSW/core](https://github.com/OpenNSW/core) on GitHub, then:

```sh
git clone https://github.com/YOUR_USERNAME/core.git
cd core
git remote add upstream https://github.com/OpenNSW/core.git
make setup
```

`make setup` installs `golangci-lint` **v2.11.2** and `addlicense`, and sets
`core.hooksPath` to `.githooks`.

Install gitleaks with your OS package manager or from
[gitleaks releases](https://github.com/gitleaks/gitleaks/releases) (see
[CONTRIBUTING.md](../../CONTRIBUTING.md)).

## Test

From the repository root, the same command CI uses for the root module:

```sh
make test
# equivalent:
go test -race ./...
```

That covers packages in the **root module** (`artifact`, `taskflow`, `workflow`).
Integration tests that need a live Temporal server are **skipped** unless you set
`TEMPORAL_TEST_ADDRESS` (for example `localhost:7233` after
`temporal server start-dev`). See
`workflow/complete_activation_integration_test.go`.

Nested modules are not in the root `./...` pattern. Test one by entering its
directory (as CI jobs do):

```sh
(cd database && go test -race ./...)
(cd temporal && go test -race ./...)
(cd httputil && go test -race ./...)
(cd platform && go test -race ./...)
```

SQLite-backed unit tests use in-memory or pure-Go drivers; they do not require a
running Postgres or MySQL. Postgres/MySQL matter when **your application**
configures those drivers via [`database`](../../database/README.md).

Other checks used before a pull request:

```sh
make build
make lint
make check   # secrets + license-check + lint + tidy + build (no tests)
```

## Fork, patch, and send a change upstream

Upstream for this project is a pull request against **`main`** on
`github.com/OpenNSW/core`.

```sh
git fetch upstream
git checkout main
git merge --ff-only upstream/main
git checkout -b feature/your-feature-name
# or: fix/issue-number
# edit, then:
make test
git add -A
git commit   # Conventional Commits; see CONTRIBUTING.md
git push -u origin HEAD
```

Open a pull request from your branch into `OpenNSW/core` `main`. CI must pass
(Quality Gate, Test & Security, per-module jobs, secret and vulnerability scans).

## Keeping a fork current

```sh
git fetch upstream
git checkout main
git merge --ff-only upstream/main
git push origin main
```

Rebase or merge `main` into your feature branch before the pull request if
`main` moved.

## How a downstream project gets upstream fixes

A product or country application that **requires** these modules upgrades its
own `go.mod`:

```sh
go get github.com/OpenNSW/core@main
go get github.com/OpenNSW/core/httputil@v0.2.0   # example tagged module
go mod tidy
```

Fixes exist for `go get github.com/OpenNSW/core/...` only after they are
**merged to upstream `main`** (and tagged, when the module uses tags). Commits
that live only on your fork are not in upstream module versions until merged; to
use an unmerged fork you must `replace` the module path in your app's `go.mod`
or change the module path. See [Releases](../releases.md).

## Deploy (running what these packages need)

This repo does not deploy a single-window product. For how an application
imports the packages and which external systems they need (Temporal, databases,
storage, IdP), see [Using OpenNSW Core](../using.md).

## Project layout (short)

```
core/
├── artifact/ taskflow/ workflow/   # root module github.com/OpenNSW/core
├── authn/ database/ httputil/ …    # nested modules github.com/OpenNSW/core/<dir>
├── platform/                       # application-layer module on top of libraries
├── docs/                           # this documentation
├── Makefile                        # setup, test, lint, …
└── .github/workflows/              # CI
```

## Getting help

- [Issues](https://github.com/OpenNSW/core/issues)
- [Reporting issues](reporting-issues.md)
- [CONTRIBUTING.md](../../CONTRIBUTING.md)
