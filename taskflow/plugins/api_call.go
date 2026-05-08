package plugins

import (
	"encoding/json"
	"fmt"
	"log"
)

// APICallPlugin implements the generic_api_call plugin for FIRE_AND_FORGET tasks.
// It sends an API request to a configured URL containing the task data payload.
type APICallPlugin struct {
	dispatcher Dispatcher
}

// NewAPICallPlugin creates a new APICallPlugin.
func NewAPICallPlugin(dispatcher Dispatcher) *APICallPlugin {
	if dispatcher == nil {
		dispatcher = DefaultHTTPDispatcher
	}
	return &APICallPlugin{
		dispatcher: dispatcher,
	}
}

func (p *APICallPlugin) Name() string {
	return "generic_api_call"
}

// APICallConfig holds properties decoded from the TaskTemplate's JSON configuration.
type APICallConfig struct {
	URL string `json:"url"`
}

func (p *APICallPlugin) Execute(ctx PluginContext, configRaw json.RawMessage) error {
	var cfg APICallConfig
	if err := json.Unmarshal(configRaw, &cfg); err != nil {
		return fmt.Errorf("failed to parse generic_api_call config: %w", err)
	}

	if cfg.URL == "" {
		return fmt.Errorf("missing 'url' in generic_api_call config")
	}

	ctx.Record.Status = "DISPATCHED"

	log.Printf("[Plugin: generic_api_call] Dispatching fire-and-forget payload for task %s to URL: %s", ctx.Record.TaskID, cfg.URL)

	err := p.dispatcher(ctx.Context, cfg.URL, ctx.Record.TaskID, ctx.Record.Data)
	if err != nil {
		return fmt.Errorf("api call dispatch failed: %w", err)
	}

	log.Printf("[Plugin: generic_api_call] Successfully invoked API for task %s (active step: %s)", ctx.Record.TaskID, ctx.Record.SubTaskNodeID)
	return nil
}
