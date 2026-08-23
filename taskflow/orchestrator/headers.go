// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package orchestrator

import (
	"context"
	"fmt"

	"github.com/OpenNSW/core/remote"
)

// HeadersInputKey is the reserved task input carrying request headers for
// whatever outbound call a plugin makes.
//
// It exists because a header value can belong to the case being processed rather
// than to the service — the identifier a provider issued for the organisation a
// submission is filed under, a reference the workflow resolved earlier — while
// the plugins that make the call are generic and model only a body. A workflow
// artifact maps the variable it already has onto a header name:
//
//	"input_mapping": {
//	  "company.data.partner_client_key?": "_headers.x-partner-client-key"
//	}
//
// StartSubTask puts these on the plugin's context (see ContextWithInputHeaders),
// and remote applies them to every request made with it. So carrying one is an
// artifact change: no plugin, interpreter or service configuration has to know
// the header exists.
//
// A header the plugin can build itself belongs on remote.Request instead, which
// wins over these. Authentication is applied last of all, so nothing here can
// shadow it.
const HeadersInputKey = "_headers"

// ContextWithInputHeaders returns ctx carrying the headers named under
// HeadersInputKey in inputs, or ctx unchanged when there are none.
//
// Only the reserved key is this package's business. What counts as a sendable
// header — and that a blank optional value is dropped rather than fatal — is
// remote.ContextWithHeaderValues, which owns request headers.
func ContextWithInputHeaders(ctx context.Context, inputs map[string]any) (context.Context, error) {
	raw, ok := inputs[HeadersInputKey]
	if !ok || raw == nil {
		return ctx, nil
	}

	values, ok := raw.(map[string]any)
	if !ok {
		return ctx, fmt.Errorf("task input %q must be a map of header name to value, got %T", HeadersInputKey, raw)
	}

	// A malformed entry fails the subtask: an artifact that names a header the
	// engine cannot pass is a wiring mistake, and finding out from a provider's
	// rejection is worse than failing here.
	headerCtx, err := remote.ContextWithHeaderValues(ctx, values)
	if err != nil {
		return ctx, fmt.Errorf("task input %q: %w", HeadersInputKey, err)
	}
	return headerCtx, nil
}
