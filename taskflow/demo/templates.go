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

// loadTemplates scans all *.json files recursively in templatesDir and registers them in the registry.
func loadTemplates(registry *orchestrator.TaskTemplateRegistry, templatesDir string) error {
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

		// 1. Try to unmarshal and register as a task template entry
		var entry orchestrator.TaskTemplateEntry
		errTemplate := json.Unmarshal(data, &entry)
		if errTemplate == nil && entry.TemplateID != "" && entry.PluginName != "" {
			registry.Register(entry)
			log.Printf("[Registry] Loaded template: %s (task_type=%s, plugin=%s)", entry.TemplateID, entry.TaskType, entry.PluginName)
			return nil
		}

		// 2. Try to unmarshal and register as a composite workflow template definition
		var workflowDef engine.WorkflowDefinition
		errWorkflow := json.Unmarshal(data, &workflowDef)
		if errWorkflow == nil && workflowDef.ID != "" && len(workflowDef.Nodes) > 0 {
			registry.RegisterWorkflow(workflowDef)
			log.Printf("[Registry] Loaded sub-workflow template: %s (%s)", workflowDef.ID, workflowDef.Name)
			return nil
		}

		// If it's not a template or a sub-workflow, check if the JSON is malformed
		if errTemplate != nil && errWorkflow != nil {
			var raw map[string]any
			if errRaw := json.Unmarshal(data, &raw); errRaw != nil {
				log.Printf("[Registry] Warning: Invalid JSON syntax in file %s: %v", path, errRaw)
			}
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("recursive template search failed: %w", err)
	}
	return nil
}
