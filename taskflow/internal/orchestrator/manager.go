package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	engine "github.com/OpenNSW/go-temporal-workflow"
	"github.com/OpenNSW/nsw-task-flow/internal/store"
	"github.com/google/uuid"
)

// TaskManager orchestrates the three-layer architecture described in the design doc.
// It bridges Layer 1 (macro journey) and Layer 2 (micro flow) via a single DB entry per task.
type TaskManager struct {
	db       *store.TaskDB
	registry *TaskTemplateRegistry
	// layer1Manager wakes Layer 1 when a Layer 2 sub-workflow completes.
	layer1Manager engine.TemporalManager
	// layer2Manager starts and wakes Layer 2 sub-workflows.
	layer2Manager engine.TemporalManager
}

func NewTaskManager(db *store.TaskDB, registry *TaskTemplateRegistry, layer1 engine.TemporalManager, layer2 engine.TemporalManager) *TaskManager {
	return &TaskManager{
		db:            db,
		registry:      registry,
		layer1Manager: layer1,
		layer2Manager: layer2,
	}
}

// StartTask is called by the Layer 1 engine when it activates a TASK node.
// It looks up the template registry, creates the single DB record with Layer 1 parent
// coordinates, and kicks off the Layer 2 workflow.
func (tm *TaskManager) StartTask(payload engine.TaskPayload) error {
	regEntry, ok := tm.registry.Get(payload.TaskTemplateID)
	if !ok {
		return fmt.Errorf("unknown task_template_id: %s", payload.TaskTemplateID)
	}

	taskID := "task-" + uuid.New().String()[:8]
	layer2WorkflowID := "layer2-" + taskID

	// Map Layer 1 inputs into the namespaced Data map.
	// e.g. input_mapping: {"applicant_name": "userform.applicant_name"}
	// payload.Inputs holds the already-mapped values from the engine.
	initialData := make(map[string]any)
	// The engine applies input_mapping before calling us, so payload.Inputs
	// contains keys like "userform.applicant_name". We need to expand the dot-path.
	for k, v := range payload.Inputs {
		setNestedKey(initialData, k, v)
	}
	// Embed the task ID so Layer 2 activities can reference it
	initialData["_task_id"] = taskID

	record := store.TaskRecord{
		TaskID:           taskID,
		TaskType:         regEntry.TaskType,
		UserFormID:       regEntry.UserJsonFormsID,
		ReviewerFormID:   regEntry.ReviewerJsonFormsID,
		Status:           "STARTING",
		Layer1WorkflowID: payload.WorkflowID,
		Layer1RunID:      payload.RunID,
		Layer1NodeID:     payload.NodeID,
		// Set Layer2WorkflowID now so HandleTask can find this record immediately
		Layer2WorkflowID: layer2WorkflowID,
		Data:             initialData,
		CreatedAt:        time.Now(),
	}
	tm.db.SaveTask(record)
	log.Printf("[TaskManager] Created Task record %s (template=%s)", taskID, payload.TaskTemplateID)

	// Load task.json as the Layer 2 WorkflowDefinition
	fileBytes, err := os.ReadFile("task.json")
	if err != nil {
		return fmt.Errorf("failed to read task.json: %v", err)
	}
	var def engine.WorkflowDefinition
	if err := json.Unmarshal(fileBytes, &def); err != nil {
		return fmt.Errorf("failed to parse task.json: %v", err)
	}

	err = tm.layer2Manager.StartWorkflow(context.Background(), layer2WorkflowID, def, initialData)
	if err != nil {
		return fmt.Errorf("failed to start Layer 2 workflow: %v", err)
	}
	log.Printf("[TaskManager] Started Layer 2 workflow %s for task %s", layer2WorkflowID, taskID)
	return nil
}

// HandleTask is called by the Layer 2 engine when it activates a TASK node inside the sub-workflow.
// It routes to the correct capability handler based on task_template_id.
func (tm *TaskManager) HandleTask(payload engine.TaskPayload) error {
	// Layer2WorkflowID is stored in the DB record at StartTask time, so this lookup always works.
	record, exists := tm.db.GetTaskByLayer2WorkflowID(payload.WorkflowID)
	if !exists {
		return fmt.Errorf("[HandleTask] no task record found for Layer 2 workflow %s", payload.WorkflowID)
	}

	// Update active coordinates for this step
	record.Layer2RunID = payload.RunID
	record.ActiveActivityID = payload.NodeID

	// Merge any new data from the node inputs into our namespaced Data map
	for k, v := range payload.Inputs {
		setNestedKey(record.Data, k, v)
	}

	switch payload.TaskTemplateID {
	case "generic_user_input":
		record.Status = "PENDING_USER"
		log.Printf("[TaskManager] Task %s waiting for user input at node %s", record.TaskID, payload.NodeID)

	case "generic_external_review":
		record.Status = "QUEUED_EXTERNALLY"
		// Call the external API to queue the application form request in the external system.
		log.Printf("[TaskManager] Task %s dispatched to external reviewer at node %s", record.TaskID, payload.NodeID)

	default:
		return fmt.Errorf("unknown Layer 2 task_template_id: %s", payload.TaskTemplateID)
	}

	tm.db.SaveTask(record)
	return nil
}

// HandleLayer2Completion is called when a Layer 2 workflow hits its END node.
// It uses the stored Layer 1 parent coordinates to wake up the macro journey.
func (tm *TaskManager) HandleLayer2Completion(workflowID string, finalVariables map[string]any) error {
	record, exists := tm.db.GetTaskByLayer2WorkflowID(workflowID)
	if !exists {
		// Not a Layer 2 workflow we own — safe to ignore.
		return nil
	}

	log.Printf("[TaskManager] Layer 2 workflow %s completed for task %s", workflowID, record.TaskID)

	record.Status = "COMPLETED"
	tm.db.SaveTask(record)

	// Extract the mapped output for Layer 1 from the final global state.
	// The output_mapping in workflow.json maps "reviewerform.review_outcome" -> "phase1_outcome".
	// The engine handles this mapping before calling the completion handler,
	// so finalVariables should already contain the mapped keys.
	err := tm.layer1Manager.TaskDone(
		context.Background(),
		record.Layer1WorkflowID,
		record.Layer1RunID,
		record.Layer1NodeID,
		finalVariables,
	)
	if err != nil {
		log.Printf("[TaskManager] Failed to wake Layer 1 workflow %s: %v", record.Layer1WorkflowID, err)
		return err
	}

	log.Printf("[TaskManager] Woke Layer 1 workflow %s node %s", record.Layer1WorkflowID, record.Layer1NodeID)
	return nil
}

func (tm *TaskManager) GetDB() *store.TaskDB {
	return tm.db
}

func (tm *TaskManager) GetLayer1Manager() engine.TemporalManager {
	return tm.layer1Manager
}

func (tm *TaskManager) GetLayer2Manager() engine.TemporalManager {
	return tm.layer2Manager
}

// setNestedKey sets a value in a map using a dot-separated path.
// e.g. setNestedKey(m, "userform.applicant_name", "Acme") sets m["userform"]["applicant_name"] = "Acme"
func setNestedKey(m map[string]any, dotPath string, value any) {
	if dotPath == "" {
		return
	}
	// Find the first dot
	for i := 0; i < len(dotPath); i++ {
		if dotPath[i] == '.' {
			key := dotPath[:i]
			rest := dotPath[i+1:]
			sub, ok := m[key]
			if !ok || sub == nil {
				sub = make(map[string]any)
			}
			subMap, ok := sub.(map[string]any)
			if !ok {
				subMap = make(map[string]any)
			}
			setNestedKey(subMap, rest, value)
			m[key] = subMap
			return
		}
	}
	// No dot found — leaf key
	m[dotPath] = value
}
