package tasktypes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/OpenNSW/core/notification"
)

// sender dispatches a notification request. It is satisfied by
// *notification.Manager; declaring it as an interface keeps NotificationPlugin
// unit-testable without a live SMS/email provider.
type sender interface {
	Send(ctx context.Context, req notification.Request) error
}

// NotificationPlugin dispatches a single SMS or email through the notification
// manager when a NOTIFICATION task node activates. The recipient is read from
// the task inputs (wired via the node's input_mapping); the message body/subject
// are taken from inputs (preferred) or config (fallback). The send is
// synchronous, so Execute returns nil on success rather than ErrSuspended.
//
// The request is validated before dispatch so config errors (bad channel,
// empty body) fail the step in every mode; only transport errors from
// Manager.Send are swallowed in devMode, so local workflows progress when the
// gateway is unreachable (mirrors dispatchHelper).
type NotificationPlugin struct {
	sender  sender
	devMode bool
}

// NewNotificationPlugin builds the plugin. s must be non-nil.
func NewNotificationPlugin(s sender, devMode bool) *NotificationPlugin {
	if s == nil {
		panic("sender is nil")
	}
	return &NotificationPlugin{sender: s, devMode: devMode}
}

type notificationConfig struct {
	Channel  string `json:"channel"`
	Subject  string `json:"subject,omitempty"`
	Body     string `json:"body,omitempty"`
	HTMLBody string `json:"html_body,omitempty"`
	TaskCode string `json:"task_code,omitempty"`
}

// Execute resolves the recipient from inputs, builds the request from
// inputs-or-config, dispatches via the manager, and writes a delivery summary
// to the active output namespace.
func (p *NotificationPlugin) Execute(ctx pluginContext, configRaw json.RawMessage) error {
	var cfg notificationConfig
	if err := json.Unmarshal(configRaw, &cfg); err != nil {
		return configError(fmt.Errorf("notification: invalid config: %w", err))
	}

	// Recipient comes from the workflow inputs only (input_mapping) — it's the
	// per-case applicant phone/email, never a static config value.
	raw, exists := ctx.Inputs["to"]
	if !exists {
		return inputError(errors.New(`notification: recipient input "to" is missing`))
	}
	to, ok := raw.(string)
	if !ok || to == "" {
		return inputError(fmt.Errorf("notification: recipient input %q must be a non-empty string, got %T", "to", raw))
	}

	req := notification.Request{
		Channel:  notification.ChannelType(cfg.Channel),
		To:       to,
		Subject:  pickString(ctx.Inputs, "subject", cfg.Subject),
		Body:     pickString(ctx.Inputs, "body", cfg.Body),
		HTMLBody: pickString(ctx.Inputs, "html_body", cfg.HTMLBody),
	}

	if err := req.Validate(); err != nil {
		return inputError(fmt.Errorf("notification: invalid request: %w", err))
	}

	slog.Info("taskv2 notification: dispatching",
		"taskId", ctx.Record.TaskID, "channel", req.Channel, "taskCode", cfg.TaskCode)

	status := "sent"
	if err := p.sender.Send(ctx.Context, req); err != nil {
		if !p.devMode {
			return classifyRemote(fmt.Errorf("notification: send: %w", err))
		}
		slog.Warn("taskv2 notification: send failed (dev mode — swallowing)",
			"taskId", ctx.Record.TaskID, "channel", req.Channel, "error", err)
		status = "skipped"
	}

	ctx.Record.State = "NOTIFIED"

	if ctx.OutputNamespace != "" {
		if ctx.Record.Data == nil {
			ctx.Record.Data = make(map[string]any)
		}
		out := map[string]any{
			"channel":   string(req.Channel),
			"to":        req.To,
			"status":    status,
			"task_code": cfg.TaskCode,
		}
		if status == "sent" {
			out["sent_at"] = time.Now().UTC().Format(time.RFC3339)
		}
		ctx.Record.Data[ctx.OutputNamespace] = out
	}

	return nil
}

// pickString returns inputs[key] when present as a non-empty string, else fallback.
func pickString(inputs map[string]any, key, fallback string) string {
	if v, ok := inputs[key]; ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return fallback
}
