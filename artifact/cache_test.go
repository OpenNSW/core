// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package artifact_test

import (
	"context"
	"errors"
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

	t.Run("a caller changing the bytes it got on a miss does not change the source", func(t *testing.T) {
		src := counting(testutil.MemLoader{
			"manifest_v1.json": manifest(row("welcome", "email", "", "welcome.json")),
			"welcome.json":     []byte(`{"subject":"Hello"}`),
		})
		// Too small to keep the file, so every lookup misses and fetches it from the source.
		set, err := artifact.LoadSet(ctx, src, artifact.WithFileCacheBytes(1))
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
		if n := src.count("welcome.json"); n != 3 {
			t.Errorf("welcome.json loaded %d times, want 3", n)
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
