// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package engine

import "errors"

// ParkCategory says why a node parked in NodeStatusAwaitingAdmin, so tooling can guide an admin
// without parsing NodeInfo.LastError.
type ParkCategory string

const (
	// ParkCategoryInputMapping: a required global variable for the node's input mapping is missing.
	ParkCategoryInputMapping ParkCategory = "INPUT_MAPPING"
	// ParkCategoryOutputMapping: the Activity ran, but its result lacks a required mapped field.
	ParkCategoryOutputMapping ParkCategory = "OUTPUT_MAPPING"
	// ParkCategoryTaskFailure: the node's own work (the Activity, a signal send) failed.
	ParkCategoryTaskFailure ParkCategory = "TASK_FAILURE"
	// ParkCategoryGatewayCondition: a gateway condition failed to evaluate, or no edge matched.
	ParkCategoryGatewayCondition ParkCategory = "GATEWAY_CONDITION"
	// ParkCategorySplitData: the items or branch data a split or join needs is missing or malformed.
	ParkCategorySplitData ParkCategory = "SPLIT_DATA"
	// ParkCategoryChildFailure: a spawned child workflow failed.
	ParkCategoryChildFailure ParkCategory = "CHILD_FAILURE"
	// ParkCategoryDefinitionError: the workflow definition itself is invalid, so no variable fixes it.
	ParkCategoryDefinitionError ParkCategory = "DEFINITION_ERROR"
	// ParkCategoryUnknown: the error was never categorized.
	ParkCategoryUnknown ParkCategory = "UNKNOWN"
)

// categorizedError attaches a ParkCategory to an error where it is raised.
type categorizedError struct {
	category ParkCategory
	err      error
}

func (e *categorizedError) Error() string { return e.err.Error() }
func (e *categorizedError) Unwrap() error { return e.err }

// withCategory tags err with category. An error that already has one keeps it: the innermost tag
// is the most specific.
func withCategory(category ParkCategory, err error) error {
	if err == nil {
		return nil
	}
	var ce *categorizedError
	if errors.As(err, &ce) {
		return err
	}
	return &categorizedError{category: category, err: err}
}

// categoryOf returns err's ParkCategory, or ParkCategoryUnknown if it was never tagged.
func categoryOf(err error) ParkCategory {
	var ce *categorizedError
	if errors.As(err, &ce) {
		return ce.category
	}
	return ParkCategoryUnknown
}
