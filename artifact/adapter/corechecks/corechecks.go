// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

// Package corechecks collects the artifact.Check of every kind core's adapters own, for
// artifact.Set.Validate. A host adds the checks for its own kinds to the returned map.
package corechecks

import (
	"github.com/OpenNSW/core/artifact"
	"github.com/OpenNSW/core/artifact/adapter/generictemplate"
	"github.com/OpenNSW/core/artifact/adapter/steptemplate"
	"github.com/OpenNSW/core/artifact/adapter/tasktemplate"
	"github.com/OpenNSW/core/artifact/adapter/workflowdef"
)

// Checks returns a new map holding the check for each core kind.
func Checks() artifact.Checks {
	return artifact.Checks{
		workflowdef.Kind:     workflowdef.Check,
		tasktemplate.Kind:    tasktemplate.Check,
		steptemplate.Kind:    steptemplate.Check,
		generictemplate.Kind: generictemplate.Check,
	}
}
