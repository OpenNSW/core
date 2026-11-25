# Releases

## Where release artifacts are

OpenNSW Core publishes **Go module versions as git tags** on
[github.com/OpenNSW/core](https://github.com/OpenNSW/core). There is no separate
changelog file in this repository today.

Tag shape:

| Kind | Example | Module path |
| --- | --- | --- |
| Nested module | `httputil/v0.2.0` | `github.com/OpenNSW/core/httputil` |
| Nested module | `database/v0.3.0` | `github.com/OpenNSW/core/database` |
| Nested module | `temporal/v0.1.0` | `github.com/OpenNSW/core/temporal` |
| Root module | *(no `v*` tags on the root module at the time of writing)* | `github.com/OpenNSW/core` |

List tags:

```sh
git ls-remote --tags https://github.com/OpenNSW/core.git
```

Consume a tagged module:

```sh
go get github.com/OpenNSW/core/httputil@v0.2.0
```

Consume the root module from `main` (or a commit):

```sh
go get github.com/OpenNSW/core@main
```

[SECURITY.md](../SECURITY.md) notes that a supported-version matrix for security
updates is not defined yet; fixes land on `main` and on module tags when
maintainers cut them.

## How a release is cut

Maintainers tag a module after merging the change to `main`, using the module
directory as the tag prefix (for example `httputil/v0.2.0`). Downstream apps then
`go get` that tag. If you need a precise process for a new module version, ask
in a pull request or GitHub issue — this repo does not yet document an automated
release workflow beyond CI on `main` and manual tags.

## Downstream apps picking up upstream fixes

A downstream Go module that depends on these packages upgrades by changing its
`go.mod` (and running `go mod tidy`), for example:

```sh
go get github.com/OpenNSW/core@main
go get github.com/OpenNSW/core/httputil@v0.2.0
go mod tidy
```

Only commits that are **merged to `main`** (and tags that point at those
commits) appear in upstream releases. Patches that exist only on a fork are not
part of `go get` for `github.com/OpenNSW/core/...` until they are merged
upstream (or until you point `go.mod` at your fork with a `replace` or a
different module path).
