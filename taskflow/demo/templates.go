package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	engine "github.com/OpenNSW/go-temporal-workflow"
	"github.com/OpenNSW/nsw-task-flow/orchestrator"
)

// loadTemplates scans all *.json files recursively in templatesDir and registers
// them in the registry. Each file is discriminated by the JSON fields it carries:
//
//   - workflow_id present       → TaskTemplate
//   - plugin_properties present → SubTaskTemplate
//   - nodes present             → engine.WorkflowDefinition
//   - otherwise (has id)        → generic JSON template (e.g. render config, form schema)
func loadTemplates(registry *TemplateRegistry, templatesDir string) error {
	err := filepath.WalkDir(templatesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}

		var probe struct {
			ID               string          `json:"id"`
			WorkflowID       string          `json:"workflow_id"`
			PluginProperties json.RawMessage `json:"plugin_properties"`
			Nodes            json.RawMessage `json:"nodes"`
		}
		if err := json.Unmarshal(data, &probe); err != nil {
			log.Printf("[Registry] Warning: Invalid JSON syntax in %s: %v", path, err)
			return nil
		}
		if probe.ID == "" {
			log.Printf("[Registry] Warning: Skipping %s (no \"id\" field)", path)
			return nil
		}

		switch {
		case probe.WorkflowID != "":
			var t orchestrator.TaskTemplate
			if err := json.Unmarshal(data, &t); err != nil {
				log.Printf("[Registry] Warning: Failed to parse task template %s: %v", path, err)
				return nil
			}
			registry.RegisterTaskTemplate(t)
			log.Printf("[Registry] Loaded task template: %s (type=%s, workflow=%s)", t.ID, t.Type, t.WorkflowID)

		case len(probe.PluginProperties) > 0:
			var s orchestrator.SubTaskTemplate
			if err := json.Unmarshal(data, &s); err != nil {
				log.Printf("[Registry] Warning: Failed to parse subtask template %s: %v", path, err)
				return nil
			}
			registry.RegisterSubTaskTemplate(s)
			log.Printf("[Registry] Loaded subtask template: %s (task_type=%s)", s.ID, s.TaskType)

		case len(probe.Nodes) > 0:
			var wf engine.WorkflowDefinition
			if err := json.Unmarshal(data, &wf); err != nil {
				log.Printf("[Registry] Warning: Failed to parse workflow %s: %v", path, err)
				return nil
			}
			registry.RegisterWorkflow(wf)
			log.Printf("[Registry] Loaded workflow definition: %s (%s)", wf.ID, wf.Name)

		default:
			registry.RegisterGenericTemplate(probe.ID, data)
			log.Printf("[Registry] Loaded generic JSON template: %s", probe.ID)
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("recursive template search failed: %w", err)
	}
	return nil
}
