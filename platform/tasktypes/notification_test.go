// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package tasktypes

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewNotificationPlugin_NilSenderPanics(t *testing.T) {
	assert.Panics(t, func() { NewNotificationPlugin(nil, false) })
}
