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

// UserInputConfig can be expanded if we need custom properties per form step
type UserInputConfig struct {
	StatusOverride string `json:"status_override,omitempty"`
}

func (p *UserInputPlugin) Execute(ctx PluginContext, configRaw json.RawMessage) error {
	status := "PENDING_USER"

	if len(configRaw) > 0 && string(configRaw) != "null" {
		var cfg UserInputConfig
		if err := json.Unmarshal(configRaw, &cfg); err == nil && cfg.StatusOverride != "" {
			status = cfg.StatusOverride
		}
	}

	ctx.Record.Status = status
	log.Printf("[Plugin: generic_user_input] Task %s waiting for user interaction at node %s", ctx.Record.TaskID, ctx.Record.ActiveActivityID)
	return nil
}
