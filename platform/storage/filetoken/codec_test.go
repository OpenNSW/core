package filetoken_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/google/uuid"

	"github.com/OpenNSW/nsw-srilanka/internal/storage/filetoken"
)

// rawKey returns a 32-byte key filled with b, so tests can build keysets
// without key literals.
func rawKey(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

// entry returns a keyset entry for kid with rawKey(b).
func entry(kid string, b byte) string {
	return kid + ":" + base64.StdEncoding.EncodeToString(rawKey(b))
}

// clock is a settable test clock.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newClock() *clock { return &clock{t: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)} }

func newCodec(t *testing.T, keyset string, c *clock) *filetoken.Codec {
	t.Helper()
	ks, err := filetoken.ParseKeyset(keyset)
	if err != nil {
		t.Fatalf("ParseKeyset(%q): %v", keyset, err)
	}
	codec, err := filetoken.NewCodec(ks, c.now)
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}
	return codec
}

func seal(t *testing.T, codec *filetoken.Codec, value, holder string, lifetime time.Duration) string {
	t.Helper()
	token, err := codec.Seal(filetoken.Grant{Resource: value, Holder: holder, Scopes: []string{"read"}}, lifetime)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	return token
}

func TestSealOpenRoundTrip(t *testing.T) {
	c := newClock()
	codec := newCodec(t, entry("k1", 1), c)

	token := seal(t, codec, "traders/invoice.pdf", "u:alice", time.Hour)
	if strings.Contains(token, "invoice") || strings.Contains(token, "alice") {
		t.Fatalf("token %q exposes its claims", token)
	}

	got, err := codec.Open(token)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	want := filetoken.Claims{
		Grant:    filetoken.Grant{Resource: "traders/invoice.pdf", Holder: "u:alice", Scopes: []string{"read"}},
		IssuedAt: c.t,
		Expires:  c.t.Add(time.Hour),
	}
	if !got.IssuedAt.Equal(want.IssuedAt) || !got.Expires.Equal(want.Expires) ||
		got.Resource != want.Resource || got.Holder != want.Holder || !slices.Equal(got.Scopes, want.Scopes) {
		t.Errorf("Open = %+v, want %+v", got, want)
	}
}

// Scopes are sealed and opened like the rest of the grant, and opaque to the
// codec.
func TestSealOpenScopes(t *testing.T) {
	codec := newCodec(t, entry("k1", 1), newClock())
	token, err := codec.Seal(filetoken.Grant{Resource: "v", Holder: "u:alice", Scopes: []string{"read", "submit"}}, 0)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if strings.Contains(token, "submit") {
		t.Fatalf("token %q exposes its scopes", token)
	}
	got, err := codec.Open(token)
	if err != nil || !slices.Equal(got.Scopes, []string{"read", "submit"}) {
		t.Fatalf("Open = %+v, %v; want scopes [read submit]", got, err)
	}
}

func TestOpenExpiry(t *testing.T) {
	c := newClock()
	codec := newCodec(t, entry("k1", 1), c)
	token := seal(t, codec, "v", "u:alice", time.Hour)

	c.t = c.t.Add(time.Hour - time.Second)
	if _, err := codec.Open(token); err != nil {
		t.Fatalf("Open a second before expiry: %v", err)
	}

	c.t = c.t.Add(time.Second)
	got, err := codec.Open(token)
	if !errors.Is(err, filetoken.ErrExpired) {
		t.Fatalf("Open at expiry: err = %v, want ErrExpired", err)
	}
	if got.Holder != "u:alice" || got.Resource != "v" {
		t.Errorf("an expired token's claims = %+v, want them returned", got)
	}
}

func TestOpenNoExpiry(t *testing.T) {
	c := newClock()
	codec := newCodec(t, entry("k1", 1), c)
	token := seal(t, codec, "v", "c:CDA_TO_NSW", 0)

	c.t = c.t.AddDate(20, 0, 0)
	got, err := codec.Open(token)
	if err != nil {
		t.Fatalf("Open twenty years later: %v", err)
	}
	if !got.Expires.IsZero() {
		t.Errorf("Expires = %v, want zero", got.Expires)
	}
}

func TestKeyRotation(t *testing.T) {
	c := newClock()
	before := newCodec(t, entry("old", 1), c)
	after := newCodec(t, entry("new", 2)+", "+entry("old", 1), c)
	retired := newCodec(t, entry("new", 2), c)

	oldToken := seal(t, before, "v", "h", 0)
	newToken := seal(t, after, "v", "h", 0)

	if _, err := after.Open(oldToken); err != nil {
		t.Errorf("a token sealed before rotation does not open after it: %v", err)
	}
	if _, err := before.Open(newToken); !errors.Is(err, filetoken.ErrInvalid) {
		t.Errorf("the new key did not seal: the old keyset opened its token (err = %v)", err)
	}
	if _, err := retired.Open(oldToken); !errors.Is(err, filetoken.ErrInvalid) {
		t.Errorf("Open with the old key dropped: err = %v, want ErrInvalid", err)
	}
}

func TestOpenRejectsAnotherKeyset(t *testing.T) {
	c := newClock()
	token := seal(t, newCodec(t, entry("k1", 1), c), "v", "h", 0)

	// Same kid, different key: another deployment's token.
	if _, err := newCodec(t, entry("k1", 2), c).Open(token); !errors.Is(err, filetoken.ErrInvalid) {
		t.Errorf("err = %v, want ErrInvalid", err)
	}
}

func TestOpenRejectsTampering(t *testing.T) {
	c := newClock()
	codec := newCodec(t, entry("k1", 1), c)
	token := seal(t, codec, "v", "h", 0)

	parts := strings.Split(token, ".")
	for i, part := range parts {
		if part == "" {
			continue // "dir" has no encrypted key
		}
		altered := append([]string(nil), parts...)
		b := []byte(part)
		if b[len(b)/2] == 'A' {
			b[len(b)/2] = 'B'
		} else {
			b[len(b)/2] = 'A'
		}
		altered[i] = string(b)
		if _, err := codec.Open(strings.Join(altered, ".")); !errors.Is(err, filetoken.ErrInvalid) {
			t.Errorf("part %d altered: err = %v, want ErrInvalid", i, err)
		}
	}
}

// encrypt makes a compact JWE with the given algorithms and headers, to check
// Open refuses anything but its own kind of token.
func encrypt(t *testing.T, enc jose.ContentEncryption, rcpt jose.Recipient, opts *jose.EncrypterOptions, plaintext string) string {
	t.Helper()
	e, err := jose.NewEncrypter(enc, rcpt, opts)
	if err != nil {
		t.Fatalf("NewEncrypter: %v", err)
	}
	obj, err := e.Encrypt([]byte(plaintext))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	s, err := obj.CompactSerialize()
	if err != nil {
		t.Fatalf("CompactSerialize: %v", err)
	}
	return s
}

func TestOpenRejectsOtherJWEs(t *testing.T) {
	c := newClock()
	codec := newCodec(t, entry("k1", 1), c)
	claims := `{"res":"v","h":"h","scope":["read"],"iat":1}`
	typed := (&jose.EncrypterOptions{}).WithType("nsw-file+jwe")

	cases := map[string]string{
		"no typ": encrypt(t, jose.A256GCM,
			jose.Recipient{Algorithm: jose.DIRECT, Key: rawKey(1), KeyID: "k1"}, nil, claims),
		"another typ": encrypt(t, jose.A256GCM,
			jose.Recipient{Algorithm: jose.DIRECT, Key: rawKey(1), KeyID: "k1"},
			(&jose.EncrypterOptions{}).WithType("JWT"), claims),
		"key wrapping instead of dir": encrypt(t, jose.A256GCM,
			jose.Recipient{Algorithm: jose.A256KW, Key: rawKey(1), KeyID: "k1"}, typed, claims),
		"another content encryption": encrypt(t, jose.A128GCM,
			jose.Recipient{Algorithm: jose.DIRECT, Key: rawKey(1)[:16], KeyID: "k1"}, typed, claims),
		"no holder": encrypt(t, jose.A256GCM,
			jose.Recipient{Algorithm: jose.DIRECT, Key: rawKey(1), KeyID: "k1"}, typed, `{"res":"v","scope":["read"],"iat":1}`),
		"no resource": encrypt(t, jose.A256GCM,
			jose.Recipient{Algorithm: jose.DIRECT, Key: rawKey(1), KeyID: "k1"}, typed, `{"h":"h","scope":["read"],"iat":1}`),
		"an empty scope": encrypt(t, jose.A256GCM,
			jose.Recipient{Algorithm: jose.DIRECT, Key: rawKey(1), KeyID: "k1"}, typed, `{"res":"v","h":"h","scope":[""],"iat":1}`),
		"not JSON": encrypt(t, jose.A256GCM,
			jose.Recipient{Algorithm: jose.DIRECT, Key: rawKey(1), KeyID: "k1"}, typed, "v"),
		"unknown kid": encrypt(t, jose.A256GCM,
			jose.Recipient{Algorithm: jose.DIRECT, Key: rawKey(1), KeyID: "k2"}, typed, claims),
	}
	for name, token := range cases {
		if _, err := codec.Open(token); !errors.Is(err, filetoken.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}

	// The control: the same construction with every check met does open.
	ok := encrypt(t, jose.A256GCM, jose.Recipient{Algorithm: jose.DIRECT, Key: rawKey(1), KeyID: "k1"}, typed, claims)
	if _, err := codec.Open(ok); err != nil {
		t.Errorf("a well-formed token: %v", err)
	}
}

// A token sealed before tokens carried scopes still opens, with none, so the
// caller can refuse it as allowing nothing rather than as no token at all.
func TestOpenTokenWithoutScopes(t *testing.T) {
	codec := newCodec(t, entry("k1", 1), newClock())
	typed := (&jose.EncrypterOptions{}).WithType("nsw-file+jwe")
	for name, claims := range map[string]string{
		"no scope":         `{"res":"v","h":"h","iat":1}`,
		"empty scope list": `{"res":"v","h":"h","scope":[],"iat":1}`,
	} {
		token := encrypt(t, jose.A256GCM, jose.Recipient{Algorithm: jose.DIRECT, Key: rawKey(1), KeyID: "k1"}, typed, claims)
		got, err := codec.Open(token)
		if err != nil {
			t.Fatalf("%s: Open: %v", name, err)
		}
		if got.Resource != "v" || got.Holder != "h" || len(got.Scopes) != 0 {
			t.Errorf("%s: Open = %+v, want the grant with no scopes", name, got)
		}
	}
}

func TestOpenRejectsNonTokens(t *testing.T) {
	codec := newCodec(t, entry("k1", 1), newClock())
	for _, s := range []string{
		"",
		uuid.NewString(),
		"traders/invoice.pdf",
		"a.b.c.d.e",
		strings.Repeat("a", 3000),
	} {
		if _, err := codec.Open(s); !errors.Is(err, filetoken.ErrInvalid) {
			t.Errorf("Open(%.40q): err = %v, want ErrInvalid", s, err)
		}
	}
}

func TestSealRejects(t *testing.T) {
	codec := newCodec(t, entry("k1", 1), newClock())
	read := []string{"read"}
	cases := []struct {
		name     string
		grant    filetoken.Grant
		lifetime time.Duration
	}{
		{"no resource", filetoken.Grant{Holder: "h", Scopes: read}, 0},
		{"no holder", filetoken.Grant{Resource: "v", Scopes: read}, 0},
		{"no scope", filetoken.Grant{Resource: "v", Holder: "h"}, 0},
		{"an empty scope", filetoken.Grant{Resource: "v", Holder: "h", Scopes: []string{"read", ""}}, 0},
		{"negative lifetime", filetoken.Grant{Resource: "v", Holder: "h", Scopes: read}, -time.Second},
		{"a resource too long to open", filetoken.Grant{Resource: strings.Repeat("v", 2000), Holder: "h", Scopes: read}, 0},
	}
	for _, tc := range cases {
		if _, err := codec.Seal(tc.grant, tc.lifetime); err == nil {
			t.Errorf("%s: Seal succeeded", tc.name)
		}
	}
}

// A token may carry another deployment's token as its value: an agency seals
// the token TNSW gave it for one of its officers.
func TestSealNestedToken(t *testing.T) {
	c := newClock()
	tnsw := newCodec(t, entry("tnsw", 1), c)
	agency := newCodec(t, entry("cda", 2), c)

	inner := seal(t, tnsw, "consignments/"+strings.Repeat("x", 100)+".pdf", "c:CDA_TO_NSW", 0)
	outer := seal(t, agency, inner, "u:"+strings.Repeat("s", 64), time.Hour)

	got, err := agency.Open(outer)
	if err != nil {
		t.Fatalf("agency Open: %v", err)
	}
	if got.Resource != inner {
		t.Fatalf("agency Open resource = %q, want TNSW's token", got.Resource)
	}
	if _, err := tnsw.Open(got.Resource); err != nil {
		t.Errorf("TNSW Open of the unwrapped token: %v", err)
	}
}

func TestNewCodecRequiresKeyset(t *testing.T) {
	if _, err := filetoken.NewCodec(nil, nil); err == nil {
		t.Error("NewCodec(nil) succeeded")
	}
	if _, err := filetoken.NewCodec(&filetoken.Keyset{}, nil); err == nil {
		t.Error("NewCodec(empty keyset) succeeded")
	}
}
