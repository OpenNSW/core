// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package payment

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewWebhookVerificationError(t *testing.T) {
	err := NewWebhookVerificationError("invalid signature")
	assert.ErrorIs(t, err, ErrWebhookVerificationFailed)
	assert.Contains(t, err.Error(), "invalid signature")
}
