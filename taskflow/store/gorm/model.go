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
	ParentNodeID         string          `gorm:"column:parent_node_id;type:text"`
	TaskWorkflowID       string          `gorm:"column:task_workflow_id;type:text;index"`
	TaskRunID            string          `gorm:"column:task_run_id;type:text"`
	SubTaskNodeID        string          `gorm:"column:subtask_node_id;type:text"`
	ActiveTaskTemplateID string          `gorm:"column:active_task_template_id;type:text"`
	Data                 json.RawMessage `gorm:"column:data;type:jsonb;serializer:json"`
	CreatedAt            time.Time       `gorm:"column:created_at;type:timestamptz;not null;autoCreateTime"`
	UpdatedAt            time.Time       `gorm:"column:updated_at;type:timestamptz;not null;autoUpdateTime"`
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
				"taskId", m.TaskID, "error", err)
		}
	}

	return store.TaskRecord{
		TaskID:               m.TaskID,
		TaskType:             m.TaskType,
		State:                m.State,
		RenderConfig:         m.RenderConfig,
		ParentWorkflowID:     m.ParentWorkflowID,
		ParentRunID:          m.ParentRunID,
		ParentNodeID:         m.ParentNodeID,
		RootWorkflowID:       m.RootWorkflowID,
		TaskWorkflowID:       m.TaskWorkflowID,
		TaskRunID:            m.TaskRunID,
		SubTaskNodeID:        m.SubTaskNodeID,
		ActiveTaskTemplateID: m.ActiveTaskTemplateID,
		Data:                 data,
		CreatedAt:            m.CreatedAt,
		UpdatedAt:            m.UpdatedAt,
	}
}

// FromDomain creates a GORM model from the domain TaskRecord.
func FromDomain(r store.TaskRecord) TaskRecordModel {
	dataBytes, err := json.Marshal(r.Data)
	if err != nil {
		slog.Error("taskflow gorm store: FromDomain failed to marshal Data", "taskId", r.TaskID, "error", err)
	}

	return TaskRecordModel{
		TaskID:               r.TaskID,
		TaskType:             r.TaskType,
		State:                r.State,
		RenderConfig:         r.RenderConfig,
		ParentWorkflowID:     r.ParentWorkflowID,
		RootWorkflowID:       r.RootWorkflowID,
		ParentRunID:          r.ParentRunID,
		ParentNodeID:         r.ParentNodeID,
		TaskWorkflowID:       r.TaskWorkflowID,
		TaskRunID:            r.TaskRunID,
		SubTaskNodeID:        r.SubTaskNodeID,
		ActiveTaskTemplateID: r.ActiveTaskTemplateID,
		Data:                 dataBytes,
	}
}
