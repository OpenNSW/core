// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package notification

import "errors"

// ErrProvidersRequired is returned by Config.Validate when Providers is empty.
var ErrProvidersRequired = errors.New("notification providers configuration is required")

// Config holds the notifications subsystem configuration: one settings block
// per channel, keyed by ChannelType (e.g. "email", "sms"), handed to the
// matching Provider's Configure call.
//
// Config carries yaml struct tags, so it can be embedded in a larger
// application config struct and populated generically (e.g. via
// yaml.Unmarshal, or configyaml.LoadAndExpand for {{env:}}/{{file:}} secret
// placeholders within a provider's block) instead of pointing at its own
// standalone config file.
type Config struct {
	Providers map[ChannelType]map[string]any `yaml:"providers"`
}

// Validate returns ErrProvidersRequired when Providers is empty.
func (c Config) Validate() error {
	if len(c.Providers) == 0 {
		return ErrProvidersRequired
	}
	return nil
}
