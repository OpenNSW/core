// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package payment

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContextWithRequest_RoundTrip(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	ctx := ContextWithRequest(context.Background(), req)

	got := RequestFromContext(ctx)
	assert.Same(t, req, got, "RequestFromContext should return the exact same *http.Request instance")
}

func TestRequestFromContext_AbsentReturnsNil(t *testing.T) {
	assert.Nil(t, RequestFromContext(context.Background()))
}
