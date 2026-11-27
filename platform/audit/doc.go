// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package audit records security-relevant application events as structured
// logs with Category. Bootstrap points LogSink at the OTLP handler only, so
// records are omitted from the process (pod) log.
//
// Call sites depend on Auditor. Bootstrap constructs a Client, registers
// LogSink, and passes the Client as Auditor. Core payment and storage emit
// shared/audit.Event values; CoreAdapter maps those into this package's Event
// vocabulary before they reach the Client.
//
// Each record names its actor from the request's principal: a machine client
// is SERVICE, a user is MEMBER with the handle of the organization unit they
// act for (actorOU), and a request with no principal is SYSTEM/anonymous. An
// event can name its own ActorType and ActorID instead, such as ADMIN for an
// admin user or SERVICE for a partner that signs its requests. An event about a
// case sets CaseID, which LogSink writes as caseID, so one query finds every
// record about that case.
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
// A callback held for retry is answered 202, and the record of the delivery that
// started the retry carries heldForRetry. When the retries end, a second record
// on the same target, actor and trace carries retryAttempts and retryOutcome
// (applied, failed, refused or gave_up), with no httpStatus.
//
// RegisterSink is intended for composition-root wiring only. Audit itself is
// nil-receiver safe so optional call sites can leave the dependency unset.
package audit
