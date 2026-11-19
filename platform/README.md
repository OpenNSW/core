# platform

The application layer of a single window, built on core's libraries. A single window takes these packages as a set:
they share one principal (`authn`), one catalog of roles and clients (`catalog`) and one audit trail (`audit`), and
they wire HTTP routes and middleware.

## What belongs here

A package belongs in `platform` when it needs the platform's shared types or the server's shape: it imports
`platform/authn`, `platform/catalog` or `platform/audit`, or it registers routes or middleware.

A package that works without adopting those conventions is a library and gets its own module at the top of the
repository, or joins an existing one. Examples are `storage` (drivers and the upload service), `htmlgen` (HTML
rendering) and `refid` (reference numbers). A library never imports `platform`; `platform` imports libraries.

`platform/storage` and the top-level `storage` module show the split. The `storage` library stores files and
presigns URLs for any caller. `platform/storage` picks a backend from the `storage` library or a proxy onto another
service, serves files over HTTP, and issues file tokens whose holder is an `authn` principal.

## Packages

| Package | What it does |
|---|---|
| `authn` | The application's boundary onto `core/authn`: the principal of an authenticated request |
| `catalog` | Maps the logical names used in configuration onto a deployment's IdP roles and OAuth2 client IDs |
| `audit` | Structured audit events for security-relevant actions |
| `securitylog` | Security events worth alerting on |
| `systemlog` | A log line for every inbound HTTP request |
| `storage` | Serves a deployment's files from a `storage` library backend or a proxy onto another service |
| `storage/filetoken` | Seals a grant (resource, holder, scopes) into an opaque encrypted token, and opens it |
| `storage/fileaccess` | Decides who a file token is for, what it allows and how long it lasts |
| `payloadschema` | Checks step submissions against their JSON Schema |
| `taskauthz` | The shared vocabulary of task authorization |
