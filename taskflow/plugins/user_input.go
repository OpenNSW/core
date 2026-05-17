package plugins

import (
	"encoding/json"
	"log"
)

// UserInputPlugin implements a standard human interaction / form submission step.
type UserInputPlugin struct{}

func NewUserInputPlugin() TaskPlugin {
	return &UserInputPlugin{}
}

// UserInputConfig holds properties specific to the user input step
type UserInputConfig struct {
	StatusOverride string `json:"status_override,omitempty"`
}

func (p *UserInputPlugin) Execute(ctx PluginContext, configRaw json.RawMessage) error {
	status := "PENDING_USER"

	if len(configRaw) > 0 && string(configRaw) != "null" {
		var cfg UserInputConfig
		if err := json.Unmarshal(configRaw, &cfg); err == nil {
			if cfg.StatusOverride != "" {
				status = cfg.StatusOverride
			}
		}
	}

	ctx.Record.State = status
	log.Printf("[Plugin: generic_user_input] Task %s, at node %s", ctx.Record.TaskID, ctx.Record.SubTaskNodeID)
	return ErrSuspended
}
