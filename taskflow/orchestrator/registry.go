// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package orchestrator

import "github.com/OpenNSW/core/artifact/adapter/types"

// TaskTemplate and SubTaskTemplate are defined in artifact/adapter/types so that
// artifact adapters and orchestrator can both reference them without a circular import.
// These aliases preserve the orchestrator.TaskTemplate / orchestrator.SubTaskTemplate API.
type (
	TaskTemplate    = types.TaskTemplate
	SubTaskTemplate = types.SubTaskTemplate
)
