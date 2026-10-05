// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package types

import "encoding/json"

// TaskTemplate describes a Task — the macro unit of work activated by a parent
// workflow. A Task runs a child workflow whose nodes invoke StepTemplates.
type TaskTemplate struct {
	ID             string `json:"id"`
	Type           string `json:"type"`             // user-facing category (e.g. "APPLICATION")
	WorkflowID     string `json:"workflow_id"`      // points at a registered engine.WorkflowDefinition
	RenderConfigID string `json:"render_config_id"` // task-level render config
}

// ExecutionPhase identifies a point in the CompleteTaskStep lifecycle at which
// an extension may be invoked.
type ExecutionPhase string

const (
	PhasePreResume  ExecutionPhase = "PRE_RESUME"
	PhasePostResume ExecutionPhase = "POST_RESUME"
)

// ExtensionConfig defines configuration for an extension attached to a step.
type ExtensionConfig struct {
	ID         string          `json:"id"`
	Phase      ExecutionPhase  `json:"phase"`
	Properties json.RawMessage `json:"properties,omitempty"`
}

// StepTemplate describes a Step — one execution inside a Task's workflow
// that delegates to a plugin.
type StepTemplate struct {
	ID string `json:"id"`
	// PluginType is the plugin routing key (e.g. "USER_INPUT"), unrelated to TaskTemplate.Type
	// (the Task's own business category) despite the historical JSON key they share. Written
	// "task_type" in template JSON for compatibility with existing definitions.
	//
	// TODO(#taskflow-guarded-writes): change the wire key to "plugin_type" (and migrate every
	// stored StepTemplate, in this repo and in hosts) once that content migration is planned.
	PluginType       string            `json:"task_type"`
	PluginProperties json.RawMessage   `json:"plugin_properties"`          // plugin-specific config
	OutputNamespace  string            `json:"output_namespace,omitempty"` // top-level slot in TaskRecord.Data where CompleteTaskStep payloads are written
	Extensions       []ExtensionConfig `json:"extensions,omitempty"`       // list of extensions to run during CompleteTaskStep
}
