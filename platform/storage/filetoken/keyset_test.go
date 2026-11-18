package filetoken_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/OpenNSW/nsw-srilanka/internal/storage/filetoken"
)

func TestParseKeyset(t *testing.T) {
	for _, value := range []string{
		entry("k1", 1),
		entry("2026-10", 1) + "," + entry("2026-01", 2),
		" " + entry("a_b", 1) + " , " + entry("c", 2) + " ",
	} {
		if _, err := filetoken.ParseKeyset(value); err != nil {
			t.Errorf("ParseKeyset(%q): %v", value, err)
		}
	}
}

func TestParseKeysetRejects(t *testing.T) {
	short := base64.StdEncoding.EncodeToString(rawKey(1)[:16])
	cases := map[string]string{
		"empty":         "",
		"blank":         "  ",
		"no kid":        base64.StdEncoding.EncodeToString(rawKey(1)),
		"empty kid":     ":" + base64.StdEncoding.EncodeToString(rawKey(1)),
		"uppercase kid": entry("K1", 1),
		"long kid":      entry(strings.Repeat("k", 33), 1),
		"duplicate kid": entry("k1", 1) + "," + entry("k1", 2),
		"empty entry":   entry("k1", 1) + ",",
		"bad base64":    "k1:not base64!",
		"short key":     "k1:" + short,
	}
	for name, value := range cases {
		if _, err := filetoken.ParseKeyset(value); err == nil {
			t.Errorf("%s: ParseKeyset(%q) succeeded", name, value)
		}
	}
}

// The first key seals; the rest only open.
func TestParseKeysetNewestFirst(t *testing.T) {
	c := newClock()
	token := seal(t, newCodec(t, entry("new", 2)+","+entry("old", 1), c), "v", "h", 0)

	if _, err := newCodec(t, entry("new", 2), c).Open(token); err != nil {
		t.Errorf("the first key did not seal: %v", err)
	}
}
