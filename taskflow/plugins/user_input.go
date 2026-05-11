package plugins

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/OpenNSW/nsw-task-flow/store"
)

// UserInputPlugin implements a standard human interaction / form submission step.
type UserInputPlugin struct{}

func NewUserInputPlugin() *UserInputPlugin {
	return &UserInputPlugin{}
}

func (p *UserInputPlugin) Name() string {
	return "generic_user_input"
}

// UserInputConfig holds properties specific to the user input step
type UserInputConfig struct {
	StatusOverride  string `json:"status_override,omitempty"`
	UserJsonFormsID string `json:"user_jsonforms_id,omitempty"`
}

func (p *UserInputPlugin) Execute(ctx PluginContext, configRaw json.RawMessage) error {
	status := "PENDING_USER"

	if len(configRaw) > 0 && string(configRaw) != "null" {
		var cfg UserInputConfig
		if err := json.Unmarshal(configRaw, &cfg); err == nil {
			if cfg.StatusOverride != "" {
				status = cfg.StatusOverride
			}
			if cfg.UserJsonFormsID != "" {
				ctx.Record.UserFormID = cfg.UserJsonFormsID
			}
		}
	}

	ctx.Record.Status = status
	log.Printf("[Plugin: generic_user_input] Task %s waiting for user interaction (form: %s) at node %s", ctx.Record.TaskID, ctx.Record.UserFormID, ctx.Record.SubTaskNodeID)
	return ErrSuspended
}

func (p *UserInputPlugin) Render(configRaw json.RawMessage, record store.TaskRecord, getTemplate TemplateRetriever) (map[string]any, error) {
	var cfg UserInputConfig
	if len(configRaw) > 0 && string(configRaw) != "null" {
		_ = json.Unmarshal(configRaw, &cfg)
	}

	renderInfo := map[string]any{
		"form_type": "user_input",
	}

	if cfg.UserJsonFormsID != "" {
		raw, exists := getTemplate(cfg.UserJsonFormsID)
		if !exists {
			return nil, fmt.Errorf("user json form template %q not found", cfg.UserJsonFormsID)
		}
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, fmt.Errorf("failed to unmarshal user json form template %q: %w", cfg.UserJsonFormsID, err)
		}
		renderInfo["user_form_schema"] = decoded
	}
	return renderInfo, nil
}
