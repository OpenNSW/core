package plugins

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/OpenNSW/nsw-task-flow/store"
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
	return ErrSuspended
}

func (p *OfficerInputPlugin) Render(configRaw json.RawMessage, record store.TaskRecord, getTemplate TemplateRetriever) (map[string]any, error) {
	var cfg OfficerInputConfig
	if len(configRaw) > 0 && string(configRaw) != "null" {
		_ = json.Unmarshal(configRaw, &cfg)
	}

	renderInfo := map[string]any{
		"form_type": "officer_input",
	}

	if cfg.OfficerJsonFormsID != "" {
		raw, exists := getTemplate(cfg.OfficerJsonFormsID)
		if !exists {
			return nil, fmt.Errorf("officer json form template %q not found", cfg.OfficerJsonFormsID)
		}
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, fmt.Errorf("failed to unmarshal officer json form template %q: %w", cfg.OfficerJsonFormsID, err)
		}
		renderInfo["officer_form_schema"] = decoded
	}
	return renderInfo, nil
}
