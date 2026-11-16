// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package remote

// Log attribute keys. The HTTP ones follow OpenTelemetry semantic conventions, so
// log records line up with the spans and metrics an instrumented transport emits.
const (
	attrService       = "remote.service"
	attrMethod        = "http.request.method"
	attrServerAddress = "server.address"
	attrURLPath       = "url.path"
	attrStatusCode    = "http.response.status_code"
	attrResendCount   = "http.request.resend_count"
	attrErrorType     = "error.type"
)
