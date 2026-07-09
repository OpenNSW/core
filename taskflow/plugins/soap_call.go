// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package plugins

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/OpenNSW/core/remote"
)

// SOAPInterpreter adapts a domain to the generic SOAP-call plugin: it builds
// the SOAP envelope from the task inputs and interprets the raw response into
// a record state plus fields to record into the output namespace.
type SOAPInterpreter interface {
	// BuildEnvelope returns the SOAP envelope XML for the operation, derived
	// from the task's mapped inputs. An error means the request cannot even be
	// built (e.g. an incomplete document); the plugin then skips the call and
	// hands the error straight to Interpret, so user-facing prose stays in the
	// domain.
	BuildEnvelope(operation string, inputs map[string]any) (string, error)

	// Interpret turns the call outcome into a record state ("" leaves the
	// state unchanged) and the fields to record into the output namespace.
	// callErr is the build or transport error (nil on success); resp is the
	// raw response, nil when the call never happened. A non-2xx status arrives
	// here as a response, not an error — SOAP faults carry meaningful bodies.
	Interpret(operation string, callErr error, resp *remote.RawResponse) (state string, out map[string]any)
}

// SOAPCallPlugin posts a SOAP envelope to a configured service and records the
// outcome. Transport (endpoint, timeout, mTLS client certificate) is resolved
// by remote.Manager from the services registry; envelope building and response
// interpretation are delegated to a SOAPInterpreter, so the plugin itself is
// domain-agnostic. It is an auto plugin: it always returns nil so the workflow
// advances, and the interpreter's recorded flags drive the gateways.
type SOAPCallPlugin struct {
	manager     *remote.Manager
	interpreter SOAPInterpreter
}

// NewSOAPCallPlugin creates a new SOAPCallPlugin bound to an interpreter.
func NewSOAPCallPlugin(manager *remote.Manager, interp SOAPInterpreter) TaskPlugin {
	return &SOAPCallPlugin{manager: manager, interpreter: interp}
}

// SOAPCallConfig holds properties decoded from the TaskTemplate's JSON
// configuration.
type SOAPCallConfig struct {
	ServiceID  string `json:"service_id"`
	Operation  string `json:"operation"`
	Path       string `json:"path,omitempty"`        // relative to the service URL; "" posts to the service URL itself
	SOAPAction string `json:"soap_action,omitempty"` // SOAP 1.1 action; quoted automatically, "" sends SOAPAction: ""
}

func (p *SOAPCallPlugin) Execute(ctx PluginContext, configRaw json.RawMessage) error {
	if ctx.Record == nil {
		return fmt.Errorf("soap_call: task record is required")
	}
	if p.manager == nil {
		return fmt.Errorf("soap_call: remote manager is not initialized")
	}
	if p.interpreter == nil {
		return fmt.Errorf("soap_call: interpreter is not initialized")
	}

	var cfg SOAPCallConfig
	if len(configRaw) > 0 && string(configRaw) != "null" {
		if err := json.Unmarshal(configRaw, &cfg); err != nil {
			return fmt.Errorf("soap_call: invalid config: %w", err)
		}
	}
	if cfg.ServiceID == "" {
		return fmt.Errorf("soap_call: service_id is required")
	}

	var resp *remote.RawResponse
	envelope, callErr := p.interpreter.BuildEnvelope(cfg.Operation, ctx.Inputs)
	if callErr == nil {
		req := remote.RawRequest{
			Method:      "POST",
			Path:        cfg.Path,
			ContentType: "text/xml; charset=utf-8",
			Body:        []byte(envelope),
			Headers:     map[string]string{"SOAPAction": quoteSOAPAction(cfg.SOAPAction)},
		}
		resp, callErr = p.manager.CallRaw(ctx.Context, cfg.ServiceID, req)
	}

	state, out := p.interpreter.Interpret(cfg.Operation, callErr, resp)
	if out == nil {
		out = map[string]any{}
	}
	// Replace the namespace wholesale so stale keys from a prior loop
	// iteration never leak into this outcome.
	if ns := ctx.OutputNamespace; ns != "" {
		if ctx.Record.Data == nil {
			ctx.Record.Data = map[string]any{}
		}
		ctx.Record.Data[ns] = out
	}
	if state != "" {
		ctx.Record.State = state
	}

	if callErr != nil {
		slog.WarnContext(ctx.Context, "soap_call: call failed", "task_id", ctx.Record.TaskID, "service_id", cfg.ServiceID, "operation", cfg.Operation, "error", callErr)
	} else {
		slog.InfoContext(ctx.Context, "soap_call: call completed", "task_id", ctx.Record.TaskID, "service_id", cfg.ServiceID, "operation", cfg.Operation, "status", rawStatus(resp))
	}
	return nil
}

func rawStatus(resp *remote.RawResponse) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

// quoteSOAPAction wraps the action in the double quotes SOAP 1.1 requires —
// an empty action is sent as SOAPAction: "" — leaving an already-quoted
// config value untouched.
func quoteSOAPAction(action string) string {
	if len(action) >= 2 && action[0] == '"' && action[len(action)-1] == '"' {
		return action
	}
	return `"` + action + `"`
}
