// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package artifact_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/testutil"
)

// countingLoader counts the loads of each path and can fail them until told to stop.
type countingLoader struct {
	files testutil.MemLoader

	mu    sync.Mutex
	loads map[string]int
	fail  error
}

func (c *countingLoader) Load(ctx context.Context, path string) ([]byte, error) {
	c.mu.Lock()
	c.loads[path]++
	fail := c.fail
	c.mu.Unlock()
	if fail != nil && path == "welcome.json" {
		return nil, fail
	}
	return c.files.Load(ctx, path)
}

func (c *countingLoader) count(path string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loads[path]
}

func counting(files testutil.MemLoader) *countingLoader {
	return &countingLoader{files: files, loads: make(map[string]int)}
}

func welcomeIn(t *testing.T, set *artifact.Set, v int) (fakeEmail, error) {
	t.Helper()
	reg, err := set.Registry(context.Background(), v)
	if err != nil {
		t.Fatalf("Registry(%d): %v", v, err)
	}
	return artifact.Latest[fakeEmail](context.Background(), reg, "welcome")
}

func TestSetCachesArtifactFiles(t *testing.T) {
	ctx := context.Background()

	t.Run("a versioned source fetches each file once, across versions", func(t *testing.T) {
		src := counting(testutil.MemLoader{
			"manifest_v1.json": manifest(row("welcome", "email", "", "welcome.json")),
			"manifest_v2.json": manifest(row("welcome", "email", "", "welcome.json")),
			"welcome.json":     []byte(`{"subject":"Hello"}`),
		})
		set, err := artifact.LoadSet(ctx, src)
		if err != nil {
			t.Fatalf("LoadSet: %v", err)
		}
		for range 3 {
			for _, v := range []int{1, 2} {
				if got, err := welcomeIn(t, set, v); err != nil || got.Subject != "Hello" {
					t.Fatalf("v%d: %+v, %v", v, got, err)
				}
			}
		}
		if n := src.count("welcome.json"); n != 1 {
			t.Errorf("welcome.json loaded %d times, want 1", n)
		}
	})

	t.Run("a failed fetch is not kept", func(t *testing.T) {
		src := counting(testutil.MemLoader{
			"manifest_v1.json": manifest(row("welcome", "email", "", "welcome.json")),
			"welcome.json":     []byte(`{"subject":"Hello"}`),
		})
		set, err := artifact.LoadSet(ctx, src)
		if err != nil {
			t.Fatalf("LoadSet: %v", err)
		}
		boom := errors.New("bucket unavailable")
		src.fail = boom
		if _, err := welcomeIn(t, set, 1); !errors.Is(err, boom) {
			t.Fatalf("err = %v, want %v", err, boom)
		}
		src.fail = nil
		if got, err := welcomeIn(t, set, 1); err != nil || got.Subject != "Hello" {
			t.Fatalf("after recovery: %+v, %v", got, err)
		}
		if got, err := welcomeIn(t, set, 1); err != nil || got.Subject != "Hello" {
			t.Fatalf("cached: %+v, %v", got, err)
		}
		if n := src.count("welcome.json"); n != 2 {
			t.Errorf("welcome.json loaded %d times, want 2 (the failure, then once)", n)
		}
	})

	t.Run("a manifest.json source fetches every time, so edits show", func(t *testing.T) {
		files := testutil.MemLoader{
			"manifest.json": manifest(row("welcome", "email", "", "welcome.json")),
			"welcome.json":  []byte(`{"subject":"Hello"}`),
		}
		src := counting(files)
		set, err := artifact.LoadSet(ctx, src)
		if err != nil {
			t.Fatalf("LoadSet: %v", err)
		}
		if got, err := welcomeIn(t, set, 1); err != nil || got.Subject != "Hello" {
			t.Fatalf("before the edit: %+v, %v", got, err)
		}
		files["welcome.json"] = []byte(`{"subject":"Edited"}`)
		if got, err := welcomeIn(t, set, 1); err != nil || got.Subject != "Edited" {
			t.Errorf("after the edit: %+v, %v; want the edited file", got, err)
		}
	})

	t.Run("a caller changing the bytes it got does not change the cache", func(t *testing.T) {
		src := counting(testutil.MemLoader{
			"manifest_v1.json": manifest(row("welcome", "email", "", "welcome.json")),
			"welcome.json":     []byte(`{"subject":"Hello"}`),
		})
		set, err := artifact.LoadSet(ctx, src)
		if err != nil {
			t.Fatalf("LoadSet: %v", err)
		}
		reg, err := set.Registry(ctx, 1)
		if err != nil {
			t.Fatalf("Registry(1): %v", err)
		}
		for range 3 {
			got, err := artifact.Latest[mutatingEmail](ctx, reg, "welcome")
			if err != nil || got.Subject != "Hello" {
				t.Fatalf("got %+v, %v; want the original file", got, err)
			}
		}
	})

}

// mutatingEmail is a fakeEmail whose Parse overwrites its input once it has read it.
type mutatingEmail struct{ fakeEmail }

func (e *mutatingEmail) Parse(raw []byte) error {
	err := e.fakeEmail.Parse(raw)
	for i := range raw {
		raw[i] = 'x'
	}
	return err
}

func TestSetFileCacheLimit(t *testing.T) {
	ctx := context.Background()
	// Each file is 20 bytes: {"subject":"Hello"} padded with spaces.
	file := func(subject string) []byte {
		b := []byte(`{"subject":"` + subject + `"}`)
		for len(b) < 20 {
			b = append(b, ' ')
		}
		return b
	}
	src := func() *countingLoader {
		return counting(testutil.MemLoader{
			"manifest_v1.json": manifest(
				row("a", "email", "", "a.json"),
				row("b", "email", "", "b.json"),
				row("c", "email", "", "c.json"),
			),
			"a.json": file("A"),
			"b.json": file("B"),
			"c.json": file("C"),
		})
	}
	get := func(t *testing.T, set *artifact.Set, id string) {
		t.Helper()
		reg, err := set.Registry(ctx, 1)
		if err != nil {
			t.Fatalf("Registry(1): %v", err)
		}
		if _, err := artifact.Latest[fakeEmail](ctx, reg, id); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}

	t.Run("evicts the least recently used file to stay within the limit", func(t *testing.T) {
		loads := src()
		set, err := artifact.LoadSet(ctx, loads, artifact.WithFileCacheBytes(40)) // room for two files
		if err != nil {
			t.Fatalf("LoadSet: %v", err)
		}
		get(t, set, "a")
		get(t, set, "b")
		get(t, set, "a") // a is now the most recently used
		get(t, set, "c") // evicts b
		get(t, set, "a")
		get(t, set, "c")
		get(t, set, "b")
		want := map[string]int{"a.json": 1, "b.json": 2, "c.json": 1}
		for path, n := range want {
			if got := loads.count(path); got != n {
				t.Errorf("%s loaded %d times, want %d", path, got, n)
			}
		}
	})

	t.Run("a file larger than the limit is never kept", func(t *testing.T) {
		loads := src()
		set, err := artifact.LoadSet(ctx, loads, artifact.WithFileCacheBytes(10))
		if err != nil {
			t.Fatalf("LoadSet: %v", err)
		}
		get(t, set, "a")
		get(t, set, "a")
		if got := loads.count("a.json"); got != 2 {
			t.Errorf("a.json loaded %d times, want 2", got)
		}
	})

	t.Run("a limit of zero turns the cache off", func(t *testing.T) {
		loads := src()
		set, err := artifact.LoadSet(ctx, loads, artifact.WithFileCacheBytes(0))
		if err != nil {
			t.Fatalf("LoadSet: %v", err)
		}
		get(t, set, "a")
		get(t, set, "a")
		if got := loads.count("a.json"); got != 2 {
			t.Errorf("a.json loaded %d times, want 2", got)
		}
	})
}

func TestSetWarm(t *testing.T) {
	ctx := context.Background()

	t.Run("fetches every distinct file of the manifest into the cache", func(t *testing.T) {
		src := counting(testutil.MemLoader{
			"manifest_v1.json": manifest(
				row("welcome", "email", "", "welcome.json"),
				row("welcome", "email", "1", "welcome.json"), // shared path
				row("farewell", "email", "", "farewell.json"),
			),
			"welcome.json":  []byte(`{"subject":"Hello"}`),
			"farewell.json": []byte(`{"subject":"Goodbye"}`),
		})
		set, err := artifact.LoadSet(ctx, src)
		if err != nil {
			t.Fatalf("LoadSet: %v", err)
		}
		if err := set.Warm(ctx, set.Current()); err != nil {
			t.Fatalf("Warm: %v", err)
		}
		if got := src.count("welcome.json"); got != 1 {
			t.Errorf("welcome.json loaded %d times during warm, want 1", got)
		}
		if got := src.count("farewell.json"); got != 1 {
			t.Errorf("farewell.json loaded %d times during warm, want 1", got)
		}

		// Subsequent lookups are served from memory without touching the loader.
		reg := set.CurrentRegistry()
		for _, id := range []string{"welcome", "farewell"} {
			if _, err := artifact.Latest[fakeEmail](ctx, reg, id); err != nil {
				t.Fatalf("Latest(%s): %v", id, err)
			}
		}
		if got := src.count("welcome.json"); got != 1 {
			t.Errorf("welcome.json loaded %d times after lookup, want 1", got)
		}
		if got := src.count("farewell.json"); got != 1 {
			t.Errorf("farewell.json loaded %d times after lookup, want 1", got)
		}
	})

	t.Run("loads a newer manifest version on demand before warming its files", func(t *testing.T) {
		files := testutil.MemLoader{
			"manifest_v1.json": manifest(row("welcome", "email", "", "welcome.json")),
			"welcome.json":     []byte(`{"subject":"Hello"}`),
		}
		src := counting(files)
		set, err := artifact.LoadSet(ctx, src)
		if err != nil {
			t.Fatalf("LoadSet: %v", err)
		}
		files["manifest_v2.json"] = manifest(row("welcome", "email", "", "welcome_v2.json"))
		files["welcome_v2.json"] = []byte(`{"subject":"Hello v2"}`)

		if err := set.Warm(ctx, 2); err != nil {
			t.Fatalf("Warm(2): %v", err)
		}
		if got := src.count("welcome_v2.json"); got != 1 {
			t.Errorf("welcome_v2.json loaded %d times, want 1", got)
		}
		if got, err := welcomeIn(t, set, 2); err != nil || got.Subject != "Hello v2" {
			t.Fatalf("v2 lookup: %+v, %v", got, err)
		}
		if got := src.count("welcome_v2.json"); got != 1 {
			t.Errorf("welcome_v2.json loaded %d times after lookup, want 1", got)
		}
	})

	t.Run("rejects an unknown manifest version", func(t *testing.T) {
		src := counting(testutil.MemLoader{
			"manifest_v1.json": manifest(row("welcome", "email", "", "welcome.json")),
			"welcome.json":     []byte(`{"subject":"Hello"}`),
		})
		set, err := artifact.LoadSet(ctx, src)
		if err != nil {
			t.Fatalf("LoadSet: %v", err)
		}
		for _, v := range []int{0, -1, 5} {
			if err := set.Warm(ctx, v); !errors.Is(err, artifact.ErrUnknownManifestVersion) {
				t.Errorf("Warm(%d): got %v, want ErrUnknownManifestVersion", v, err)
			}
		}
	})

	t.Run("is a no-op for artifact files when the cache is off", func(t *testing.T) {
		unversioned := counting(testutil.MemLoader{
			"manifest.json": manifest(row("welcome", "email", "", "welcome.json")),
			"welcome.json":  []byte(`{"subject":"Hello"}`),
		})
		set, err := artifact.LoadSet(ctx, unversioned)
		if err != nil {
			t.Fatalf("LoadSet(manifest.json): %v", err)
		}
		if err := set.Warm(ctx, 1); err != nil {
			t.Fatalf("Warm on manifest.json: %v", err)
		}
		if got := unversioned.count("welcome.json"); got != 0 {
			t.Errorf("manifest.json warm loaded welcome.json %d times, want 0", got)
		}

		disabled := counting(testutil.MemLoader{
			"manifest_v1.json": manifest(row("welcome", "email", "", "welcome.json")),
			"welcome.json":     []byte(`{"subject":"Hello"}`),
		})
		setOff, err := artifact.LoadSet(ctx, disabled, artifact.WithFileCacheBytes(0))
		if err != nil {
			t.Fatalf("LoadSet(cache=0): %v", err)
		}
		if err := setOff.Warm(ctx, 1); err != nil {
			t.Fatalf("Warm with cache=0: %v", err)
		}
		if got := disabled.count("welcome.json"); got != 0 {
			t.Errorf("cache=0 warm loaded welcome.json %d times, want 0", got)
		}
	})

	t.Run("tries every file and joins load errors", func(t *testing.T) {
		src := counting(testutil.MemLoader{
			"manifest_v1.json": manifest(
				row("a", "email", "", "missing_a.json"),
				row("b", "email", "", "present.json"),
				row("c", "email", "", "missing_c.json"),
			),
			"present.json": []byte(`{"subject":"Present"}`),
		})
		set, err := artifact.LoadSet(ctx, src)
		if err != nil {
			t.Fatalf("LoadSet: %v", err)
		}
		err = set.Warm(ctx, 1)
		if !errors.Is(err, artifact.ErrNotFound) {
			t.Fatalf("Warm err = %v, want ErrNotFound", err)
		}
		if !strings.Contains(err.Error(), "missing_a.json") || !strings.Contains(err.Error(), "missing_c.json") {
			t.Errorf("Warm err = %q, want both missing files named", err.Error())
		}
		if got := src.count("present.json"); got != 1 {
			t.Errorf("present.json loaded %d times, want 1", got)
		}
	})

	t.Run("stops claiming files once ctx is cancelled", func(t *testing.T) {
		src := counting(testutil.MemLoader{
			"manifest_v1.json": manifest(
				row("welcome", "email", "", "welcome.json"),
				row("farewell", "email", "", "farewell.json"),
			),
			"welcome.json":  []byte(`{"subject":"Hello"}`),
			"farewell.json": []byte(`{"subject":"Goodbye"}`),
		})
		set, err := artifact.LoadSet(ctx, src)
		if err != nil {
			t.Fatalf("LoadSet: %v", err)
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()

		if err := set.Warm(cancelled, 1); !errors.Is(err, context.Canceled) {
			t.Fatalf("Warm err = %v, want context.Canceled", err)
		}
		for _, p := range []string{"welcome.json", "farewell.json"} {
			if got := src.count(p); got != 0 {
				t.Errorf("%s loaded %d times after cancel, want 0", p, got)
			}
		}
	})
}
