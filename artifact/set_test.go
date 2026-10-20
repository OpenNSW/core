// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package artifact_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
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
		// Manifests above the pin are not read at load time, but still load on demand.
		if set.Current() != 1 || set.Latest() != 1 {
			t.Errorf("current, latest = %d, %d; want 1, 1", set.Current(), set.Latest())
		}
		if _, err := set.Registry(context.Background(), 2); err != nil {
			t.Errorf("Registry(2) on demand: %v", err)
		}
		if set.Current() != 1 {
			t.Errorf("current = %d after an on-demand load, want the pinned 1", set.Current())
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
		if _, err := set.Registry(ctx, 3); err == nil {
			t.Error("the broken manifest_v3.json should still be refused on demand")
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

	t.Run("no manifest_v1.json fails", func(t *testing.T) {
		_, err := artifact.LoadSet(ctx, testutil.MemLoader{"manifest.json": manifest()})
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

	t.Run("a malformed manifest fails", func(t *testing.T) {
		src := twoVersions()
		src["manifest_v2.json"] = []byte(`{`)
		if _, err := artifact.LoadSet(ctx, src); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("an explicit version must keep its path across manifests", func(t *testing.T) {
		src := testutil.MemLoader{
			"manifest_v1.json": manifest(row("ports", "static_data", "1", "ports.json")),
			"manifest_v2.json": manifest(row("ports", "static_data", "1", "ports_moved.json")),
		}
		_, err := artifact.LoadSet(ctx, src)
		if err == nil || !strings.Contains(err.Error(), "must keep its file") {
			t.Fatalf("expected an explicit-version path error, got %v", err)
		}

		src["manifest_v2.json"] = manifest(row("ports", "static_data", "1", "ports.json"), row("ports", "static_data", "2", "ports_v2.json"))
		if _, err := artifact.LoadSet(ctx, src); err != nil {
			t.Fatalf("same path and a new version should load, got %v", err)
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

	t.Run("loads a manifest published after the set was loaded", func(t *testing.T) {
		src := twoVersions()
		set, err := artifact.LoadSet(ctx, src)
		if err != nil {
			t.Fatalf("LoadSet: %v", err)
		}
		src["manifest_v3.json"] = manifest(row("welcome", "email", "", "welcome_v3.json"))
		src["welcome_v3.json"] = []byte(`{"subject":"Third"}`)

		reg, err := set.Registry(ctx, 3)
		if err != nil {
			t.Fatalf("Registry(3): %v", err)
		}
		got, err := artifact.Latest[fakeEmail](ctx, reg, "welcome")
		if err != nil || got.Subject != "Third" {
			t.Fatalf("v3 resolved %q, %v; want Third", got.Subject, err)
		}
		if set.Current() != 2 || set.Latest() != 3 {
			t.Errorf("current, latest = %d, %d; want 2, 3 (an on-demand load must not move current)", set.Current(), set.Latest())
		}
	})

	t.Run("a bad manifest found on demand is reported and not cached", func(t *testing.T) {
		src := twoVersions()
		set, err := artifact.LoadSet(ctx, src)
		if err != nil {
			t.Fatalf("LoadSet: %v", err)
		}
		src["manifest_v3.json"] = []byte(`{`)
		if _, err := set.Registry(ctx, 3); err == nil || errors.Is(err, artifact.ErrUnknownManifestVersion) {
			t.Fatalf("expected the parse error for a malformed manifest_v3.json, got %v", err)
		}
		if set.Latest() != 2 {
			t.Errorf("latest = %d after a rejected manifest, want 2", set.Latest())
		}
	})

	t.Run("a probe stops at the version asked for", func(t *testing.T) {
		src := twoVersions()
		set, err := artifact.LoadSet(ctx, src)
		if err != nil {
			t.Fatalf("LoadSet: %v", err)
		}
		src["manifest_v3.json"] = manifest(row("welcome", "email", "", "welcome.json"))
		src["manifest_v4.json"] = []byte(`{`) // broken, and not asked for
		if _, err := set.Registry(ctx, 3); err != nil {
			t.Fatalf("Registry(3) should not fail on a later broken manifest, got %v", err)
		}
		if set.Latest() != 3 {
			t.Errorf("latest = %d, want 3", set.Latest())
		}
	})

	t.Run("is safe for concurrent use", func(t *testing.T) {
		src := twoVersions()
		set, err := artifact.LoadSet(ctx, src)
		if err != nil {
			t.Fatalf("LoadSet: %v", err)
		}
		// Published before the goroutines start, so they race to load it on demand.
		src["manifest_v3.json"] = manifest(row("welcome", "email", "", "welcome.json"))

		var wg sync.WaitGroup
		var failures atomic.Int32
		for i := range 32 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := set.Registry(ctx, 1+i%3); err != nil {
					failures.Add(1)
				}
				_ = set.Current()
				_ = set.Versions()
			}()
		}
		wg.Wait()
		if n := failures.Load(); n != 0 {
			t.Errorf("%d concurrent Registry calls failed", n)
		}
	})
}

func TestGetExplicit(t *testing.T) {
	ctx := context.Background()
	src := testutil.MemLoader{
		"manifest_v1.json": manifest(row("ports", "email", "1", "ports.json")),
		"manifest_v2.json": manifest(row("ports", "email", "1", "ports.json"), row("ports", "email", "2", "ports_v2.json")),
		"ports.json":       []byte(`{"subject":"one"}`),
		"ports_v2.json":    []byte(`{"subject":"two"}`),
	}
	set, err := artifact.LoadSet(ctx, src)
	if err != nil {
		t.Fatalf("LoadSet: %v", err)
	}
	for version, want := range map[string]string{"1": "one", "2": "two"} {
		got, err := artifact.GetExplicit[fakeEmail](ctx, set, "ports", version)
		if err != nil || got.Subject != want {
			t.Errorf("version %s: got %q, %v; want %q", version, got.Subject, err, want)
		}
	}
	for _, version := range []string{"3", ""} {
		if _, err := artifact.GetExplicit[fakeEmail](ctx, set, "ports", version); !errors.Is(err, artifact.ErrNotFound) {
			t.Errorf("version %q: expected ErrNotFound, got %v", version, err)
		}
	}
}

func TestValidate(t *testing.T) {
	ctx := context.Background()
	refsTo := func(kind artifact.Kind) artifact.Check {
		return func(raw []byte) ([]artifact.Ref, error) {
			s := strings.TrimSpace(string(raw))
			if s == "bad" {
				return nil, errors.New("cannot parse")
			}
			if s == "" {
				return nil, nil
			}
			return []artifact.Ref{{ID: s, Kinds: []artifact.Kind{kind}, Field: "points_at"}}, nil
		}
	}
	checks := artifact.Checks{"parent": refsTo("child")}

	t.Run("passes when every file exists and every reference resolves", func(t *testing.T) {
		set, err := artifact.LoadSet(ctx, testutil.MemLoader{
			"manifest_v1.json": manifest(row("p", "parent", "", "p.txt"), row("c", "child", "", "c.txt"), row("other", "unchecked", "", "o.txt")),
			"p.txt":            []byte("c"),
			"c.txt":            []byte(""),
			"o.txt":            []byte("anything"),
		})
		if err != nil {
			t.Fatalf("LoadSet: %v", err)
		}
		if err := set.Validate(ctx, 1, checks); err != nil {
			t.Fatalf("Validate: %v", err)
		}
	})

	t.Run("reports every problem at once", func(t *testing.T) {
		set, err := artifact.LoadSet(ctx, testutil.MemLoader{
			"manifest_v1.json": manifest(
				row("dangling", "parent", "", "dangling.txt"),
				row("broken", "parent", "", "broken.txt"),
				row("gone", "unchecked", "", "gone.txt"),
				row("wrong-kind", "parent", "", "wrong.txt"),
				row("c", "unchecked", "", "c.txt"),
			),
			"dangling.txt": []byte("nobody"),
			"broken.txt":   []byte("bad"),
			"wrong.txt":    []byte("c"),
			"c.txt":        []byte(""),
		})
		if err != nil {
			t.Fatalf("LoadSet: %v", err)
		}
		err = set.Validate(ctx, 1, checks)
		if err == nil {
			t.Fatal("expected problems")
		}
		msg := err.Error()
		for _, want := range []string{
			"4 problem(s)",
			`dangling/parent: points_at "nobody" is not listed as child`,
			"broken/parent: \"broken.txt\": cannot parse",
			"gone/unchecked: load \"gone.txt\"",
			`wrong-kind/parent: points_at "c" is not listed as child`,
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("report is missing %q:\n%s", want, msg)
			}
		}
	})

	t.Run("an unknown version fails", func(t *testing.T) {
		set, err := artifact.LoadSet(ctx, twoVersions())
		if err != nil {
			t.Fatalf("LoadSet: %v", err)
		}
		if err := set.Validate(ctx, 7, checks); !errors.Is(err, artifact.ErrUnknownManifestVersion) {
			t.Fatalf("expected ErrUnknownManifestVersion, got %v", err)
		}
	})
}

func TestLatestPrefersTheUnversionedRow(t *testing.T) {
	ctx := context.Background()
	set, err := artifact.LoadSet(ctx, testutil.MemLoader{
		"manifest_v1.json": manifest(row("welcome", "email", "", "current.json"), row("welcome", "email", "1", "old.json")),
		"current.json":     []byte(`{"subject":"current"}`),
		"old.json":         []byte(`{"subject":"old"}`),
	})
	if err != nil {
		t.Fatalf("LoadSet: %v", err)
	}
	got, err := artifact.Latest[fakeEmail](ctx, set.CurrentRegistry(), "welcome")
	if err != nil || got.Subject != "current" {
		t.Fatalf("Latest = %q, %v; want the unversioned row", got.Subject, err)
	}
	old, err := artifact.GetExplicit[fakeEmail](ctx, set, "welcome", "1")
	if err != nil || old.Subject != "old" {
		t.Fatalf("GetExplicit(1) = %q, %v; want the explicit row", old.Subject, err)
	}
}

// A version only a manifest published after the set loaded lists is found by probing.
func TestGetExplicitFindsAVersionInANewerManifest(t *testing.T) {
	ctx := context.Background()
	src := testutil.MemLoader{
		"manifest_v1.json": manifest(row("ports", "email", "1", "ports.json")),
		"ports.json":       []byte(`{"subject":"one"}`),
	}
	set, err := artifact.LoadSet(ctx, src)
	if err != nil {
		t.Fatalf("LoadSet: %v", err)
	}
	src["manifest_v2.json"] = manifest(row("ports", "email", "1", "ports.json"), row("ports", "email", "2", "ports_v2.json"))
	src["ports_v2.json"] = []byte(`{"subject":"two"}`)

	got, err := artifact.GetExplicit[fakeEmail](ctx, set, "ports", "2")
	if err != nil || got.Subject != "two" {
		t.Fatalf("GetExplicit(2) = %q, %v; want the v2 file", got.Subject, err)
	}
	if set.Current() != 1 || set.Latest() != 2 {
		t.Errorf("current, latest = %d, %d; want 1, 2", set.Current(), set.Latest())
	}
	if _, err := artifact.GetExplicit[fakeEmail](ctx, set, "ports", "9"); !errors.Is(err, artifact.ErrNotFound) {
		t.Errorf("an unpublished version: expected ErrNotFound, got %v", err)
	}
}
