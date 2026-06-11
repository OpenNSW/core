# Contributing

## Prerequisites

| Tool | Version | Install |
|---|---|---|
| Go | ≥ 1.26.4 | [go.dev/dl](https://go.dev/dl/) |
| git | ≥ 2.9 | system package manager |
| golangci-lint | v2.11.2 | `go install` (see below) |
| addlicense | latest | `go install` (see below) |
| gitleaks | latest | see [gitleaks install](#gitleaks) |

Ensure `$(go env GOPATH)/bin` is on your `PATH`.

## One-time setup

```sh
make setup
```

This installs `golangci-lint` and `addlicense`, then runs
`git config core.hooksPath .githooks` to register the git hooks.

**Manual equivalent:**

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.2
go install github.com/google/addlicense@latest
git config core.hooksPath .githooks
```

### gitleaks

| OS | Command |
|---|---|
| macOS | `brew install gitleaks` |
| Linux | `brew install gitleaks` or [binary release](https://github.com/gitleaks/gitleaks/releases) |
| Windows | `choco install gitleaks` or `scoop install gitleaks` |
| Any | Download binary from [github.com/gitleaks/gitleaks/releases](https://github.com/gitleaks/gitleaks/releases) |

## Pre-commit hook

The hook at `.githooks/pre-commit` runs the following checks against staged
changes on every `git commit`:

| Step | Tool | Behaviour |
|---|---|---|
| Secret scan | `gitleaks` | Hard gate — runs first, before any auto-fix |
| License headers | `addlicense` | **Auto-adds** missing SPDX headers, re-stages |
| Format | `golangci-lint fmt` | **Auto-formats** (gofmt + goimports), re-stages |
| Lint | `golangci-lint run` | Scoped to changed packages; blocks on errors |
| go mod tidy | `go mod tidy` | Fail-and-instruct if go.mod/go.sum are not tidy |
| Build | `go build ./...` | Blocks on compile errors |

> **Partial staging note:** if you `git add -p` only some hunks of a `.go`
> file, the auto-fixers re-stage the whole file. Stage whole files (or run
> `make fmt` first) to avoid pulling unstaged hunks into the commit.

## Pre-push hook

`go test -race ./...` runs on every `git push`. Bypass with `git push --no-verify`.

## Bypassing hooks

```sh
# Skip all hooks for one commit
git commit --no-verify

# Skip individual checks (comma-separated)
SKIP=lint git commit
SKIP=secrets,lint git commit

# Skip pre-push tests
git push --no-verify
```

Available `SKIP` tokens: `secrets`, `license`, `format`, `lint`, `mod`, `build`.

## Running checks manually

```sh
make check         # secrets + license-check + lint + tidy + build
make fmt           # auto-format
make lint          # lint only
make license       # add missing headers
make license-check # verify headers (as CI does)
make tidy          # verify go.mod/go.sum
make build         # build
make test          # go test -race ./...
make secrets       # gitleaks scan
make vuln          # govulncheck
```

## License header policy

Every `.go` file must carry these two lines at the very top:

```go
// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation
```

The year is stamped **once at file creation** and never bumped on edit — git
history is the authoritative record of edit dates. `addlicense` is idempotent:
it skips files that already have a header.

The canonical template lives at [.github/license-header.tmpl](.github/license-header.tmpl).

## Commit convention

We follow [Conventional Commits](https://www.conventionalcommits.org/):

| Prefix | When to use |
|---|---|
| `feat:` | New feature |
| `fix:` | Bug fix |
| `refactor:` | Code change that is not a feature or fix |
| `docs:` | Documentation only |
| `build:` | Build system / dependency changes |
| `ci:` | CI/CD changes |
| `chore:` | Maintenance (tooling, config, etc.) |
| `test:` | Test-only changes |

Use `scope` in parentheses for context: `feat(payment): add retry logic`.
Use `!` after the type for breaking changes: `feat!: rename Config struct`.

## CI overview

| Job | Gate | What it checks |
|---|---|---|
| Quality Gate | Blocking | go mod tidy, golangci-lint, license headers |
| Test & Security | Blocking (after Quality Gate) | go test -race, gosec (informational) |
| Vulnerability Scan | Blocking | govulncheck (call-graph reachability) |
| Secret Scan | Blocking | gitleaks dir scan |

CI enforces every gate independently of local hooks — `--no-verify` bypasses
hooks but not CI.
