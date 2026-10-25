// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package artifact_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/testutil"
)

func manifest(rows ...string) []byte {
	return []byte(`{"artifacts":[` + strings.Join(rows, ",") + `]}`)
}

func row(id, kind, version, path string) string {
	return fmt.Sprintf(`{"id":%q,"kind":%q,"version":%q,"path":%q}`, id, kind, version, path)
}

// twoVersions is a source where manifest v2 repoints "welcome" at a new file.
func twoVersions() testutil.MemLoader {
	return testutil.MemLoader{
		"manifest_v1.json": manifest(row("welcome", "email", "", "welcome.json")),
		"manifest_v2.json": manifest(row("welcome", "email", "", "welcome_v2.json")),
		"welcome.json":     []byte(`{"subject":"Hello"}`),
		"welcome_v2.json":  []byte(`{"subject":"Hello again"}`),
	}
}

func TestLoadSet(t *testing.T) {
	ctx := context.Background()

	t.Run("loads every version and makes the highest current", func(t *testing.T) {
		set, err := artifact.LoadSet(ctx, twoVersions())
		if err != nil {
			t.Fatalf("LoadSet: %v", err)
		}
		if set.Current() != 2 || set.Latest() != 2 {
			t.Errorf("current, latest = %d, %d; want 2, 2", set.Current(), set.Latest())
		}
		if got := set.Versions(); len(got) != 2 || got[0] != 1 || got[1] != 2 {
			t.Errorf("Versions() = %v, want [1 2]", got)
		}
	})

	t.Run("each version resolves its own file", func(t *testing.T) {
		set, err := artifact.LoadSet(ctx, twoVersions())
		if err != nil {
			t.Fatalf("LoadSet: %v", err)
		}
		for v, want := range map[int]string{1: "Hello", 2: "Hello again"} {
			reg, err := set.Registry(ctx, v)
			if err != nil {
				t.Fatalf("Registry(%d): %v", v, err)
			}
			got, err := artifact.Latest[fakeEmail](ctx, reg, "welcome")
			if err != nil {
				t.Fatalf("v%d: %v", v, err)
			}
			if got.Subject != want {
				t.Errorf("v%d subject = %q, want %q", v, got.Subject, want)
			}
		}
		cur, err := artifact.Latest[fakeEmail](ctx, set.CurrentRegistry(), "welcome")
		if err != nil || cur.Subject != "Hello again" {
			t.Errorf("current registry resolved %q, %v; want the v2 file", cur.Subject, err)
		}
	})

	t.Run("a pin makes an older version current", func(t *testing.T) {
		set, err := artifact.LoadSet(ctx, twoVersions(), artifact.WithPinnedVersion(1))
		if err != nil {
			t.Fatalf("LoadSet: %v", err)
		}
		// Manifests above the pin are not read.
		if set.Current() != 1 || set.Latest() != 1 {
			t.Errorf("current, latest = %d, %d; want 1, 1", set.Current(), set.Latest())
		}
	})

	t.Run("a pin rolls back past a broken newer manifest", func(t *testing.T) {
		src := twoVersions()
		src["manifest_v3.json"] = manifest(row("welcome", "email", "", "a.json"), row("welcome", "email", "", "b.json"))
		if _, err := artifact.LoadSet(ctx, src); err == nil {
			t.Fatal("without a pin the broken manifest_v3.json should fail LoadSet")
		}
		set, err := artifact.LoadSet(ctx, src, artifact.WithPinnedVersion(2))
		if err != nil {
			t.Fatalf("pinned to 2: %v", err)
		}
		if set.Current() != 2 || set.Latest() != 2 {
			t.Errorf("current, latest = %d, %d; want 2, 2", set.Current(), set.Latest())
		}
		if _, err := set.Registry(ctx, 3); !errors.Is(err, artifact.ErrUnknownManifestVersion) {
			t.Errorf("Registry(3): expected ErrUnknownManifestVersion, got %v", err)
		}
	})

	for name, pin := range map[string]int{"above the highest": 3, "negative": -1} {
		t.Run("a pin "+name+" fails", func(t *testing.T) {
			_, err := artifact.LoadSet(ctx, twoVersions(), artifact.WithPinnedVersion(pin))
			if !errors.Is(err, artifact.ErrUnknownManifestVersion) {
				t.Fatalf("expected ErrUnknownManifestVersion, got %v", err)
			}
		})
	}

	t.Run("without manifest_v1.json, manifest.json loads as version 1", func(t *testing.T) {
		src := testutil.MemLoader{
			"manifest.json": manifest(row("welcome", "email", "", "welcome.json")),
			"welcome.json":  []byte(`{"subject":"Hello"}`),
		}
		for _, pin := range []int{0, 1} {
			set, err := artifact.LoadSet(ctx, src, artifact.WithPinnedVersion(pin))
			if err != nil {
				t.Fatalf("pin %d: LoadSet: %v", pin, err)
			}
			if set.Current() != 1 || set.Latest() != 1 {
				t.Errorf("pin %d: current, latest = %d, %d; want 1, 1", pin, set.Current(), set.Latest())
			}
			got, err := artifact.Latest[fakeEmail](ctx, set.CurrentRegistry(), "welcome")
			if err != nil || got.Subject != "Hello" {
				t.Errorf("pin %d: resolved %q, %v; want manifest.json's file", pin, got.Subject, err)
			}
		}
		if _, err := artifact.LoadSet(ctx, src, artifact.WithPinnedVersion(2)); !errors.Is(err, artifact.ErrUnknownManifestVersion) {
			t.Errorf("pin 2: expected ErrUnknownManifestVersion, got %v", err)
		}
	})

	t.Run("manifest.json is ignored once manifest_v1.json exists", func(t *testing.T) {
		src := twoVersions()
		src["manifest.json"] = []byte(`{`)
		set, err := artifact.LoadSet(ctx, src)
		if err != nil || set.Latest() != 2 {
			t.Fatalf("LoadSet = latest %v, %v; want 2 from the versioned manifests", set, err)
		}
	})

	t.Run("no manifest fails", func(t *testing.T) {
		_, err := artifact.LoadSet(ctx, testutil.MemLoader{})
		if !errors.Is(err, artifact.ErrUnknownManifestVersion) {
			t.Fatalf("expected ErrUnknownManifestVersion, got %v", err)
		}
	})

	t.Run("a load error other than not-found fails instead of ending the probe", func(t *testing.T) {
		src := twoVersions()
		boom := errors.New("connection reset")
		loader := errorLoader(func(ctx context.Context, path string) ([]byte, error) {
			if path == "manifest_v2.json" {
				return nil, boom
			}
			return src.Load(ctx, path)
		})
		if _, err := artifact.LoadSet(ctx, loader); !errors.Is(err, boom) {
			t.Fatalf("expected the loader error, got %v", err)
		}
	})

	t.Run("a manifest with duplicate rows fails", func(t *testing.T) {
		src := twoVersions()
		src["manifest_v2.json"] = manifest(row("welcome", "email", "", "a.json"), row("welcome", "email", "", "b.json"))
		_, err := artifact.LoadSet(ctx, src)
		if err == nil || !strings.Contains(err.Error(), "manifest_v2.json") {
			t.Fatalf("expected an error naming manifest_v2.json, got %v", err)
		}
	})

	t.Run("an error in manifest.json names manifest.json", func(t *testing.T) {
		src := testutil.MemLoader{"manifest.json": manifest(row("welcome", "email", "", "a.json"), row("welcome", "email", "", "b.json"))}
		_, err := artifact.LoadSet(ctx, src)
		if err == nil || !strings.HasPrefix(err.Error(), "manifest.json:") {
			t.Fatalf("expected an error naming manifest.json, got %v", err)
		}
	})

	t.Run("a malformed manifest fails", func(t *testing.T) {
		src := twoVersions()
		src["manifest_v2.json"] = []byte(`{`)
		if _, err := artifact.LoadSet(ctx, src); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("nil loader fails", func(t *testing.T) {
		if _, err := artifact.LoadSet(ctx, nil); err == nil {
			t.Fatal("expected an error")
		}
	})
}

func TestSetRegistry(t *testing.T) {
	ctx := context.Background()

	t.Run("rejects versions that cannot exist", func(t *testing.T) {
		set, err := artifact.LoadSet(ctx, twoVersions())
		if err != nil {
			t.Fatalf("LoadSet: %v", err)
		}
		for _, v := range []int{0, -1, 3} {
			if _, err := set.Registry(ctx, v); !errors.Is(err, artifact.ErrUnknownManifestVersion) {
				t.Errorf("Registry(%d): expected ErrUnknownManifestVersion, got %v", v, err)
			}
			if _, err := set.Registry(ctx, v); errors.Is(err, artifact.ErrNotFound) {
				t.Errorf("Registry(%d): an unknown version must not match ErrNotFound", v)
			}
		}
	})
}
