// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package fileaccess decides who a file token is for, what it allows and how
// long it lasts.
//
// Callers outside the server never see a stored file's value, only a token
// for it. A token names its holder (a user by IdP subject, a machine client by
// client ID) and lists its scopes, the actions it allows:
//
//   - ScopeRead: open the file on the storage route.
//   - ScopeSubmit: send the token back as a file field's value in a task
//     submission, where it resolves to the stored value.
//
// A user's token that can read expires after the configured lifetime. One that
// can only be submitted opens nothing, so it never expires. A machine client's
// token never expires either, because an OGA keeps it in its own records for as
// long as it needs the file.
//
// Tokens are grants. Resolve checks only that the caller is the token's
// holder, that the token allows what the caller is doing and, if it expires,
// that it hasn't; it doesn't check again whether the caller may still see the
// file. So issue a token only to a caller already allowed what its scopes
// allow. A caller who loses that access keeps it until their tokens expire:
// for a user, after the configured lifetime; otherwise, once the key that
// sealed them is retired.
//
// A token ends with its resource's extension, after the sealed part
// ("<sealed>.pdf"), so a file's token can be told by its name as its storage
// key can: an agency holds TNSW's tokens, never its keys. The extension is
// outside the seal, so Resolve accepts a token only with exactly its
// resource's extension, or with none when the resource has none.
//
// filetoken seals and opens the tokens. This package only binds them to
// callers.
package fileaccess

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/OpenNSW/core/platform/authn"
	"github.com/OpenNSW/core/platform/storage/filetoken"
)

// Scope is an action a file token allows its holder.
type Scope string

const (
	// ScopeRead allows opening the file on the storage route.
	ScopeRead Scope = "read"
	// ScopeSubmit allows sending the token as a file field's value in a task
	// submission.
	ScopeSubmit Scope = "submit"
)

var (
	// ErrInvalid means the reference is not a file token this deployment
	// issued.
	ErrInvalid = errors.New("fileaccess: not a file token")
	// ErrNotYours means the token was issued to another caller.
	ErrNotYours = errors.New("fileaccess: file token issued to another caller")
	// ErrScope means the token was the caller's but doesn't allow what the
	// caller is doing with it.
	ErrScope = errors.New("fileaccess: file token doesn't allow this")
	// ErrExpired means the token was the caller's but has expired.
	ErrExpired = errors.New("fileaccess: file token expired")
)

// Access issues file tokens to callers and resolves them back to the
// resources they stand for.
type Access struct {
	codec   *filetoken.Codec
	userTTL time.Duration
}

// New returns an Access that seals with codec and gives users tokens that
// can read a lifetime of userTTL.
func New(codec *filetoken.Codec, userTTL time.Duration) (*Access, error) {
	if codec == nil {
		return nil, errors.New("fileaccess: codec is required")
	}
	if userTTL <= 0 {
		return nil, errors.New("fileaccess: user token lifetime must be positive")
	}
	return &Access{codec: codec, userTTL: userTTL}, nil
}

// UploadScopes returns the scopes of the token p is given for a file they
// upload. A user's can only be submitted, as the value of a form's file field:
// they open the file with the token the task's view gives once the form is
// saved, so it never expires. A machine client, such as an agency proxying its
// officer's upload, keeps the token and opens the file with it too.
func UploadScopes(p *authn.Principal) []Scope {
	if p != nil && p.Kind == authn.KindUser {
		return []Scope{ScopeSubmit}
	}
	return []Scope{ScopeRead, ScopeSubmit}
}

// Issue returns a token that lets p, the caller, do what scopes allow with
// resource.
func (a *Access) Issue(p *authn.Principal, resource string, scopes ...Scope) (string, error) {
	holder := holderOf(p)
	if holder == "" {
		return "", errors.New("fileaccess: the caller has no subject or client ID to issue a token to")
	}
	return a.issue(holder, p.Kind == authn.KindUser, resource, scopes)
}

// IssueForClient returns a token that lets the machine client clientID do
// what scopes allow with resource, when that client is not the caller. It is
// how a file is handed to an OGA: the push runs in a workflow step, with no
// request principal, and names its recipient from the catalog.
func (a *Access) IssueForClient(clientID, resource string, scopes ...Scope) (string, error) {
	if clientID == "" {
		return "", errors.New("fileaccess: client ID is required")
	}
	return a.issue(clientHolder(clientID), false, resource, scopes)
}

// issue seals a token for holder. A user's token that can read expires after
// the user lifetime; any other never does.
func (a *Access) issue(holder string, user bool, resource string, scopes []Scope) (string, error) {
	if len(scopes) == 0 {
		return "", errors.New("fileaccess: a token needs at least one scope")
	}
	names := make([]string, len(scopes))
	var lifetime time.Duration
	for i, scope := range scopes {
		names[i] = string(scope)
		if user && scope == ScopeRead {
			lifetime = a.userTTL
		}
	}
	token, err := a.codec.Seal(filetoken.Grant{Resource: resource, Holder: holder, Scopes: names}, lifetime)
	if err != nil {
		return "", fmt.Errorf("fileaccess: %w", err)
	}
	return token + extension(resource), nil
}

// Resolve returns the resource ref stands for, so that p can do what need
// allows with it, provided ref is a token issued to p, allows need and has not
// expired. It checks nothing else (see the package doc). A token issued before
// tokens carried scopes allows nothing, so it is ErrScope: refused, rather
// than taken for a value that is not a token. The checks run in
// that order: a token issued to someone else is ErrNotYours whatever it
// allows, and even once expired, so the caller is not told to fetch a fresh
// one it could never get. A token without its resource's extension, or with
// another, is ErrInvalid.
func (a *Access) Resolve(p *authn.Principal, ref string, need Scope) (string, error) {
	sealed, ext := ref, ""
	// A compact JWE has five parts (RFC 7516, section 7.1), so a sixth is the
	// extension.
	if strings.Count(ref, ".") == 5 {
		i := strings.LastIndex(ref, ".")
		sealed, ext = ref[:i], ref[i:]
	}
	claims, err := a.codec.Open(sealed)
	if (err != nil && !errors.Is(err, filetoken.ErrExpired)) || ext != extension(claims.Resource) {
		return "", ErrInvalid
	}
	if holder := holderOf(p); holder == "" || claims.Holder != holder {
		return "", ErrNotYours
	}
	if !slices.Contains(claims.Scopes, string(need)) {
		return "", ErrScope
	}
	if err != nil {
		return "", ErrExpired
	}
	return claims.Resource, nil
}

// fileExtension is an extension a token carries: letters and digits after a
// dot, as storage keeps them in a key.
var fileExtension = regexp.MustCompile(`^\.[A-Za-z0-9]+$`)

// extension returns resource's extension, with its dot, or "" when it has
// none.
func extension(resource string) string {
	if ext := path.Ext(resource); fileExtension.MatchString(ext) {
		return ext
	}
	return ""
}

// holderOf names p as a token holder, or returns "" when p has no identity a
// token can name. A user is named by the IdP subject rather than the persisted
// user ID, which is not resolved on every request.
func holderOf(p *authn.Principal) string {
	if p == nil {
		return ""
	}
	switch p.Kind {
	case authn.KindUser:
		if p.IDPUserID != "" {
			return userHolder(p.IDPUserID)
		}
	case authn.KindClient:
		if p.ClientID != "" {
			return clientHolder(p.ClientID)
		}
	}
	return ""
}

// The prefixes keep a user and a machine client with the same ID apart.
func userHolder(sub string) string        { return "u:" + sub }
func clientHolder(clientID string) string { return "c:" + clientID }
