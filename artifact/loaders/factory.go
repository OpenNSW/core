// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package loaders selects and constructs the core/artifact Loader used
// to fetch artifacts (workflow definitions, templates, manifests) at startup.
//
// The concrete source is chosen at runtime by Config.Type. Rather than
// flattening every backend's settings into one struct, Config embeds each
// loader's own Config (local.Config, github.Config, s3.Config) verbatim — so
// each backend keeps ownership of its config shape and validation, exactly as
// core intends. Supported types: "local", "github", and "s3".
package loaders

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/loaders/github"
	"github.com/OpenNSW/core/artifact/loaders/local"
	"github.com/OpenNSW/core/artifact/loaders/s3"
)

// Supported loader types.
const (
	TypeLocal  = "local"
	TypeGitHub = "github"
	TypeS3     = "s3"
)

// supportedTypes lists the valid Type values, for error messages.
const supportedTypes = `"local", "github", or "s3"`

// Config selects a loader backend via Type and carries each backend's own
// config. Only the config for the selected Type is read.
type Config struct {
	// Type is the loader backend: "local", "github", or "s3".
	Type string

	// Local is used when Type == "local".
	Local local.Config
	// GitHub is used when Type == "github".
	GitHub github.Config
	// S3 is used when Type == "s3".
	S3 s3.Config
}

// Validate reports misconfiguration before New is called, delegating to the
// selected backend's own Config.Validate.
func (c Config) Validate() error {
	switch strings.TrimSpace(c.Type) {
	case TypeLocal:
		return c.Local.Validate()
	case TypeGitHub:
		return c.GitHub.Validate()
	case TypeS3:
		return c.S3.Validate()
	case "":
		return fmt.Errorf("artifact loader: Type is required")
	default:
		return fmt.Errorf("artifact loader: unsupported type %q (want %s)", c.Type, supportedTypes)
	}
}

// New builds the artifact.Loader selected by cfg.Type. It validates cfg first,
// so the type switch below only ever sees a supported Type.
func New(ctx context.Context, cfg Config) (artifact.Loader, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	var (
		loader artifact.Loader
		err    error
	)

	switch strings.TrimSpace(cfg.Type) {
	case TypeLocal:
		slog.InfoContext(ctx, "initializing local artifact loader", "root", cfg.Local.Root)
		loader, err = local.New(cfg.Local)
	case TypeGitHub:
		slog.InfoContext(ctx, "initializing github artifact loader",
			"owner", cfg.GitHub.Owner, "repo", cfg.GitHub.Repo, "ref", cfg.GitHub.Ref)
		loader, err = github.New(cfg.GitHub)
	case TypeS3:
		slog.InfoContext(ctx, "initializing s3 artifact loader",
			"bucket", cfg.S3.Bucket, "region", cfg.S3.Region, "endpoint", cfg.S3.Endpoint)
		loader, err = s3.New(ctx, cfg.S3)
	}
	if err != nil {
		// Backend constructors already prefix their errors (e.g. "s3 loader: …")
		// and Validate errors are returned verbatim above, so return err as-is
		// rather than double-prefixing.
		return nil, err
	}

	return loader, nil
}
