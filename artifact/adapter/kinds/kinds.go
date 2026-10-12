// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package kinds names the artifact kinds core's adapters own. It imports nothing but artifact,
// so adapters that refer to each other's kinds share these constants instead of copying them.
package kinds

import "github.com/OpenNSW/core/artifact"

const (
	// Workflow is an engine.WorkflowDefinition (adapter workflowdef).
	Workflow artifact.Kind = "workflow"
	// TaskTemplate is a types.TaskTemplate (adapter tasktemplate).
	TaskTemplate artifact.Kind = "task_template"
	// StepTemplate is a types.StepTemplate (adapter steptemplate).
	StepTemplate artifact.Kind = "subtask_template"
	// GenericTemplate is free-form JSON: a render config, form schema or document (adapter
	// generictemplate).
	GenericTemplate artifact.Kind = "generic_template"
)
