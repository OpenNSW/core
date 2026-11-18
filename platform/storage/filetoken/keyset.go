package filetoken

import (
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// keySize is the length of an A256GCM key.
const keySize = 32

// kidPattern keeps key IDs short and plain: every token carries one in its
// header.
var kidPattern = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

// Keyset holds the keys tokens are sealed with. The first key seals every new
// token; any key opens a token that names it. Rotating means prepending a new
// key and keeping the old ones for as long as their tokens must open. Tokens
// that never expire, such as those given to OGAs, need their key kept for good.
type Keyset struct {
	current string
	keys    map[string][]byte
}

// ParseKeyset parses comma-separated "kid:key" pairs, newest first. Each key is
// 32 bytes in standard base64, such as the output of `openssl rand -base64 32`.
func ParseKeyset(value string) (*Keyset, error) {
	if strings.TrimSpace(value) == "" {
		return nil, errors.New("filetoken: keyset is empty")
	}
	ks := &Keyset{keys: map[string][]byte{}}
	for i, entry := range strings.Split(value, ",") {
		kid, encoded, ok := strings.Cut(strings.TrimSpace(entry), ":")
		if !ok {
			return nil, fmt.Errorf("filetoken: keyset entry %d: want kid:key", i+1)
		}
		if !kidPattern.MatchString(kid) {
			return nil, fmt.Errorf("filetoken: keyset entry %d: kid %q must match %s", i+1, kid, kidPattern)
		}
		if _, dup := ks.keys[kid]; dup {
			return nil, fmt.Errorf("filetoken: keyset entry %d: duplicate kid %q", i+1, kid)
		}
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("filetoken: keyset entry %d (kid %q): key is not valid base64: %w", i+1, kid, err)
		}
		if len(key) != keySize {
			return nil, fmt.Errorf("filetoken: keyset entry %d (kid %q): key is %d bytes, want %d", i+1, kid, len(key), keySize)
		}
		if i == 0 {
			ks.current = kid
		}
		ks.keys[kid] = key
	}
	return ks, nil
}
