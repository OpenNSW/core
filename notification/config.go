// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package notification

import "errors"

var ErrConfigPathRequired = errors.New("notification config path is required")

// Config holds the file-based notifications subsystem configuration.
type Config struct {
	Path string
}

// Validate returns ErrConfigPathRequired when Path is empty.
func (c Config) Validate() error {
	if c.Path == "" {
		return ErrConfigPathRequired
	}
	return nil
}
