// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package fileaccess_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/google/uuid"

	"github.com/OpenNSW/core/platform/authn"
	"github.com/OpenNSW/nsw-srilanka/internal/storage/fileaccess"
	"github.com/OpenNSW/nsw-srilanka/internal/storage/filetoken"
)

const userTTL = time.Hour

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// newAccess returns an Access over a one-key keyset filled with b, and the
// clock it runs on.
func newAccess(t *testing.T, b byte) (*fileaccess.Access, *clock) {
	t.Helper()
	c := &clock{t: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
	ks, err := filetoken.ParseKeyset("k1:" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32)))
	if err != nil {
		t.Fatalf("ParseKeyset: %v", err)
	}
	codec, err := filetoken.NewCodec(ks, c.now)
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}
	a, err := fileaccess.New(codec, userTTL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a, c
}

func user(sub string) *authn.Principal {
	return &authn.Principal{Kind: authn.KindUser, IDPUserID: sub}
}

func client(id string) *authn.Principal {
	return &authn.Principal{Kind: authn.KindClient, ClientID: id}
}

// read and submit are the scopes of a token a reader who can act is given.
var readSubmit = []fileaccess.Scope{fileaccess.ScopeRead, fileaccess.ScopeSubmit}

// issue issues a token that can read and submit.
func issue(t *testing.T, a *fileaccess.Access, p *authn.Principal, value string, scopes ...fileaccess.Scope) string {
	t.Helper()
	if len(scopes) == 0 {
		scopes = readSubmit
	}
	token, err := a.Issue(p, value, scopes...)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return token
}

// assertResolves checks ref resolves to want for reading.
func assertResolves(t *testing.T, a *fileaccess.Access, p *authn.Principal, ref, want string) {
	t.Helper()
	got, err := a.Resolve(p, ref, fileaccess.ScopeRead)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != want {
		t.Fatalf("Resolve = %q, want %q", got, want)
	}
}

// assertRefused checks ref is refused for reading, with want.
func assertRefused(t *testing.T, a *fileaccess.Access, p *authn.Principal, ref string, want error) {
	t.Helper()
	got, err := a.Resolve(p, ref, fileaccess.ScopeRead)
	if !errors.Is(err, want) {
		t.Fatalf("Resolve: err = %v, want %v", err, want)
	}
	if got != "" {
		t.Fatalf("Resolve returned %q alongside an error", got)
	}
}

func TestUserToken(t *testing.T) {
	a, c := newAccess(t, 1)
	token := issue(t, a, user("alice"), "traders/invoice.pdf")

	assertResolves(t, a, user("alice"), token, "traders/invoice.pdf")
	assertRefused(t, a, user("bob"), token, fileaccess.ErrNotYours)
	assertRefused(t, a, client("alice"), token, fileaccess.ErrNotYours)

	c.t = c.t.Add(userTTL)
	assertRefused(t, a, user("alice"), token, fileaccess.ErrExpired)
	// Someone else's token is not theirs, expired or not.
	assertRefused(t, a, user("bob"), token, fileaccess.ErrNotYours)
}

// The token names the IdP subject, so it resolves whether or not the user's
// profile has been resolved on that request.
func TestUserTokenNamesSubject(t *testing.T) {
	a, _ := newAccess(t, 1)
	token := issue(t, a, &authn.Principal{Kind: authn.KindUser, IDPUserID: "sub-1", UserID: "user-1"}, "v")

	assertResolves(t, a, &authn.Principal{Kind: authn.KindUser, IDPUserID: "sub-1"}, token, "v")
	assertRefused(t, a, &authn.Principal{Kind: authn.KindUser, IDPUserID: "sub-2", UserID: "user-1"}, token, fileaccess.ErrNotYours)
}

func TestClientTokenNeverExpires(t *testing.T) {
	a, c := newAccess(t, 1)
	fromUpload := issue(t, a, client("CDA_TO_NSW"), "consignments/cert.pdf")
	fromPush, err := a.IssueForClient("CDA_TO_NSW", "consignments/cert.pdf", fileaccess.ScopeRead)
	if err != nil {
		t.Fatalf("IssueForClient: %v", err)
	}

	c.t = c.t.AddDate(10, 0, 0)
	for _, token := range []string{fromUpload, fromPush} {
		assertResolves(t, a, client("CDA_TO_NSW"), token, "consignments/cert.pdf")
		assertRefused(t, a, client("EFC_TO_NSW"), token, fileaccess.ErrNotYours)
		assertRefused(t, a, user("CDA_TO_NSW"), token, fileaccess.ErrNotYours)
	}
}

func TestResolveRejectsNonTokens(t *testing.T) {
	a, _ := newAccess(t, 1)
	other, _ := newAccess(t, 2)
	fromAnotherDeployment := issue(t, other, user("alice"), "v")

	for _, ref := range []string{"", "traders/invoice.pdf", uuid.NewString(), fromAnotherDeployment} {
		assertRefused(t, a, user("alice"), ref, fileaccess.ErrInvalid)
	}
}

func TestCallerWithoutIdentity(t *testing.T) {
	a, _ := newAccess(t, 1)
	token := issue(t, a, user("alice"), "v")

	for name, p := range map[string]*authn.Principal{
		"nil":               nil,
		"user without sub":  {Kind: authn.KindUser, UserID: "alice"},
		"client without ID": {Kind: authn.KindClient},
		"unknown kind":      {IDPUserID: "alice", ClientID: "alice"},
	} {
		if _, err := a.Issue(p, "v", fileaccess.ScopeRead); err == nil {
			t.Errorf("%s: Issue succeeded", name)
		}
		if _, err := a.Resolve(p, token, fileaccess.ScopeRead); !errors.Is(err, fileaccess.ErrNotYours) {
			t.Errorf("%s: Resolve err = %v, want ErrNotYours", name, err)
		}
	}
}

func TestIssueRejects(t *testing.T) {
	a, _ := newAccess(t, 1)
	if _, err := a.Issue(user("alice"), "", fileaccess.ScopeRead); err == nil {
		t.Error("Issue with no resource succeeded")
	}
	if _, err := a.Issue(user("alice"), "v"); err == nil {
		t.Error("Issue with no scope succeeded")
	}
	if _, err := a.IssueForClient("", "v", fileaccess.ScopeRead); err == nil {
		t.Error("IssueForClient with no client ID succeeded")
	}
	if _, err := a.IssueForClient("CDA_TO_NSW", "v"); err == nil {
		t.Error("IssueForClient with no scope succeeded")
	}
}

func TestNewRejects(t *testing.T) {
	ks, err := filetoken.ParseKeyset("k1:" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	codec, err := filetoken.NewCodec(ks, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fileaccess.New(nil, userTTL); err == nil {
		t.Error("New without a codec succeeded")
	}
	for _, ttl := range []time.Duration{0, -time.Second} {
		if _, err := fileaccess.New(codec, ttl); err == nil {
			t.Errorf("New with user lifetime %v succeeded", ttl)
		}
	}
}

// A token allows only the scopes it was issued with, and the checks run
// holder, then scope, then expiry.
func TestScopes(t *testing.T) {
	a, c := newAccess(t, 1)
	readOnly := issue(t, a, user("alice"), "v", fileaccess.ScopeRead)
	submitOnly := issue(t, a, user("alice"), "v", fileaccess.ScopeSubmit)
	both := issue(t, a, user("alice"), "v")

	for name, tc := range map[string]struct {
		ref  string
		need fileaccess.Scope
		want error
	}{
		"read token, read":     {readOnly, fileaccess.ScopeRead, nil},
		"read token, submit":   {readOnly, fileaccess.ScopeSubmit, fileaccess.ErrScope},
		"submit token, submit": {submitOnly, fileaccess.ScopeSubmit, nil},
		"submit token, read":   {submitOnly, fileaccess.ScopeRead, fileaccess.ErrScope},
		"read+submit, read":    {both, fileaccess.ScopeRead, nil},
		"read+submit, submit":  {both, fileaccess.ScopeSubmit, nil},
		"a scope never issued": {both, fileaccess.Scope("delete"), fileaccess.ErrScope},
	} {
		got, err := a.Resolve(user("alice"), tc.ref, tc.need)
		if !errors.Is(err, tc.want) || (err == nil && got != "v") {
			t.Errorf("%s: Resolve = %q, %v; want %v", name, got, err, tc.want)
		}
	}

	// Someone else's token is not theirs, whatever it allows.
	if _, err := a.Resolve(user("bob"), submitOnly, fileaccess.ScopeRead); !errors.Is(err, fileaccess.ErrNotYours) {
		t.Errorf("another user's token: err = %v, want ErrNotYours", err)
	}
	// An expired token that doesn't allow the action is refused for the scope,
	// so the caller isn't told to fetch a fresh one that wouldn't allow it either.
	c.t = c.t.Add(userTTL)
	if _, err := a.Resolve(user("alice"), readOnly, fileaccess.ScopeSubmit); !errors.Is(err, fileaccess.ErrScope) {
		t.Errorf("expired read token, submit: err = %v, want ErrScope", err)
	}
}

// A user's token that can read expires; one that can only be submitted opens
// nothing, so it never does.
func TestUserLifetimeFollowsScopes(t *testing.T) {
	a, c := newAccess(t, 1)
	readOnly := issue(t, a, user("alice"), "v", fileaccess.ScopeRead)
	both := issue(t, a, user("alice"), "v")
	submitOnly := issue(t, a, user("alice"), "v", fileaccess.ScopeSubmit)

	c.t = c.t.Add(userTTL)
	for name, ref := range map[string]string{"read": readOnly, "read+submit": both} {
		if _, err := a.Resolve(user("alice"), ref, fileaccess.ScopeRead); !errors.Is(err, fileaccess.ErrExpired) {
			t.Errorf("%s token after the lifetime: err = %v, want ErrExpired", name, err)
		}
	}
	c.t = c.t.AddDate(10, 0, 0)
	if got, err := a.Resolve(user("alice"), submitOnly, fileaccess.ScopeSubmit); err != nil || got != "v" {
		t.Errorf("submit token ten years on: Resolve = %q, %v; want the resource", got, err)
	}
}

// A token issued before tokens carried scopes, sealed with this deployment's
// key, allows nothing. It is refused as such, not taken for a value that is
// not a token, which a submission would store as sent.
func TestTokenWithoutScopes(t *testing.T) {
	a, c := newAccess(t, 1)
	e, err := jose.NewEncrypter(jose.A256GCM,
		jose.Recipient{Algorithm: jose.DIRECT, Key: bytes.Repeat([]byte{1}, 32), KeyID: "k1"},
		(&jose.EncrypterOptions{}).WithType("nsw-file+jwe"))
	if err != nil {
		t.Fatal(err)
	}
	obj, err := e.Encrypt([]byte(`{"res":"v","h":"u:alice","iat":1,"exp":` + fmt.Sprint(c.t.Add(time.Minute).Unix()) + `}`))
	if err != nil {
		t.Fatal(err)
	}
	token, err := obj.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}

	for _, need := range []fileaccess.Scope{fileaccess.ScopeRead, fileaccess.ScopeSubmit} {
		if _, err := a.Resolve(user("alice"), token, need); !errors.Is(err, fileaccess.ErrScope) {
			t.Errorf("%s: err = %v, want ErrScope", need, err)
		}
	}
	assertRefused(t, a, user("bob"), token, fileaccess.ErrNotYours)
}

// A token ends with its resource's extension, which Resolve requires exactly:
// it is outside the seal, so one added, removed or changed is refused.
func TestTokenCarriesExtension(t *testing.T) {
	a, _ := newAccess(t, 1)
	pdf := issue(t, a, user("alice"), "traders/invoice.pdf")
	none := issue(t, a, user("alice"), "traders/invoice")
	if !strings.HasSuffix(pdf, ".pdf") || strings.Count(pdf, ".") != 5 {
		t.Fatalf("token %q doesn't end with its file's extension", pdf)
	}
	if strings.Count(none, ".") != 4 {
		t.Fatalf("token %q carries an extension its file hasn't", none)
	}
	assertResolves(t, a, user("alice"), pdf, "traders/invoice.pdf")
	assertResolves(t, a, user("alice"), none, "traders/invoice")

	sealed := strings.TrimSuffix(pdf, ".pdf")
	for _, ref := range []string{sealed, sealed + ".xlsx", sealed + ".PDF", none + ".pdf"} {
		assertRefused(t, a, user("alice"), ref, fileaccess.ErrInvalid)
	}

	// A token over another deployment's token ends as that one does, so an
	// agency's token for a file TNSW holds carries the file's extension too.
	other, _ := newAccess(t, 2)
	agencys := issue(t, other, client("CDA_TO_NSW"), pdf)
	if !strings.HasSuffix(agencys, ".pdf") {
		t.Fatalf("token %q over %q doesn't end with .pdf", agencys, pdf)
	}
	got, err := other.Resolve(client("CDA_TO_NSW"), agencys, fileaccess.ScopeRead)
	if err != nil || got != pdf {
		t.Fatalf("Resolve = %q, %v; want %q", got, err, pdf)
	}
}

// A user's upload token can only be submitted, and never expires; a machine
// client's opens the file too.
func TestUploadScopes(t *testing.T) {
	a, c := newAccess(t, 1)
	fromUser := issue(t, a, user("alice"), "v", fileaccess.UploadScopes(user("alice"))...)
	fromClient := issue(t, a, client("CDA_TO_NSW"), "v", fileaccess.UploadScopes(client("CDA_TO_NSW"))...)

	c.t = c.t.AddDate(10, 0, 0)
	if got, err := a.Resolve(user("alice"), fromUser, fileaccess.ScopeSubmit); err != nil || got != "v" {
		t.Errorf("user's upload token, submit: Resolve = %q, %v; want the resource", got, err)
	}
	assertRefused(t, a, user("alice"), fromUser, fileaccess.ErrScope)
	assertResolves(t, a, client("CDA_TO_NSW"), fromClient, "v")
	if got, err := a.Resolve(client("CDA_TO_NSW"), fromClient, fileaccess.ScopeSubmit); err != nil || got != "v" {
		t.Errorf("client's upload token, submit: Resolve = %q, %v; want the resource", got, err)
	}
}
