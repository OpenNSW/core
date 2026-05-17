package main

import (
	"encoding/json"

	engine "github.com/OpenNSW/go-temporal-workflow"
	"github.com/OpenNSW/nsw-task-flow/orchestrator"
)

// TemplateRegistry is an in-memory implementation of
// orchestrator.TaskTemplateRegistry. It is used by the demo to load templates
// from JSON files on disk.
type TemplateRegistry struct {
	taskTemplates    map[string]orchestrator.TaskTemplate
	subTaskTemplates map[string]orchestrator.SubTaskTemplate
	workflows        map[string]engine.WorkflowDefinition
	genericTemplates map[string]json.RawMessage
}

func NewTemplateRegistry() *TemplateRegistry {
	return &TemplateRegistry{
		taskTemplates:    make(map[string]orchestrator.TaskTemplate),
		subTaskTemplates: make(map[string]orchestrator.SubTaskTemplate),
		workflows:        make(map[string]engine.WorkflowDefinition),
		genericTemplates: make(map[string]json.RawMessage),
	}
}

func (r *TemplateRegistry) RegisterTaskTemplate(t orchestrator.TaskTemplate) {
	r.taskTemplates[t.ID] = t
}

func (r *TemplateRegistry) GetTaskTemplate(id string) (orchestrator.TaskTemplate, bool) {
	t, ok := r.taskTemplates[id]
	return t, ok
}

func (r *TemplateRegistry) RegisterSubTaskTemplate(s orchestrator.SubTaskTemplate) {
	r.subTaskTemplates[s.ID] = s
}

func (r *TemplateRegistry) GetSubTaskTemplate(id string) (orchestrator.SubTaskTemplate, bool) {
	s, ok := r.subTaskTemplates[id]
	return s, ok
}

func (r *TemplateRegistry) RegisterWorkflow(def engine.WorkflowDefinition) {
	r.workflows[def.ID] = def
}

func (r *TemplateRegistry) GetWorkflow(id string) (engine.WorkflowDefinition, bool) {
	def, ok := r.workflows[id]
	return def, ok
}

func (r *TemplateRegistry) RegisterGenericTemplate(id string, raw json.RawMessage) {
	r.genericTemplates[id] = raw
}

func (r *TemplateRegistry) GetGenericTemplate(id string) (json.RawMessage, bool) {
	raw, ok := r.genericTemplates[id]
	return raw, ok
}
