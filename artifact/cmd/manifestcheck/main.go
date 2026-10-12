// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Command manifestcheck checks bundles of versioned artifact manifests on disk, for an artifacts
// repository's CI.
//
//	manifestcheck [-base DIR] [-root DIR] BUNDLE...
//
// Each BUNDLE is a directory, relative to -root (default "."), holding manifest_v1.json,
// manifest_v2.json, … and the artifacts they list. For each bundle it checks that:
//
//   - manifest versions are contiguous from 1, so the runtime's probe reaches every one;
//   - every manifest loads as artifact.LoadSet loads it: no duplicate rows, and an explicitly
//     versioned row keeps its path across manifests;
//   - every artifact each manifest lists exists, parses for the core kinds, and points only at
//     artifacts the same manifest lists (see corechecks).
//
// With -base, the root of a checkout of the branch being merged into, it also checks that every
// manifest the base has is unchanged and every file a base manifest lists is unchanged: a
// published manifest and its artifacts are immutable.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"sync"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/adapter/corechecks"
	"github.com/OpenNSW/core/artifact/loaders/local"
)

func main() {
	// The report is the output; LoadSet's progress logs would only interleave with it.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("manifestcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "directory the bundles are relative to")
	base := fs.String("base", "", "root of a checkout of the base branch; enables the immutability check")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: manifestcheck [-base DIR] [-root DIR] BUNDLE...")
		return 2
	}

	failed := false
	for _, bundle := range fs.Args() {
		problems := checkBundle(ctx, filepath.Join(*root, bundle))
		if *base != "" {
			problems = append(problems, checkImmutable(filepath.Join(*base, bundle), filepath.Join(*root, bundle))...)
		}
		if len(problems) == 0 {
			_, _ = fmt.Fprintf(stdout, "ok   %s\n", bundle)
			continue
		}
		failed = true
		_, _ = fmt.Fprintf(stdout, "FAIL %s\n", bundle)
		for _, p := range problems {
			_, _ = fmt.Fprintf(stdout, "     %v\n", p)
		}
	}
	if failed {
		return 1
	}
	return 0
}

var manifestName = regexp.MustCompile(`^manifest_v([1-9][0-9]*)\.json$`)

// manifestVersions returns the versions of the manifest files directly in dir, ascending.
func manifestVersions(dir string) ([]int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var vs []int
	for _, e := range entries {
		m := manifestName.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() {
			continue
		}
		v, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		vs = append(vs, v)
	}
	slices.Sort(vs)
	return vs, nil
}

func checkBundle(ctx context.Context, dir string) []error {
	versions, err := manifestVersions(dir)
	if err != nil {
		return []error{err}
	}
	if len(versions) == 0 {
		return []error{fmt.Errorf("no %s", artifact.ManifestPath(1))}
	}
	var problems []error
	for i, v := range versions {
		if v != i+1 {
			problems = append(problems, fmt.Errorf("%s is missing, so %s and later are never loaded", artifact.ManifestPath(i+1), artifact.ManifestPath(v)))
			break
		}
	}

	disk, err := local.New(local.Config{Root: dir})
	if err != nil {
		return append(problems, err)
	}
	// A bundle's manifests share most of their files, which are immutable, so each file is
	// read and checked once however many manifests list it. Only the reference check, which
	// depends on what each manifest lists, runs per manifest.
	loader := &cachingLoader{inner: disk, files: make(map[string][]byte)}
	checks := memoize(corechecks.Checks())
	set, err := artifact.LoadSet(ctx, loader)
	if err != nil {
		return append(problems, err)
	}
	for _, v := range set.Versions() {
		if err := set.Validate(ctx, v, checks); err != nil {
			problems = append(problems, err)
		}
	}
	return problems
}

// cachingLoader reads each file once. Validate loads concurrently, so it locks.
type cachingLoader struct {
	inner artifact.Loader
	mu    sync.Mutex
	files map[string][]byte
}

func (c *cachingLoader) Load(ctx context.Context, path string) ([]byte, error) {
	c.mu.Lock()
	b, ok := c.files[path]
	c.mu.Unlock()
	if ok {
		return b, nil
	}
	b, err := c.inner.Load(ctx, path)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.files[path] = b
	c.mu.Unlock()
	return b, nil
}

// memoize wraps each check so the same bytes of the same kind are parsed once.
func memoize(checks artifact.Checks) artifact.Checks {
	type result struct {
		refs []artifact.Ref
		err  error
	}
	var mu sync.Mutex
	seen := make(map[string]result)
	out := make(artifact.Checks, len(checks))
	for kind, check := range checks {
		out[kind] = func(raw []byte) ([]artifact.Ref, error) {
			sum := sha256.Sum256(raw)
			key := string(kind) + "\x00" + string(sum[:])
			mu.Lock()
			r, ok := seen[key]
			mu.Unlock()
			if ok {
				return r.refs, r.err
			}
			refs, err := check(raw)
			mu.Lock()
			seen[key] = result{refs, err}
			mu.Unlock()
			return refs, err
		}
	}
	return out
}

// checkImmutable reports every base manifest that head changed or removed, and every file a
// base manifest lists that head changed or removed. A bundle the base does not have, or one
// without versioned manifests yet, has nothing published to protect.
func checkImmutable(baseDir, headDir string) []error {
	versions, err := manifestVersions(baseDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return []error{fmt.Errorf("base: %w", err)}
	}

	// Rows name paths relative to the bundle; reading through os.Root keeps a row that
	// climbs out of it ("../x") from reading anything outside.
	base, err := os.OpenRoot(baseDir)
	if err != nil {
		return []error{fmt.Errorf("base: %w", err)}
	}
	defer func() { _ = base.Close() }()
	head, err := os.OpenRoot(headDir)
	if errors.Is(err, os.ErrNotExist) {
		return []error{fmt.Errorf("bundle %s was deleted; its published manifests must stay", filepath.Base(headDir))}
	}
	if err != nil {
		return []error{err}
	}
	defer func() { _ = head.Close() }()

	var problems []error
	checked := make(map[string]bool)
	same := func(rel, what string) {
		if checked[rel] {
			return
		}
		checked[rel] = true
		before, err := base.ReadFile(rel)
		if err != nil {
			problems = append(problems, fmt.Errorf("base: %w", err))
			return
		}
		after, err := head.ReadFile(rel)
		switch {
		case errors.Is(err, os.ErrNotExist):
			problems = append(problems, fmt.Errorf("%s %s was deleted; a published %s must stay", what, rel, what))
		case err != nil:
			problems = append(problems, err)
		case !bytes.Equal(before, after):
			problems = append(problems, fmt.Errorf("%s %s was changed; publish a new file and a new manifest instead", what, rel))
		}
	}

	loader, err := local.New(local.Config{Root: baseDir})
	if err != nil {
		return []error{fmt.Errorf("base: %w", err)}
	}
	for _, v := range versions {
		same(artifact.ManifestPath(v), "manifest")
		cfg, err := artifact.LoadManifest(context.Background(), loader, v)
		if err != nil {
			problems = append(problems, fmt.Errorf("base: %w", err))
			continue
		}
		for _, row := range cfg.Artifacts {
			same(filepath.FromSlash(row.Path), "artifact")
		}
	}
	return problems
}
