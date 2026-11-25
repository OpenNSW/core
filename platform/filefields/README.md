# filefields

Reads which values in a task's data are stored files, as the task's render config declares them, and rewrites those
values. It is pure: it does no I/O and knows nothing of what the values are rewritten to.

A render config lists its file fields at the top level:

```json
"files": ["applicantform.invoice", "userform.documents[*].file"]
```

Each entry is a path into the task's data. A name steps into an object's field, and each `[*]` steps into every
element of an array, so arrays nest to any depth:

```text
path    = segment { "." segment }
segment = name { "[*]" }
name    = 1*( letter | digit | "_" | "-" )
```

The data must have the shape its declaration says. A field that holds a list of files is declared with `[*]`, and one
that holds one file without it. Where a path meets another shape, `Replace` fails rather than leave a file
unprotected.

```go
paths, err := filefields.Parse(renderConfig) // the render config's "files" list
err = filefields.Replace(data, paths, func(key string) (string, error) { // rewrites data in place: pass a copy
    return issueToken(key) // what each stored file's value becomes
})
```
