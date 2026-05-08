package plugins

import (
	"encoding/json"
	"fmt"
	"log"
)

// EventWaitPlugin implements the register_task_and_wait plugin.
// It queues a task externally via HTTP POST and transitions the task record to "WAITING_FOR_EVENT".
type EventWaitPlugin struct {
	dispatcher Dispatcher
}

// NewEventWaitPlugin creates a new EventWaitPlugin.
func NewEventWaitPlugin(dispatcher Dispatcher) *EventWaitPlugin {
	if dispatcher == nil {
		dispatcher = DefaultHTTPDispatcher
	}
	return &EventWaitPlugin{
		dispatcher: dispatcher,
	}
}

func (p *EventWaitPlugin) Name() string {
	return "register_task_and_wait"
}

// EventWaitConfig holds properties decoded from the TaskTemplate's JSON configuration.
type EventWaitConfig struct {
	ExternalURL string `json:"external_url"`
	TaskType    string `json:"task_type,omitempty"`
}

func (p *EventWaitPlugin) Execute(ctx PluginContext, configRaw json.RawMessage) error {
	var cfg EventWaitConfig
	if err := json.Unmarshal(configRaw, &cfg); err != nil {
		return fmt.Errorf("failed to parse register_task_and_wait config: %w", err)
	}

	if cfg.ExternalURL == "" {
		return fmt.Errorf("missing 'external_url' in register_task_and_wait config")
	}

	// Transition the record to WAITING_FOR_EVENT to represent that this step is sleeping / waiting
	ctx.Record.Status = "WAITING_FOR_EVENT"

	// Prepare payload for external queue
	dispatchPayload := map[string]any{
		"task_id":         ctx.Record.TaskID,
		"subtask_node_id": ctx.Record.SubTaskNodeID,
		"task_type":       cfg.TaskType,
		"data":            ctx.Record.Data,
	}

	log.Printf("[Plugin: register_task_and_wait] Queueing event task %s to external URL: %s", ctx.Record.TaskID, cfg.ExternalURL)

	err := p.dispatcher(ctx.Context, cfg.ExternalURL, ctx.Record.TaskID, dispatchPayload)
	if err != nil {
		return fmt.Errorf("event registration dispatch failed: %w", err)
	}

	log.Printf("[Plugin: register_task_and_wait] Successfully queued task %s (active step: %s, external task type: %s)", ctx.Record.TaskID, ctx.Record.SubTaskNodeID, cfg.TaskType)
	return ErrSuspended
}
