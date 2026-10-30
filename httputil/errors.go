// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package httputil

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"
)

// Category classifies an Error and decides its HTTP status, so handlers never
// pick a status by hand. Use a predefined category, or declare one with
// NewCategory for a status they don't cover.
type Category struct {
	name   string
	status int
}

// NewCategory declares a category written with status, which must be a 4xx
// or 5xx code. Declare it once, as a package-level value:
//
//	var CategoryTooEarly = httputil.NewCategory("tooEarly", http.StatusTooEarly)
func NewCategory(name string, status int) Category {
	return Category{name: name, status: status}
}

var (
	CategoryInvalid              = NewCategory("invalid", http.StatusBadRequest)
	CategoryUnauthenticated      = NewCategory("unauthenticated", http.StatusUnauthorized)
	CategoryForbidden            = NewCategory("forbidden", http.StatusForbidden)
	CategoryNotFound             = NewCategory("notFound", http.StatusNotFound)
	CategoryConflict             = NewCategory("conflict", http.StatusConflict)
	CategoryTooLarge             = NewCategory("tooLarge", http.StatusRequestEntityTooLarge)
	CategoryUnsupportedMediaType = NewCategory("unsupportedMediaType", http.StatusUnsupportedMediaType)
	CategoryUnprocessable        = NewCategory("unprocessable", http.StatusUnprocessableEntity)
	CategoryTooManyRequests      = NewCategory("tooManyRequests", http.StatusTooManyRequests)
	CategoryUnavailable          = NewCategory("unavailable", http.StatusServiceUnavailable)
	CategoryInternal             = NewCategory("internal", http.StatusInternalServerError)
)

// Status returns the HTTP status for c. A zero Category, or one whose status
// is not 4xx or 5xx, is 500, so a missing or mistyped category fails loudly
// instead of passing as a client error or a success.
func (c Category) Status() int {
	if !validErrorStatus(c.status) {
		return http.StatusInternalServerError
	}
	return c.status
}

// String returns the category's name.
func (c Category) String() string {
	return c.name
}

func validErrorStatus(status int) bool {
	return status >= http.StatusBadRequest && status <= 599
}

// Error is a client-facing API error. Declare each one once, as a
// package-level value, and return it as an error with &ErrX, ErrX.WithParams,
// ErrX.WithDetails or ErrX.Wrap:
//
//	var ErrMemberNotFound = httputil.Error{
//		Code:     "PB-1003",
//		Category: httputil.CategoryNotFound,
//		Message:  httputil.Message{Key: "pb.member.not_found", DefaultValue: "Member not found"},
//		Description: httputil.Message{
//			Key:          "pb.member.not_found_description",
//			DefaultValue: "No member has the ID {{memberId}}",
//		},
//	}
//
//	return ErrMemberNotFound.WithParams(map[string]string{"memberId": id})
//
// errors.Is(err, &ErrX) matches any error derived from ErrX, since errors
// are compared by Code.
type Error struct {
	// Code is stable and unique, e.g. "PB-1003": an upper-case prefix per
	// service or package, a hyphen, then 4 digits.
	Code string
	// Category decides the HTTP status.
	Category Category
	// Message is a short title, e.g. "Member not found".
	Message Message
	// Description is the cause or what to do, e.g. "No member has the ID
	// {{memberId}}". It is optional.
	Description Message

	details []FieldError
	cause   error
}

// FieldError reports one invalid input field.
type FieldError struct {
	Field   string  `json:"field"` // JSON path, e.g. "contact.email"
	Code    string  `json:"code"`
	Message Message `json:"message"`
}

// ErrInternal is written for every server failure: any error that is not an
// *Error. Packages declare client errors only and let WriteError fall back to
// this one.
var ErrInternal = Error{
	Code:     "SYS-5000",
	Category: CategoryInternal,
	Message:  Message{Key: "sys.internal", DefaultValue: "Internal server error"},
	Description: Message{
		Key:          "sys.internal_description",
		DefaultValue: "An error occurred while processing your request",
	},
}

// WithParams returns a copy of e with params filling the placeholders of both
// Message and Description. Each message keeps only the params it uses.
func (e Error) WithParams(params map[string]string) *Error {
	e.Message = e.Message.WithParams(params)
	e.Description = e.Description.WithParams(params)
	return &e
}

// WithDetails returns a copy of e with details added, for reporting every
// invalid field of a request at once.
func (e Error) WithDetails(details ...FieldError) *Error {
	e.details = slices.Concat(e.details, details)
	return &e
}

// Wrap returns a copy of e that keeps cause for logging. WriteError logs the
// cause but never sends it to the client.
func (e Error) Wrap(cause error) *Error {
	e.cause = cause
	return &e
}

// Error implements error. The text is for logs, not for clients.
func (e *Error) Error() string {
	s := e.Code + ": " + e.Message.String()
	if e.cause != nil {
		s += ": " + e.cause.Error()
	}
	return s
}

// Unwrap returns the cause given to Wrap, if any.
func (e *Error) Unwrap() error {
	return e.cause
}

// Is reports whether target is an *Error with the same Code, so errors.Is
// matches a declared error however it was parameterized or wrapped.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// errorBody is the JSON shape of an API error response.
type errorBody struct {
	Code          string       `json:"code"`
	Message       Message      `json:"message"`
	Description   Message      `json:"description,omitzero"`
	Details       []FieldError `json:"details"`
	CorrelationID string       `json:"correlationId,omitempty"`
}

// WriteError writes err as an API error. An *Error (found with errors.As) is
// written with its category's status. Any other error, including nil, is
// logged with the request's correlation ID and written as ErrInternal, so
// internal error text never reaches the client.
//
// An *Error is logged when it carries more than its declaration: a cause
// from Wrap, or context added by wrapping it in another error. Server
// categories (internal, unavailable) log at error level, client categories
// at warn level.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	correlationID := CorrelationID(r)

	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr == nil {
		slog.ErrorContext(r.Context(), "httputil: unhandled error", "error", err, "correlationId", correlationID)
		apiErr = &ErrInternal
	} else if apiErr.cause != nil || err != error(apiErr) {
		level := slog.LevelWarn
		if status := apiErr.Category.Status(); status >= http.StatusInternalServerError {
			level = slog.LevelError
		}
		slog.Log(r.Context(), level, "httputil: request failed",
			"code", apiErr.Code, "category", apiErr.Category.String(), "error", err, "correlationId", correlationID)
	}

	details := apiErr.details
	if details == nil {
		details = []FieldError{}
	}
	JSON(w, apiErr.Category.Status(), errorBody{
		Code:          apiErr.Code,
		Message:       apiErr.Message,
		Description:   apiErr.Description,
		Details:       details,
		CorrelationID: correlationID,
	})
}
