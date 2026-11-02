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
	// Claimable says whether the task can be claimed (see store.TaskRecord.ClaimedBy). False, the
	// default, gives it no claims. Taskflow does not enforce it: the host offers claims, and calls
	// TaskManager.ClaimTask, only for a claimable task.
	Claimable bool `json:"claimable"`
	// Read lists who may read the task. Taskflow does not enforce it: the host decides which
	// roles a caller holds.
	Read ReadPolicy `json:"read"`
}

// ReadPolicy lists the roles that may read a task, in precedence order: a caller holding
// several reads it as the first one listed. Roles are opaque to taskflow. An empty list leaves
// the decision to the host.
type ReadPolicy struct {
	Roles []string `json:"roles"`
}

// ExecutionPhase identifies a point in the CompleteTaskStep lifecycle at which
// an extension may be invoked. SaveDraft runs the PRE_RESUME extensions too, with
// the step's draft command as the command, and never the POST_RESUME ones.
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
	// Draft, when set, lets SaveDraft store partial input for the step under OutputNamespace
	// without completing it. A step without it does not accept drafts.
	Draft *DraftConfig `json:"draft,omitempty"`
}

// DraftConfig turns on draft saving for a step.
type DraftConfig struct {
	// Command is the command a draft save is authorized as: the PRE_RESUME extensions see it as
	// the payload's "__command", so an authz rule names it like any other command of the step.
	Command string `json:"command"`
}
