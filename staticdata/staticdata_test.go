// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package staticdata

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/testutil"
)

// newTestRegistry returns a one-manifest artifact set listing id at version.
func newTestRegistry(t *testing.T, id, version, path string, body []byte) *artifact.Set {
	t.Helper()
	manifest, err := json.Marshal(artifact.ManifestConfig{Artifacts: []artifact.ManifestRow{{ID: id, Kind: Kind, Version: version, Path: path}}})
	if err != nil {
		t.Fatal(err)
	}
	set, err := artifact.LoadSet(context.Background(), testutil.MemLoader{path: body, artifact.ManifestPath(1): manifest})
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// newTwoManifestSet returns a set whose manifest 1 lists ports_1.json and whose manifest 2
// lists ports_2.json, both under id "ports" with no version, as a release that replaces the
// data publishes them.
func newTwoManifestSet(t *testing.T) *artifact.Set {
	t.Helper()
	set, err := artifact.LoadSet(context.Background(), testutil.MemLoader{
		"ports_1.json":     []byte(`{"data":["CMB"]}`),
		"ports_2.json":     []byte(`{"data":["CMB","HRI"]}`),
		"manifest_v1.json": []byte(`{"artifacts":[{"id":"ports","kind":"static_data","path":"ports_1.json"}]}`),
		"manifest_v2.json": []byte(`{"artifacts":[{"id":"ports","kind":"static_data","path":"ports_2.json"}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// Each manifest version resolves the data it lists, so a case pinned to manifest 1 keeps
// the list it started with after manifest 2 replaces it.
func TestLoadAt_ResolvesEachManifestsOwnData(t *testing.T) {
	set := newTwoManifestSet(t)
	for v, want := range map[int]string{1: `{"data":["CMB"]}`, 2: `{"data":["CMB","HRI"]}`} {
		raw, err := LoadAt(context.Background(), set, "ports", v)
		if err != nil || string(raw) != want {
			t.Errorf("manifest %d: got %s, %v; want %s", v, raw, err, want)
		}
	}
}

// A manifest that lists the id only at explicit versions resolves its highest one.
func TestLoadAt_ExplicitVersionsResolveHighest(t *testing.T) {
	set, err := artifact.LoadSet(context.Background(), testutil.MemLoader{
		"ports_1.json":     []byte(`{"data":["CMB"]}`),
		"ports_2.json":     []byte(`{"data":["CMB","HRI"]}`),
		"manifest_v1.json": []byte(`{"artifacts":[{"id":"ports","kind":"static_data","version":"1","path":"ports_1.json"},{"id":"ports","kind":"static_data","version":"2","path":"ports_2.json"}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := LoadAt(context.Background(), set, "ports", 1)
	if err != nil || string(raw) != `{"data":["CMB","HRI"]}` {
		t.Fatalf("got %s, %v; want version 2's data", raw, err)
	}
}

func TestLoadAt_UnknownManifestVersion(t *testing.T) {
	set := newTwoManifestSet(t)
	if _, err := LoadAt(context.Background(), set, "ports", 3); !errors.Is(err, artifact.ErrUnknownManifestVersion) {
		t.Fatalf("expected ErrUnknownManifestVersion, got %v", err)
	}
}

func TestLoadAt_UnknownID(t *testing.T) {
	set := newTwoManifestSet(t)
	if _, err := LoadAt(context.Background(), set, "missing", 1); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// LoadAt rejects an artifact that is not a JSON object with a top-level "data" array.
func TestLoadAt_InvalidArtifact(t *testing.T) {
	for name, body := range map[string]string{
		"invalid json":            `not json`,
		"missing data field":      `{"codes":["0101"]}`,
		"bare array":              `["0101","0102"]`,
		"data field not an array": `{"data":"0101"}`,
		"data field is null":      `{"data":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			set := newTestRegistry(t, "hs-codes", "", "refdata/hs-codes.json", []byte(body))
			if _, err := LoadAt(context.Background(), set, "hs-codes", 1); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}

// A manifest that lists the id only at an explicit version, as the manifests published so far
// do, resolves that version.
func TestLoadAt_ExplicitVersionOnly(t *testing.T) {
	set := newTestRegistry(t, "hs-codes", "1.0.0", "refdata/hs-codes/1.0.0.json", []byte(`{"data":["0101"]}`))

	raw, err := LoadAt(context.Background(), set, "hs-codes", 1)
	if err != nil || string(raw) != `{"data":["0101"]}` {
		t.Fatalf("got %s, %v", raw, err)
	}
}
