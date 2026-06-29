// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package gorm

import (
	"context"
	"errors"
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
	model := FromDomain(record)
	// store.TaskStore.SaveTask returns no error (persistence is treated as
	// best-effort), so the only observability we have for a failed upsert is
	// a log line.
	// Explicit DoUpdates so the conflict path doesn't clobber created_at.
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
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
		slog.Error("taskflow gorm store: SaveTask upsert failed",
			"taskId", record.TaskID, "error", err)
	}
}

func (s *TaskStore) GetTask(ctx context.Context, taskID string) (store.TaskRecord, bool) {
	var model TaskRecordModel
	if err := s.db.WithContext(ctx).First(&model, "task_id = ?", taskID).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			slog.Error("taskflow gorm store: GetTask db error", "taskId", taskID, "error", err)
		}
		return store.TaskRecord{}, false
	}
	return model.ToDomain(), true
}

func (s *TaskStore) GetTaskByWorkflowID(ctx context.Context, workflowID string) (store.TaskRecord, bool) {
	var model TaskRecordModel
	if err := s.db.WithContext(ctx).First(&model, "task_workflow_id = ?", workflowID).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			slog.Error("taskflow gorm store: GetTaskByWorkflowID db error", "workflowId", workflowID, "error", err)
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
		slog.Error("taskflow gorm store: GetAllTasks db error", "parentWorkflowId", parentWorkflowID, "error", err)
		return nil
	}

	records := make([]store.TaskRecord, len(models))
	for i, m := range models {
		records[i] = m.ToDomain()
	}
	return records
}
