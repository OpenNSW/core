// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package gorm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/OpenNSW/core/taskflow/store"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TaskStore is a GORM-backed implementation of store.TaskStore, persisting
// TaskRecords to the "task_records_v2" table.
type TaskStore struct {
	db *gorm.DB
}

func New(db *gorm.DB) *TaskStore {
	return &TaskStore{db: db}
}

func (s *TaskStore) SaveTask(ctx context.Context, record store.TaskRecord) {
	// active_step_id and seq are owned by the guarded step statements below. Omitting them keeps a
	// full-record save from moving them, and lets this write work before the columns exist.
	model := FromDomain(record)
	// store.TaskStore.SaveTask returns no error (persistence is treated as
	// best-effort), so the only observability we have for a failed upsert is
	// a log line.
	// Explicit DoUpdates so the conflict path doesn't clobber created_at.
	if err := s.db.WithContext(ctx).Omit("active_step_id", "seq").Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "task_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"task_type",
			"state",
			"render_config",
			"parent_workflow_id",
			"parent_run_id",
			"parent_node_id",
			"root_workflow_id",
			"task_workflow_id",
			"task_run_id",
			"subtask_node_id",
			"active_task_template_id",
			"data",
			"updated_at",
		}),
	}).Create(&model).Error; err != nil {
		slog.ErrorContext(ctx, "taskflow gorm store: SaveTask upsert failed",
			"task_id", record.TaskID, "error", err)
	}
}

func (s *TaskStore) GetTask(ctx context.Context, taskID string) (store.TaskRecord, bool) {
	var model TaskRecordModel
	if err := s.db.WithContext(ctx).First(&model, "task_id = ?", taskID).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			slog.ErrorContext(ctx, "taskflow gorm store: GetTask db error", "task_id", taskID, "error", err)
		}
		return store.TaskRecord{}, false
	}
	return model.ToDomain(), true
}

func (s *TaskStore) GetTaskByWorkflowID(ctx context.Context, workflowID string) (store.TaskRecord, bool) {
	var model TaskRecordModel
	if err := s.db.WithContext(ctx).First(&model, "task_workflow_id = ?", workflowID).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			slog.ErrorContext(ctx, "taskflow gorm store: GetTaskByWorkflowID db error", "task_workflow_id", workflowID, "error", err)
		}
		return store.TaskRecord{}, false
	}
	return model.ToDomain(), true
}

func (s *TaskStore) GetAllTasks(ctx context.Context, parentWorkflowID string) []store.TaskRecord {
	var models []TaskRecordModel
	query := s.db.WithContext(ctx)
	if parentWorkflowID != "" {
		query = query.Where("root_workflow_id = ?", parentWorkflowID)
	}
	if err := query.Find(&models).Error; err != nil {
		slog.ErrorContext(ctx, "taskflow gorm store: GetAllTasks db error", "parent_workflow_id", parentWorkflowID, "error", err)
		return nil
	}

	records := make([]store.TaskRecord, len(models))
	for i, m := range models {
		records[i] = m.ToDomain()
	}
	return records
}

// jsonData encodes task data for the jsonb column the way FromDomain does.
func jsonData(data map[string]any) (json.RawMessage, error) {
	if data == nil {
		return json.RawMessage("null"), nil
	}
	return json.Marshal(data)
}

// stepUpdate runs one guarded UPDATE on a task row and returns the rows it changed. A guard that
// does not match changes 0 rows, which callers treat as "stale, dropped", not as an error.
func (s *TaskStore) stepUpdate(ctx context.Context, taskID, guard string, guardArgs []any, set map[string]any) (int64, error) {
	tx := s.db.WithContext(ctx).Model(&TaskRecordModel{}).
		Where("task_id = ? AND "+guard, append([]any{taskID}, guardArgs...)...).
		Updates(set)
	return tx.RowsAffected, tx.Error
}

func (s *TaskStore) ClaimStep(ctx context.Context, taskID string, claim store.StepClaim) (int64, error) {
	data, err := jsonData(claim.Data)
	if err != nil {
		return 0, fmt.Errorf("encode task data: %w", err)
	}
	// No active_step_id check: a claim's job is to replace whatever step is currently active with
	// claim.StepID, so the row's current active_step_id is expected to differ and isn't relevant.
	// seq <= claim.Seq alone detects staleness, since seq is workflow-wide and monotonic — "<=" (not
	// "<") also lets a retry of this exact claim (same seq) match again.
	return s.stepUpdate(ctx, taskID, "seq <= ?", []any{claim.Seq}, map[string]any{
		"active_step_id":          claim.StepID,
		"seq":                     claim.Seq,
		"active_task_template_id": claim.ActiveTaskTemplateID,
		"state":                   claim.State,
		"data":                    data,
	})
}

func (s *TaskStore) WriteRenderState(ctx context.Context, taskID, stepID string, seq int64, state string, data map[string]any) (int64, error) {
	encoded, err := jsonData(data)
	if err != nil {
		return 0, fmt.Errorf("encode task data: %w", err)
	}
	return s.stepUpdate(ctx, taskID, "active_step_id = ? AND seq = ?", []any{stepID, seq}, map[string]any{
		"state": state,
		"data":  encoded,
	})
}

func (s *TaskStore) PersistSubmission(ctx context.Context, taskID, stepID string, seq int64, data map[string]any) (int64, error) {
	encoded, err := jsonData(data)
	if err != nil {
		return 0, fmt.Errorf("encode task data: %w", err)
	}
	return s.stepUpdate(ctx, taskID, "active_step_id = ? AND seq = ?", []any{stepID, seq}, map[string]any{
		"data":  encoded,
		"state": store.StateAdvancing,
		"seq":   seq + 1,
	})
}

func (s *TaskStore) CompleteTask(ctx context.Context, taskID string, seq int64) (int64, error) {
	// No active_step_id check, for the same reason as ClaimStep: completion isn't about which step
	// is active, only about whether a later step has already superseded this seq.
	return s.stepUpdate(ctx, taskID, "seq <= ?", []any{seq}, map[string]any{
		"state": store.StateCompleted,
		"seq":   seq,
	})
}
