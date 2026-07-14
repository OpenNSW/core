// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package github provides an artifact Loader that fetches bytes from a GitHub
// repository over the REST Contents API, using only net/http (no GitHub SDK
// dependency).
package github

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/OpenNSW/core/artifact"
)

// rawMediaType asks the Contents API to return the file's raw bytes rather than
// a base64-encoded JSON envelope.
const rawMediaType = "application/vnd.github.raw"

// maxErrorBody bounds how much of an error response body we read back into an
// error message, so a large/hostile response can't blow up memory.
const maxErrorBody = 2 << 10 // 2 KiB

// Loader fetches artifact bytes from a single ref of a GitHub repository. It is
// safe for concurrent use: all fields are set at construction and never mutated.
type Loader struct {
	owner      string
	repo       string
	ref        string
	basePath   string
	token      string
	baseURL    string
	useRawHost bool
	rawBaseURL string
	httpClient *http.Client
}

// New validates cfg and constructs a Loader. It returns an error if the
// configuration is invalid, matching the temporal.NewClient(cfg) shape.
func New(cfg Config) (*Loader, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	rawBaseURL := cfg.RawBaseURL
	if rawBaseURL == "" {
		rawBaseURL = defaultRawBaseURL
	}
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &Loader{
		owner:      cfg.Owner,
		repo:       cfg.Repo,
		ref:        cfg.Ref,
		basePath:   strings.Trim(cfg.BasePath, "/"),
		token:      cfg.Token,
		baseURL:    strings.TrimRight(baseURL, "/"),
		useRawHost: cfg.UseRawHost,
		rawBaseURL: strings.TrimRight(rawBaseURL, "/"),
		httpClient: client,
	}, nil
}

// Load fetches the raw contents of p, resolved against the loader's BasePath and
// pinned Ref. A missing file is reported as artifact.ErrNotFound; other non-2xx
// responses and transport failures are returned as wrapped I/O errors.
func (l *Loader) Load(ctx context.Context, p string) ([]byte, error) {
	full, err := l.resolve(p)
	if err != nil {
		return nil, err
	}

	req, err := l.newRequest(ctx, full)
	if err != nil {
		return nil, fmt.Errorf("github loader: build request for %q: %w", p, err)
	}
	if l.token != "" {
		req.Header.Set("Authorization", "Bearer "+l.token)
	}

	resp, err := l.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github loader: get %q: %w", p, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("github loader: read body for %q: %w", p, err)
		}
		return data, nil
	case http.StatusNotFound:
		return nil, fmt.Errorf("%w: github %s/%s@%s path %q", artifact.ErrNotFound, l.owner, l.repo, l.ref, full)
	default:
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return nil, fmt.Errorf("github loader: get %q: unexpected status %s: %s",
			p, resp.Status, strings.TrimSpace(string(snippet)))
	}
}

// newRequest builds a GET request for the fully-resolved path full, using the
// raw-content host or the Contents API depending on the loader's mode. The two
// differ only in URL shape and headers; response handling is shared in Load.
func (l *Loader) newRequest(ctx context.Context, full string) (*http.Request, error) {
	if l.useRawHost {
		// https://raw.githubusercontent.com/{owner}/{repo}/{ref}/{path}
		// The ref sits in the path (so slashy branch names work) and no media
		// type is needed — the body is the raw file.
		u, err := url.Parse(l.rawBaseURL)
		if err != nil {
			return nil, fmt.Errorf("invalid raw base URL %q: %w", l.rawBaseURL, err)
		}
		u.Path = path.Join("/", u.Path, l.owner, l.repo, l.ref, full)
		return http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	}

	// {baseURL}/repos/{owner}/{repo}/contents/{path}?ref={ref}
	u, err := url.Parse(l.baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL %q: %w", l.baseURL, err)
	}
	u.Path = path.Join("/", u.Path, "repos", l.owner, l.repo, "contents", full)
	u.RawQuery = url.Values{"ref": {l.ref}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", rawMediaType)
	return req, nil
}

// resolve joins p onto the loader's BasePath and guards against a path that
// escapes it (e.g. via "..").
func (l *Loader) resolve(p string) (string, error) {
	full := path.Join(l.basePath, p)
	if l.basePath == "" {
		// With no base, reject any path that climbs above the repository root.
		if full == ".." || strings.HasPrefix(full, "../") {
			return "", fmt.Errorf("%w: path %q escapes repository root", artifact.ErrNotFound, p)
		}
	} else if full != l.basePath && !strings.HasPrefix(full, l.basePath+"/") {
		return "", fmt.Errorf("%w: path %q escapes base path %q", artifact.ErrNotFound, p, l.basePath)
	}
	if full == "" || full == "." {
		return "", fmt.Errorf("%w: empty path", artifact.ErrNotFound)
	}
	return full, nil
}
