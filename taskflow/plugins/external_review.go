package plugins

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/OpenNSW/nsw-task-flow/store"
)

// ExternalReviewPlugin manages asynchronous delegation of task steps to third-party government agencies.
type ExternalReviewPlugin struct {
	dispatcher Dispatcher
}

// NewExternalReviewPlugin returns a plugin with a custom or default HTTP dispatcher.
func NewExternalReviewPlugin(dispatcher Dispatcher) *ExternalReviewPlugin {
	if dispatcher == nil {
		dispatcher = DefaultHTTPDispatcher
	}
	return &ExternalReviewPlugin{
		dispatcher: dispatcher,
	}
}

func (p *ExternalReviewPlugin) Name() string {
	return "generic_external_review"
}

// ExternalReviewConfig holds properties decoded from the TaskTemplate's JSON configuration.
type ExternalReviewConfig struct {
	ExternalURL         string `json:"external_url"`
	ReviewerJsonFormsID string `json:"reviewer_jsonforms_id,omitempty"`
}

func (p *ExternalReviewPlugin) Execute(ctx PluginContext, configRaw json.RawMessage) error {
	var cfg ExternalReviewConfig
	if err := json.Unmarshal(configRaw, &cfg); err != nil {
		return fmt.Errorf("failed to parse external review plugin config: %w", err)
	}

	if cfg.ExternalURL == "" {
		return fmt.Errorf("missing 'external_url' in external review plugin config")
	}

	if cfg.ReviewerJsonFormsID != "" {
		ctx.Record.ReviewerFormID = cfg.ReviewerJsonFormsID
	}

	ctx.Record.Status = "QUEUED_EXTERNALLY"
	log.Printf("[Plugin: generic_external_review] Dispatching task %s to external URL: %s", ctx.Record.TaskID, cfg.ExternalURL)

	err := p.dispatcher(ctx.Context, cfg.ExternalURL, ctx.Record.TaskID, ctx.Record.Data)
	if err != nil {
		return fmt.Errorf("external dispatch failed: %w", err)
	}

	log.Printf("[Plugin: generic_external_review] Successfully dispatched task %s (active step: %s, form: %s)", ctx.Record.TaskID, ctx.Record.SubTaskNodeID, ctx.Record.ReviewerFormID)
	return ErrSuspended
}

func (p *ExternalReviewPlugin) Render(configRaw json.RawMessage, record store.TaskRecord, getTemplate TemplateRetriever) (map[string]any, error) {
	var cfg ExternalReviewConfig
	if len(configRaw) > 0 && string(configRaw) != "null" {
		_ = json.Unmarshal(configRaw, &cfg)
	}

	renderInfo := map[string]any{
		"form_type": "external_review",
	}

	if cfg.ReviewerJsonFormsID != "" {
		if raw, exists := getTemplate(cfg.ReviewerJsonFormsID); exists {
			var decoded map[string]any
			if err := json.Unmarshal(raw, &decoded); err == nil {
				renderInfo["reviewer_form_schema"] = decoded
			}
		}
	}
	return renderInfo, nil
}
