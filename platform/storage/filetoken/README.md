# filetoken

Seals a grant into an opaque token, and opens it again. A grant names a resource, the holder allowed to use it and
the scopes saying what the holder may do. All three are opaque strings here: the caller decides what they mean.
[`fileaccess`](../fileaccess/README.md) uses this package to hand out tokens for stored files.

```go
keys, err := filetoken.ParseKeyset(cfg.TokenKeyset) // "kid2:<base64>,kid1:<base64>", newest first
codec, err := filetoken.NewCodec(keys, nil)         // nil clock means time.Now

token, err := codec.Seal(filetoken.Grant{
    Resource: "uploads/3f2a.pdf",
    Holder:   "alice", // whatever form the caller chooses
    Scopes:   []string{"read"},
}, time.Hour) // 0 means the token never expires

claims, err := codec.Open(token) // filetoken.ErrInvalid or filetoken.ErrExpired on failure
```

## Tokens

A token is a compact JWE (`dir` + `A256GCM`). Its claims are encrypted, and the GCM tag makes any change to them or to
the header detectable, so only a deployment holding the keyset can read or forge one. `Seal` requires at least one
scope. `Claims` carries the grant plus `IssuedAt` and `Expires`; `Expires` is zero for a token that never expires.

## Keys and rotation

`Keyset` is comma-separated `kid:key` pairs, newest first. Each key is 32 bytes in standard base64, such as the output
of `openssl rand -base64 32`. The first key seals every new token; any key in the set opens a token that names it.

To rotate, prepend a new key and keep the old ones for as long as their tokens must open. Tokens that never expire
need their key kept for good.
