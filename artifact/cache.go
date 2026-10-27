// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package artifact

import (
	"bytes"
	"context"
	"sync"
)

// cachingLoader keeps every file it loads, by path, so an artifact is fetched from the source
// once per process however often it is resolved. It is only correct for a source whose files
// never change once a manifest lists them, which is what versioned manifests promise (see Set).
//
// Only successful loads are kept: a missing file or a failed fetch is tried again next time.
// Two callers that miss on the same path at once may both fetch it; they store the same bytes.
// Entries are never evicted. A process only holds the files its flows resolve, and the
// published files are small configuration documents.
type cachingLoader struct {
	next Loader

	mu    sync.RWMutex
	files map[string][]byte
}

func newCachingLoader(next Loader) *cachingLoader {
	return &cachingLoader{next: next, files: make(map[string][]byte)}
}

// Load returns a copy of the cached file, so a parser that keeps or changes its input cannot
// change what the next caller gets.
func (c *cachingLoader) Load(ctx context.Context, path string) ([]byte, error) {
	c.mu.RLock()
	raw, ok := c.files[path]
	c.mu.RUnlock()
	if ok {
		return bytes.Clone(raw), nil
	}

	raw, err := c.next.Load(ctx, path)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.files[path] = bytes.Clone(raw)
	c.mu.Unlock()
	return raw, nil
}
