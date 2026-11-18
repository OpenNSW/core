// Package filetoken seals a grant into an opaque token, and opens such tokens
// again. A grant names a resource, the holder allowed to use it, and the
// scopes saying what the holder may do with it. All are opaque strings to
// this package: callers decide what they are, such as a stored file's key or
// "read".
//
// A token is a compact JWE ("dir" + A256GCM). Its claims are encrypted, and
// the GCM tag makes any change to them or to the header detectable, so only
// the deployment holding the keyset can read or forge one. The package does
// not know who a holder is or how long a token should last: callers decide
// both.
package filetoken

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// typ is the JWE "typ" header every token carries. Open requires it, so a JWE
// made for some other purpose never opens as a file token.
const typ = "nsw-file+jwe"

// maxTokenLength bounds what Open parses. A token carries one resource and
// one holder, so a real one is well under a kilobyte, even when the resource
// is itself a token from another deployment.
const maxTokenLength = 2048

var (
	// ErrInvalid means the token is not one this keyset sealed, or has been
	// altered.
	ErrInvalid = errors.New("filetoken: invalid token")
	// ErrExpired means the token is intact but past its expiry.
	ErrExpired = errors.New("filetoken: token expired")
)

// Grant is what a token grants.
type Grant struct {
	// Resource is what the token grants access to.
	Resource string
	// Holder names who may use the token, in whatever form the caller chose.
	Holder string
	// Scopes say what the holder may do with the resource, in whatever form
	// the caller chose. Seal requires at least one. A token sealed before
	// tokens carried scopes opens with none, so it should allow nothing.
	Scopes []string
}

// Claims is what a token carries: its grant, and when it was sealed and
// stops being usable.
type Claims struct {
	Grant
	// IssuedAt is when the token was sealed.
	IssuedAt time.Time
	// Expires is when the token stops being usable. It is zero for a token
	// that never expires.
	Expires time.Time
}

// payload is the encrypted JSON form of Claims. Times are Unix seconds.
type payload struct {
	Resource string   `json:"res"`
	Holder   string   `json:"h"`
	Scopes   []string `json:"scope"`
	IssuedAt int64    `json:"iat"`
	Expires  int64    `json:"exp,omitempty"`
}

// Codec seals and opens tokens with one keyset.
type Codec struct {
	keys *Keyset
	now  func() time.Time
}

// NewCodec returns a Codec that seals with the keyset's newest key. now is the
// clock tokens are stamped and checked against; nil means time.Now.
func NewCodec(keys *Keyset, now func() time.Time) (*Codec, error) {
	if keys == nil || keys.current == "" {
		return nil, errors.New("filetoken: keyset is empty")
	}
	if now == nil {
		now = time.Now
	}
	return &Codec{keys: keys, now: now}, nil
}

// Seal returns a token carrying g. lifetime is how long the token is usable
// for; zero means it never expires.
func (c *Codec) Seal(g Grant, lifetime time.Duration) (string, error) {
	if g.Resource == "" || g.Holder == "" {
		return "", errors.New("filetoken: resource and holder are required")
	}
	if !validScopes(g.Scopes) {
		return "", errors.New("filetoken: a grant needs at least one scope, and no empty scope")
	}
	if lifetime < 0 {
		return "", errors.New("filetoken: lifetime must not be negative")
	}
	now := c.now()
	p := payload{Resource: g.Resource, Holder: g.Holder, Scopes: g.Scopes, IssuedAt: now.Unix()}
	if lifetime > 0 {
		p.Expires = now.Add(lifetime).Unix()
	}
	plaintext, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("filetoken: encode claims: %w", err)
	}
	enc, err := jose.NewEncrypter(jose.A256GCM,
		jose.Recipient{Algorithm: jose.DIRECT, Key: c.keys.keys[c.keys.current], KeyID: c.keys.current},
		(&jose.EncrypterOptions{}).WithType(typ))
	if err != nil {
		return "", fmt.Errorf("filetoken: create encrypter: %w", err)
	}
	obj, err := enc.Encrypt(plaintext)
	if err != nil {
		return "", fmt.Errorf("filetoken: encrypt: %w", err)
	}
	token, err := obj.CompactSerialize()
	if err != nil {
		return "", fmt.Errorf("filetoken: serialize: %w", err)
	}
	if len(token) > maxTokenLength {
		return "", fmt.Errorf("filetoken: resource too long: the token would be %d bytes, Open accepts at most %d", len(token), maxTokenLength)
	}
	return token, nil
}

// Open returns the claims sealed in token. A token that fails any check is
// ErrInvalid. One that is intact but expired is ErrExpired, returned with its
// claims, so a caller can still tell whose it was.
func (c *Codec) Open(token string) (Claims, error) {
	if len(token) > maxTokenLength {
		return Claims{}, ErrInvalid
	}
	obj, err := jose.ParseEncryptedCompact(token, []jose.KeyAlgorithm{jose.DIRECT}, []jose.ContentEncryption{jose.A256GCM})
	if err != nil || obj.Header.ExtraHeaders[jose.HeaderType] != typ {
		return Claims{}, ErrInvalid
	}
	key, ok := c.keys.keys[obj.Header.KeyID]
	if !ok {
		return Claims{}, ErrInvalid
	}
	plaintext, err := obj.Decrypt(key)
	if err != nil {
		return Claims{}, ErrInvalid
	}
	var p payload
	if err := json.Unmarshal(plaintext, &p); err != nil || p.Resource == "" || p.Holder == "" || slices.Contains(p.Scopes, "") {
		return Claims{}, ErrInvalid
	}
	claims := Claims{Grant: Grant{Resource: p.Resource, Holder: p.Holder, Scopes: p.Scopes}, IssuedAt: time.Unix(p.IssuedAt, 0)}
	if p.Expires != 0 {
		claims.Expires = time.Unix(p.Expires, 0)
		if !c.now().Before(claims.Expires) {
			return claims, ErrExpired
		}
	}
	return claims, nil
}

// validScopes reports whether scopes has at least one scope and none empty.
func validScopes(scopes []string) bool {
	return len(scopes) > 0 && !slices.Contains(scopes, "")
}
