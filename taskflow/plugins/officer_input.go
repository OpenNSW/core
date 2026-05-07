package plugins

import (
	"encoding/json"
	"log"
)

// OfficerInputPlugin implements a reviewer/officer action step.
type OfficerInputPlugin struct{}

func NewOfficerInputPlugin() *OfficerInputPlugin {
	return &OfficerInputPlugin{}
}

func (p *OfficerInputPlugin) Name() string {
	return "generic_officer_input"
}

// OfficerInputConfig holds properties specific to the officer input step.
type OfficerInputConfig struct {
	StatusOverride     string `json:"status_override,omitempty"`
	OfficerJsonFormsID string `json:"officer_jsonforms_id,omitempty"`
}

func (p *OfficerInputPlugin) Execute(ctx PluginContext, configRaw json.RawMessage) error {
	status := "QUEUED_EXTERNALLY"

	if len(configRaw) > 0 && string(configRaw) != "null" {
		var cfg OfficerInputConfig
		if err := json.Unmarshal(configRaw, &cfg); err == nil {
			if cfg.StatusOverride != "" {
				status = cfg.StatusOverride
			}
			if cfg.OfficerJsonFormsID != "" {
				ctx.Record.ReviewerFormID = cfg.OfficerJsonFormsID
			}
		}
	}

	ctx.Record.Status = status
	log.Printf("[Plugin: generic_officer_input] Task %s waiting for officer interaction (form: %s) at node %s", ctx.Record.TaskID, ctx.Record.ReviewerFormID, ctx.Record.SubTaskNodeID)
	return nil
}
