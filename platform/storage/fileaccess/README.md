# fileaccess

Decides who a file token is for, what it allows and how long it lasts. Callers outside the server never see a stored
file's key, only a token for it. This package binds tokens to callers; [`filetoken`](../filetoken/README.md) seals
and opens them.

```go
access, err := fileaccess.New(codec, time.Hour) // a user's readable tokens last an hour

// Hand the caller a token for a file they may open.
token, err := access.Issue(principal, storageKey, fileaccess.ScopeRead)

// Hand a file to a machine client that isn't the caller, such as an agency.
token, err = access.IssueForClient("fcau", storageKey, fileaccess.ScopeRead, fileaccess.ScopeSubmit)

// Turn a token back into the storage key, for this caller and this action.
key, err := access.Resolve(principal, token, fileaccess.ScopeRead)
```

## Scopes

| Scope | Allows |
|---|---|
| `ScopeRead` | Opening the file on the storage download route |
| `ScopeSubmit` | Sending the token as a file field's value in a task submission, where it resolves to the stored key |

`UploadScopes(principal)` gives the scopes for a file the caller uploads. A user's upload token can only be submitted.
A machine client's can also read, because it keeps the token to open the file later.

## Holders and lifetimes

A token names its holder: a user by IdP subject, a machine client by client ID. `Resolve` refuses a token held by
someone else.

- A user's token that can read expires after the lifetime given to `New`.
- A user's token that can only be submitted opens nothing, so it never expires.
- A machine client's token never expires, because the client keeps it in its own records for as long as it needs the
  file.

Tokens are grants. `Resolve` checks only the holder, the scope and, if the token expires, the expiry. It doesn't check
again whether the caller may still see the file, so issue a token only to a caller already allowed what its scopes
allow. A caller who loses access keeps it until their tokens expire, or until the key that sealed them is retired.

## Extensions

A token ends with its file's extension, outside the sealed part (`<sealed>.pdf`), so a file can be recognised from its
token as from its key. `Resolve` accepts a token only with exactly its resource's extension, or with none when the
resource has none.

## Errors

| Error | Meaning |
|---|---|
| `ErrInvalid` | Not a file token |
| `ErrNotYours` | Issued to another caller |
| `ErrScope` | Doesn't allow the action |
| `ErrExpired` | Past its lifetime |
