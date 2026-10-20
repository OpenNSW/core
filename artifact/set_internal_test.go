// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package artifact

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type countingLoader struct {
	files map[string][]byte
	loads atomic.Int32
}

func (c *countingLoader) Load(_ context.Context, path string) ([]byte, error) {
	c.loads.Add(1)
	if b, ok := c.files[path]; ok {
		return b, nil
	}
	return nil, ErrNotFound
}

func TestRegistryBacksOffAfterAnEmptyProbe(t *testing.T) {
	ctx := context.Background()
	src := &countingLoader{files: map[string][]byte{"manifest_v1.json": []byte(`{"artifacts":[]}`)}}
	set, err := LoadSet(ctx, src)
	if err != nil {
		t.Fatalf("LoadSet: %v", err)
	}
	clock := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	set.now = func() time.Time { return clock }

	src.loads.Store(0)
	if _, err := set.Registry(ctx, 5); !errors.Is(err, ErrUnknownManifestVersion) {
		t.Fatalf("expected ErrUnknownManifestVersion, got %v", err)
	}
	if n := src.loads.Load(); n != 1 {
		t.Fatalf("first miss made %d loads, want 1", n)
	}

	// A second miss inside the interval does not probe again, even once the manifest exists.
	src.files["manifest_v2.json"] = []byte(`{"artifacts":[]}`)
	if _, err := set.Registry(ctx, 2); !errors.Is(err, ErrUnknownManifestVersion) {
		t.Fatalf("expected ErrUnknownManifestVersion inside the back-off, got %v", err)
	}
	if n := src.loads.Load(); n != 1 {
		t.Fatalf("a miss inside the back-off made %d loads in all, want 1", n)
	}

	clock = clock.Add(reprobeInterval)
	if _, err := set.Registry(ctx, 2); err != nil {
		t.Fatalf("after the back-off the new manifest should load, got %v", err)
	}
}

// A probe that fails with a loader error backs off like one that finds nothing, and a later
// probe after the interval loads the version.
func TestRegistryBacksOffAfterAFailedProbe(t *testing.T) {
	ctx := context.Background()
	src := &countingLoader{files: map[string][]byte{"manifest_v1.json": []byte(`{"artifacts":[]}`)}}
	set, err := LoadSet(ctx, src)
	if err != nil {
		t.Fatalf("LoadSet: %v", err)
	}
	clock := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	set.now = func() time.Time { return clock }

	src.files["manifest_v2.json"] = []byte(`{`)
	src.loads.Store(0)
	if _, err := set.Registry(ctx, 2); err == nil {
		t.Fatal("expected an error for a malformed manifest_v2.json")
	}
	if _, err := set.Registry(ctx, 2); !errors.Is(err, ErrUnknownManifestVersion) {
		t.Fatalf("inside the back-off: expected ErrUnknownManifestVersion, got %v", err)
	}
	if n := src.loads.Load(); n != 1 {
		t.Fatalf("made %d loads, want 1 (the second call must back off)", n)
	}

	src.files["manifest_v2.json"] = []byte(`{"artifacts":[]}`)
	clock = clock.Add(reprobeInterval)
	if _, err := set.Registry(ctx, 2); err != nil {
		t.Fatalf("after the back-off the fixed manifest should load, got %v", err)
	}
}

// A probe holds no Set lock across its loader round trips, so lookups of versions already
// loaded do not wait for it.
func TestProbeDoesNotBlockLoadedVersions(t *testing.T) {
	ctx := context.Background()
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	loader := blockingLoader{
		files:   map[string][]byte{"manifest_v1.json": []byte(`{"artifacts":[]}`)},
		block:   "manifest_v2.json",
		entered: entered,
		release: release,
	}
	// Pinned, so LoadSet itself does not probe the blocked manifest.
	set, err := LoadSet(ctx, loader, WithPinnedVersion(1))
	if err != nil {
		t.Fatalf("LoadSet: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_, _ = set.Registry(ctx, 2)
		close(done)
	}()
	<-entered // the probe is now inside the loader

	lookup := make(chan error, 1)
	go func() {
		_, err := set.Registry(ctx, 1)
		_ = set.Current()
		lookup <- err
	}()
	select {
	case err := <-lookup:
		if err != nil {
			t.Fatalf("Registry(1): %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a lookup of a loaded version waited for the probe")
	}
	close(release)
	<-done
}

type blockingLoader struct {
	files   map[string][]byte
	block   string
	entered chan<- struct{}
	release <-chan struct{}
}

func (b blockingLoader) Load(_ context.Context, path string) ([]byte, error) {
	if path == b.block {
		b.entered <- struct{}{}
		<-b.release
		return nil, ErrNotFound
	}
	if f, ok := b.files[path]; ok {
		return f, nil
	}
	return nil, ErrNotFound
}
