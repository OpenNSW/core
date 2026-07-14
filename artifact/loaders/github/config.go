// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package github

import (
	"fmt"
	"net/http"
)

// defaultBaseURL is the public GitHub REST API root. Override via Config.BaseURL
// for GitHub Enterprise Server.
const defaultBaseURL = "https://api.github.com"

// defaultRawBaseURL is the public GitHub raw-content host, used when
// Config.UseRawHost is set. Override via Config.RawBaseURL.
const defaultRawBaseURL = "https://raw.githubusercontent.com"

// Config holds configuration for the GitHub artifact loader.
//
// This is owned by the github package (mirroring temporal.Config and the local
// loader), so the package controls the shape and semantics of its own
// configuration.
type Config struct {
	// Owner is the repository owner (user or organization). Required.
	Owner string
	// Repo is the repository name. Required.
	Repo string
	// Ref is the branch, tag, or commit SHA to read from. Required. A loader
	// instance is pinned to one ref, so the manifest and the artifacts it
	// catalogs are always fetched from the same source. Prefer an immutable
	// ref (a tag or SHA) for reproducibility; a branch is only useful once the
	// registry can reload.
	Ref string
	// BasePath is an optional in-repo directory prefix that every path is
	// resolved against — the GitHub analog of local.Root. It lets one repo hold
	// configs for several deployments (e.g. "deployment-a"). Empty means the
	// repository root.
	BasePath string
	// Token is an optional GitHub token. It is required for private
	// repositories and lifts the unauthenticated rate limit; omit it for public
	// repositories.
	Token string
	// BaseURL is an optional REST API root, for GitHub Enterprise Server. It
	// defaults to https://api.github.com. Ignored when UseRawHost is set.
	BaseURL string
	// UseRawHost fetches from the raw-content host
	// (https://raw.githubusercontent.com) instead of the REST Contents API.
	// The raw host is not subject to the REST rate limit and needs no token,
	// so it is a good fit for public repositories at higher volume. Note that
	// it caches branch refs for a few minutes, so prefer an immutable Ref (a
	// tag or SHA) with it; private-repository access should use the default
	// Contents API path.
	UseRawHost bool
	// RawBaseURL is an optional raw-content host root used when UseRawHost is
	// set. It defaults to https://raw.githubusercontent.com.
	RawBaseURL string
	// HTTPClient is an optional client used for requests. It defaults to
	// http.DefaultClient; inject one to set timeouts or to point tests at a
	// stub server.
	HTTPClient *http.Client
}

// Validate ensures the GitHub loader configuration is usable. It reports
// misconfiguration at construction time rather than deferring it to the first
// Load call.
func (c Config) Validate() error {
	if c.Owner == "" {
		return fmt.Errorf("github loader: Owner is required")
	}
	if c.Repo == "" {
		return fmt.Errorf("github loader: Repo is required")
	}
	if c.Ref == "" {
		return fmt.Errorf("github loader: Ref is required")
	}
	return nil
}
