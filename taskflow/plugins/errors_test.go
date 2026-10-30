// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.temporal.io/sdk/temporal"

	"github.com/OpenNSW/core/remote"
	"github.com/OpenNSW/core/taskflow/store"
)

func TestRetryableStatus(t *testing.T) {
	for code, want := range map[int]bool{
		http.StatusBadRequest:          false,
		http.StatusUnauthorized:        false,
		http.StatusForbidden:           false,
		http.StatusNotFound:            false,
		http.StatusConflict:            false,
		http.StatusUnprocessableEntity: false,
		http.StatusRequestTimeout:      true,
		http.StatusTooManyRequests:     true,
		http.StatusInternalServerError: true,
		http.StatusBadGateway:          true,
		http.StatusServiceUnavailable:  true,
		http.StatusGatewayTimeout:      true,
	} {
		if got := RetryableStatus(code); got != want {
			t.Errorf("RetryableStatus(%d) = %v, want %v", code, got, want)
		}
	}
}

func TestPermanent(t *testing.T) {
	cause := errors.New("missing field")
	err := fmt.Errorf("step failed: %w", Permanent(ErrorTypeInput, cause))

	if !IsPermanent(err) {
		t.Error("IsPermanent = false for a wrapped Permanent error")
	}
	if !errors.Is(err, cause) {
		t.Error("Permanent must keep its cause reachable with errors.Is")
	}
	if errType, _ := PermanentType(err); errType != ErrorTypeInput {
		t.Errorf("type = %q, want %q", errType, ErrorTypeInput)
	}
	if err.Error() != "step failed: missing field" {
		t.Errorf("message = %q, want the wrapped message unchanged", err.Error())
	}
	if Permanent(ErrorTypeInput, nil) != nil {
		t.Error("Permanent(nil) must be nil")
	}
	if IsPermanent(errors.New("timeout")) {
		t.Error("IsPermanent = true for an unmarked error")
	}
	// A plugin may also return Temporal's own non-retryable error.
	if errType, ok := PermanentType(fmt.Errorf("x: %w", temporal.NewNonRetryableApplicationError("gone", "Gone", nil))); !ok || errType != "Gone" {
		t.Errorf("PermanentType of a non-retryable ApplicationError = (%q, %v)", errType, ok)
	}
}

func TestClassifyRemote(t *testing.T) {
	for name, tc := range map[string]struct {
		err       error
		permanent bool
	}{
		"400":           {&remote.RemoteError{StatusCode: http.StatusBadRequest, Wrapped: remote.ErrBadRequest}, true},
		"404":           {&remote.RemoteError{StatusCode: http.StatusNotFound, Wrapped: remote.ErrNotFound}, true},
		"429":           {&remote.RemoteError{StatusCode: http.StatusTooManyRequests}, false},
		"503":           {&remote.RemoteError{StatusCode: http.StatusServiceUnavailable}, false},
		"wrapped 422":   {fmt.Errorf("submit: %w", &remote.RemoteError{StatusCode: http.StatusUnprocessableEntity}), true},
		"timeout":       {remote.ErrTimeout, false},
		"network error": {fmt.Errorf("%w: connection refused", remote.ErrRequestFailed), false},
	} {
		t.Run(name, func(t *testing.T) {
			got := ClassifyRemote(tc.err)
			if IsPermanent(got) != tc.permanent {
				t.Errorf("IsPermanent(ClassifyRemote(%v)) = %v, want %v", tc.err, !tc.permanent, tc.permanent)
			}
			if !errors.Is(got, tc.err) {
				t.Error("ClassifyRemote must keep the error reachable with errors.Is")
			}
		})
	}
}

// The dispatcher gives up at once on a status sending again can't change, and leaves the rest to
// be retried.
func TestDefaultHTTPDispatcher_ClassifiesTheStatus(t *testing.T) {
	for code, permanent := range map[int]bool{
		http.StatusBadRequest:         true,
		http.StatusNotFound:           true,
		http.StatusTooManyRequests:    false,
		http.StatusServiceUnavailable: false,
	} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(code)
			}))
			defer srv.Close()

			err := DefaultHTTPDispatcher(context.Background(), srv.URL, "token", map[string]any{})
			if err == nil {
				t.Fatal("expected an error")
			}
			if IsPermanent(err) != permanent {
				t.Errorf("IsPermanent = %v, want %v (err: %v)", !permanent, permanent, err)
			}
		})
	}
}

// A connection that fails is retried: the receiver may be back on the next attempt.
func TestDefaultHTTPDispatcher_NetworkErrorIsRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	err := DefaultHTTPDispatcher(context.Background(), url, "token", map[string]any{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if IsPermanent(err) {
		t.Errorf("a network error must stay retryable, got %v", err)
	}
}

// A plugin's configuration error is the same on every attempt, so it is Permanent.
func TestPlugins_ConfigErrorsArePermanent(t *testing.T) {
	noDispatch := func(context.Context, string, string, map[string]any) error { return nil }
	ctx := PluginContext{Context: context.Background(), Record: &store.TaskRecord{TaskID: testTaskID, ActiveStepID: testStepID}}

	for name, tc := range map[string]struct {
		plugin TaskPlugin
		config string
	}{
		"api_call malformed":        {NewAPICallPlugin(noDispatch), `{`},
		"api_call no url":           {NewAPICallPlugin(noDispatch), `{}`},
		"external_review malformed": {NewExternalReviewPlugin(noDispatch), `{`},
		"external_review no url":    {NewExternalReviewPlugin(noDispatch), `{}`},
		"payment malformed":         {NewPaymentPlugin(noDispatch), `{`},
		"payment no url":            {NewPaymentPlugin(noDispatch), `{}`},
	} {
		t.Run(name, func(t *testing.T) {
			err := tc.plugin.Execute(ctx, json.RawMessage(tc.config))
			if !IsPermanent(err) {
				t.Errorf("err = %v, want a Permanent error", err)
			}
		})
	}
}

// A dispatch that fails keeps the dispatcher's verdict through the plugin's wrapping.
func TestPlugins_KeepTheDispatchersVerdict(t *testing.T) {
	ctx := PluginContext{Context: context.Background(), Record: &store.TaskRecord{TaskID: testTaskID, ActiveStepID: testStepID}}
	rejected := func(context.Context, string, string, map[string]any) error {
		return Permanent(ErrorTypeRejected, errors.New("external system returned 400"))
	}
	unavailable := func(context.Context, string, string, map[string]any) error {
		return errors.New("external system returned 503")
	}

	if err := NewExternalReviewPlugin(rejected).Execute(ctx, json.RawMessage(`{"external_url": "http://x"}`)); !IsPermanent(err) {
		t.Errorf("rejected dispatch: err = %v, want Permanent", err)
	}
	if err := NewExternalReviewPlugin(unavailable).Execute(ctx, json.RawMessage(`{"external_url": "http://x"}`)); err == nil || IsPermanent(err) {
		t.Errorf("unavailable dispatch: err = %v, want a retryable error", err)
	}
}
