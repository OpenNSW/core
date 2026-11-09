// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package extensions

import (
	"context"
	"testing"

	"github.com/OpenNSW/core/artifact/adapter/types"
)

func TestInvocationFrom(t *testing.T) {
	if _, ok := InvocationFrom(context.Background()); ok {
		t.Error("a bare context reported an invocation")
	}

	want := Invocation{Phase: types.PhasePostResume, IdempotencyKey: "task/step/4/POST_RESUME"}
	got, ok := InvocationFrom(WithInvocation(context.Background(), want))
	if !ok || got != want {
		t.Errorf("InvocationFrom = %+v (present %v), want %+v", got, ok, want)
	}
}
