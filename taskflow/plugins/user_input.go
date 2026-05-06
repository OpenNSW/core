package plugins

import (
	"encoding/json"
	"log"
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
	log.Printf("[Plugin: generic_user_input] Task %s waiting for user interaction (form: %s) at node %s", ctx.Record.TaskID, ctx.Record.UserFormID, ctx.Record.ActiveActivityID)
	return nil
}
