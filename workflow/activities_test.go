// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAdminParkActivityNoHandlerRegistered pins AdminParkActivity's no-op behavior when no
// AdminParkHandler is registered, which is the default for any host app that hasn't opted in
// to park notifications. This is asserted directly against the activity function rather than
// through a full workflow run: notifyAdminPark fires it from a background coroutine the
// workflow never waits on, so a workflow-level test can complete (and snapshot AuditTrail)
// before that coroutine's activity call — and any failure it would have recorded — ever runs.
func TestAdminParkActivityNoHandlerRegistered(t *testing.T) {
	acts := &Activities{}
	require.NoError(t, acts.AdminParkActivity(context.Background(), AdminParkPayload{NodeID: "task"}))
}

// TestAdminParkActivityPropagatesHandlerError confirms AdminParkActivity, unlike the no-handler
// case above, does surface a registered handler's own error back to the caller (notifyAdminPark
// is what then records that on AuditTrail instead of failing the workflow).
func TestAdminParkActivityPropagatesHandlerError(t *testing.T) {
	handlerErr := errors.New("notification sink unavailable")
	acts := &Activities{
		AdminParkHandler: func(AdminParkPayload) error {
			return handlerErr
		},
	}
	require.ErrorIs(t, acts.AdminParkActivity(context.Background(), AdminParkPayload{NodeID: "task"}), handlerErr)
}
