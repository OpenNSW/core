# Shared Package

`github.com/OpenNSW/core/shared` holds small, dependency-free helpers used by
several other modules in this repo. It has no third-party dependencies.

## `deepcopy`

Recursive deep-copy helpers for JSON-shaped data — values decoded into
`map[string]any` / `[]any` trees. Copying such a tree lets a caller hand it to
other code (e.g. an extension or a background goroutine) without risking
mutation of, or data races on, the original.

```go
cp := deepcopy.Map(m)   // deep copy of a map[string]any
v := deepcopy.Value(x)  // deep copy of any JSON-shaped value
```

## `maputil`

Dot-path get/set helpers for nested `map[string]any` trees. `SetNestedKey`
deep-copies assigned values (via `deepcopy`) and merges maps instead of
overwriting them.

```go
v, ok := maputil.GetNestedKey(m, "userform.applicant_name")
maputil.SetNestedKey(m, "userform.applicant_name", "Acme")
```

## `validation`

Shared validation helpers for config structs.

```go
err := validation.TCPPort("Port", cfg.Port)     // 1-65535
err := validation.HTTPURL("Endpoint", cfg.URL)  // absolute http(s) URL
```
