// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package gorm

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/OpenNSW/core/taskflow/store"
)

// TaskRecordModel is the GORM-compatible model for store.TaskRecord.
type TaskRecordModel struct {
	TaskID               string          `gorm:"primaryKey;column:task_id;type:text"`
	TaskType             string          `gorm:"column:task_type;type:text;index"`
	State                string          `gorm:"column:state;type:text"`
	RenderConfig         json.RawMessage `gorm:"column:render_config;type:jsonb;serializer:json"`
	ParentWorkflowID     string          `gorm:"column:parent_workflow_id;type:text;index"`
	RootWorkflowID       string          `gorm:"column:root_workflow_id;type:text;not null;default:''"`
	ParentRunID          string          `gorm:"column:parent_run_id;type:text"`
	ParentStepID         string          `gorm:"column:parent_step_id;type:text"`
	TaskWorkflowID       string          `gorm:"column:task_workflow_id;type:text;index"`
	ActiveTaskTemplateID string          `gorm:"column:active_task_template_id;type:text"`
	// ActiveStepID and Seq belong to the guarded step statements in store.go. SaveTask never
	// writes them (see TaskStore.SaveTask), so a full-record save cannot move them backwards.
	// The table needs: active_step_id UUID NULL, seq BIGINT NOT NULL DEFAULT 0.
	ActiveStepID *string         `gorm:"column:active_step_id;type:uuid"`
	Seq          int64           `gorm:"column:seq;not null;default:0"`
	Data         json.RawMessage `gorm:"column:data;type:jsonb;serializer:json"`
	CreatedAt    time.Time       `gorm:"column:created_at;type:timestamptz;not null;autoCreateTime"`
	UpdatedAt    time.Time       `gorm:"column:updated_at;type:timestamptz;not null;autoUpdateTime"`
}

func (TaskRecordModel) TableName() string {
	return "task_records_v2"
}

// ToDomain converts the GORM model to the domain TaskRecord.
func (m TaskRecordModel) ToDomain() store.TaskRecord {
	var data map[string]any
	if len(m.Data) > 0 {
		if err := json.Unmarshal(m.Data, &data); err != nil {
			slog.Error("taskflow gorm store: ToDomain unmarshal of Data failed",
				"task_id", m.TaskID, "error", err)
		}
	}

	var activeStepID string
	if m.ActiveStepID != nil {
		activeStepID = *m.ActiveStepID
	}

	return store.TaskRecord{
		TaskID:               m.TaskID,
		TaskType:             m.TaskType,
		State:                m.State,
		RenderConfig:         m.RenderConfig,
		ParentWorkflowID:     m.ParentWorkflowID,
		ParentRunID:          m.ParentRunID,
		ParentStepID:         m.ParentStepID,
		RootWorkflowID:       m.RootWorkflowID,
		TaskWorkflowID:       m.TaskWorkflowID,
		ActiveTaskTemplateID: m.ActiveTaskTemplateID,
		ActiveStepID:         activeStepID,
		Seq:                  m.Seq,
		Data:                 data,
		CreatedAt:            m.CreatedAt,
		UpdatedAt:            m.UpdatedAt,
	}
}

// FromDomain creates a GORM model from the domain TaskRecord.
func FromDomain(r store.TaskRecord) TaskRecordModel {
	dataBytes, err := json.Marshal(r.Data)
	if err != nil {
		slog.Error("taskflow gorm store: FromDomain failed to marshal Data", "task_id", r.TaskID, "error", err)
	}

	var activeStepID *string
	if r.ActiveStepID != "" {
		activeStepID = &r.ActiveStepID
	}

	return TaskRecordModel{
		TaskID:               r.TaskID,
		TaskType:             r.TaskType,
		State:                r.State,
		RenderConfig:         r.RenderConfig,
		ParentWorkflowID:     r.ParentWorkflowID,
		RootWorkflowID:       r.RootWorkflowID,
		ParentRunID:          r.ParentRunID,
		ParentStepID:         r.ParentStepID,
		TaskWorkflowID:       r.TaskWorkflowID,
		ActiveTaskTemplateID: r.ActiveTaskTemplateID,
		ActiveStepID:         activeStepID,
		Seq:                  r.Seq,
		Data:                 dataBytes,
	}
}
