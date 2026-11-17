// Package audit records security-relevant application events as structured
// logs with Category. Bootstrap points LogSink at the OTLP handler only, so
// records are omitted from the process (pod) log.
//
// Call sites depend on Auditor. Bootstrap constructs a Client, registers
// LogSink, and passes the Client as Auditor. Core payment and storage emit
// shared/audit.Event values; CoreAdapter maps those into this package's Event
// vocabulary before they reach the Client.
//
// Authenticated SLC Edge deliveries emit CUSTOMS_EVENT records targeting a
// CUSTOMS_DECLARATION or CARGO_DISPATCH_NOTE by its local record ID. Metadata
// keeps eventType, httpStatus, caseId and available edgeId/documentRef values.
// Success means callback processing was accepted, including duplicate deliveries
// and handled Customs rejections (integrated=false). Unresolved documents leave
// the target empty; malformed/unknown callbacks use CUSTOMS_CALLBACK. Payloads
// and Customs error descriptions are not copied into these audit records.
// Callback event names are allowlisted (UNKNOWN otherwise); callback-controlled
// edgeId and documentRef values over 128 bytes are omitted without changing lookups.
//
// RegisterSink is intended for composition-root wiring only. Audit itself is
// nil-receiver safe so optional call sites can leave the dependency unset.
package audit
