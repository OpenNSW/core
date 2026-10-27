// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package artifact

import (
	"bytes"
	"container/list"
	"context"
	"sync"
)

// DefaultFileCacheBytes is how many bytes of artifact files a Set keeps unless
// WithFileCacheBytes says otherwise. One manifest's files come to a few megabytes, so this
// holds several versions' worth.
const DefaultFileCacheBytes = 64 << 20

// cachingLoader keeps the files it loads, by path, so an artifact is fetched from the source
// once however often it is resolved, as long as it stays in the cache. It is only correct for a
// source whose files never change once a manifest lists them, which is what versioned manifests
// promise (see Set).
//
// The cache holds at most maxBytes of file content and evicts the least recently used files to
// stay within it. A file larger than maxBytes is never kept. Only successful loads are kept: a
// missing file or a failed fetch is tried again next time. Two callers that miss on the same path
// at once may both fetch it; they store the same bytes.
type cachingLoader struct {
	next     Loader
	maxBytes int

	mu    sync.Mutex
	size  int
	order *list.List               // most recently used at the front; values are *cachedFile
	files map[string]*list.Element // path -> its element in order
}

type cachedFile struct {
	path string
	raw  []byte
}

func newCachingLoader(next Loader, maxBytes int) *cachingLoader {
	return &cachingLoader{next: next, maxBytes: maxBytes, order: list.New(), files: make(map[string]*list.Element)}
}

// Load returns a copy of the file, from the cache or the source, so a parser that keeps or
// changes its input cannot change what the next caller gets.
func (c *cachingLoader) Load(ctx context.Context, path string) ([]byte, error) {
	c.mu.Lock()
	if el, ok := c.files[path]; ok {
		c.order.MoveToFront(el)
		raw := bytes.Clone(el.Value.(*cachedFile).raw)
		c.mu.Unlock()
		return raw, nil
	}
	c.mu.Unlock()

	raw, err := c.next.Load(ctx, path)
	if err != nil {
		return nil, err
	}
	c.store(path, raw)
	return bytes.Clone(raw), nil
}

// store keeps a copy of raw under path, evicting the least recently used files to make room.
func (c *cachingLoader) store(path string, raw []byte) {
	if len(raw) > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.files[path]; ok {
		return // another caller stored it while this one fetched
	}
	for c.size+len(raw) > c.maxBytes {
		oldest := c.order.Back()
		f := oldest.Value.(*cachedFile)
		c.order.Remove(oldest)
		delete(c.files, f.path)
		c.size -= len(f.raw)
	}
	c.files[path] = c.order.PushFront(&cachedFile{path: path, raw: bytes.Clone(raw)})
	c.size += len(raw)
}
