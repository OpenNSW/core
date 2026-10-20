// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package artifact

import "context"

type manifestVersionKey struct{}

// WithManifestVersion returns a context carrying manifest version v. Pass versions explicitly
// where a signature allows it; the context carries one across a boundary that knows nothing of
// artifacts, such as a template provider called by a renderer.
func WithManifestVersion(ctx context.Context, v int) context.Context {
	return context.WithValue(ctx, manifestVersionKey{}, v)
}

// ManifestVersionFrom returns the manifest version WithManifestVersion stored in ctx, and
// whether one was stored.
func ManifestVersionFrom(ctx context.Context) (int, bool) {
	v, ok := ctx.Value(manifestVersionKey{}).(int)
	return v, ok
}
