package plugins

import (
	"encoding/json"
	"fmt"
	"log"
)

// HTTPPostPlugin implements the generic_http_post plugin for FIRE_AND_FORGET tasks.
// It sends an HTTP POST request to a configured URL containing the task data payload.
type HTTPPostPlugin struct {
	dispatcher HTTPDispatcher
}

// NewHTTPPostPlugin creates a new HTTPPostPlugin.
func NewHTTPPostPlugin(dispatcher HTTPDispatcher) *HTTPPostPlugin {
	if dispatcher == nil {
		dispatcher = DefaultHTTPDispatcher
	}
	return &HTTPPostPlugin{
		dispatcher: dispatcher,
	}
}

func (p *HTTPPostPlugin) Name() string {
	return "generic_http_post"
}

// HTTPPostConfig holds properties decoded from the TaskTemplate's JSON configuration.
type HTTPPostConfig struct {
	URL string `json:"url"`
}

func (p *HTTPPostPlugin) Execute(ctx PluginContext, configRaw json.RawMessage) error {
	var cfg HTTPPostConfig
	if err := json.Unmarshal(configRaw, &cfg); err != nil {
		return fmt.Errorf("failed to parse generic_http_post config: %w", err)
	}

	if cfg.URL == "" {
		return fmt.Errorf("missing 'url' in generic_http_post config")
	}

	ctx.Record.Status = "DISPATCHED"

	log.Printf("[Plugin: generic_http_post] Dispatching fire-and-forget payload for task %s to URL: %s", ctx.Record.TaskID, cfg.URL)

	err := p.dispatcher(ctx.Context, cfg.URL, ctx.Record.TaskID, ctx.Record.Data)
	if err != nil {
		return fmt.Errorf("http post dispatch failed: %w", err)
	}

	log.Printf("[Plugin: generic_http_post] Successfully posted task %s (active step: %s)", ctx.Record.TaskID, ctx.Record.SubTaskNodeID)
	return nil
}
