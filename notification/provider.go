// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package notification

import "context"

// Provider is implemented by each notification channel (email, SMS, etc.).
// A provider is built ready to send, from its own config type, by its
// constructor (e.g. providers.NewSMSProvider), and handed to NewManager.
type Provider interface {
	Type() ChannelType
	Send(ctx context.Context, req Request) error
}
