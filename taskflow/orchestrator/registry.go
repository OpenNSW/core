package orchestrator

import "github.com/OpenNSW/core/taskflow/types"

// TaskTemplate and SubTaskTemplate are defined in taskflow/types so that
// artifact adapters and orchestrator can both reference them without a circular import.
// These aliases preserve the orchestrator.TaskTemplate / orchestrator.SubTaskTemplate API.
type (
	TaskTemplate    = types.TaskTemplate
	SubTaskTemplate = types.SubTaskTemplate
)
