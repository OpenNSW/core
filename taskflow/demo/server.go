package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	engine "github.com/OpenNSW/go-temporal-workflow"
	"github.com/OpenNSW/nsw-task-flow/orchestrator"
)

type server struct {
	manager               *orchestrator.TaskManager
	parentWorkflowManager engine.TemporalManager
}

func newServer(manager *orchestrator.TaskManager, parentWorkflowManager engine.TemporalManager) *server {
	return &server{manager: manager, parentWorkflowManager: parentWorkflowManager}
}

func (s *server) start(addr string) {
	// Serve the demo UI from demo/static/
	http.Handle("/", http.FileServer(http.Dir("./demo/static")))

	// Serve JSONForms schema files from demo/static/forms/
	http.Handle("/forms/", http.StripPrefix("/forms/", http.FileServer(http.Dir("./demo/static/forms"))))

	// API endpoints
	http.HandleFunc("/api/tasks", s.handleGetTasks)
	http.HandleFunc("/api/start", s.handleStartWorkflow)
	http.HandleFunc("/api/task/", s.handleTaskInteraction)

	log.Printf("[API] Starting HTTP server on %s...", addr)
	go func() {
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Printf("[API] HTTP server error: %v", err)
		}
	}()
}

func (s *server) handleGetTasks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.manager.GetAllTasks()) //nolint:errcheck
}

func (s *server) handleStartWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ApplicantName string `json:"applicant_name"`
	}
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck

	fileBytes, err := os.ReadFile("demo/workflow.json")
	if err != nil {
		http.Error(w, "Failed to read demo/workflow.json", http.StatusInternalServerError)
		return
	}

	var def engine.WorkflowDefinition
	if err := json.Unmarshal(fileBytes, &def); err != nil {
		http.Error(w, "Failed to parse demo/workflow.json", http.StatusInternalServerError)
		return
	}

	workflowID := "nsw-phyto-" + time.Now().Format("150405")
	log.Printf("[API] Starting Parent workflow %s (applicant=%s)", workflowID, req.ApplicantName)

	if req.ApplicantName == "" {
		req.ApplicantName = "John Doe"
	}
	initialVars := map[string]any{"applicant_name": req.ApplicantName}

	err = s.parentWorkflowManager.StartWorkflow(context.Background(), workflowID, def, initialVars)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to start parent workflow: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "workflow_id": workflowID}) //nolint:errcheck
}

// handleTaskInteraction is the unified endpoint: POST /api/task/{taskID}
// It routes the payload to the correct active Task workflow activity using the stored ActiveActivityID.
func (s *server) handleTaskInteraction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	taskID := strings.TrimPrefix(r.URL.Path, "/api/task/")
	if taskID == "" {
		http.Error(w, "missing task ID in path", http.StatusBadRequest)
		return
	}

	// The payload from the UI is a namespaced map matching the JSONForms structure,
	// e.g. {"userform": {...}} or {"reviewerform": {...}}
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	err := s.manager.CompleteTaskStep(r.Context(), taskID, payload)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if strings.Contains(err.Error(), "already completed") {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		log.Printf("[API] Failed to complete task step: %v", err)
		http.Error(w, "failed to resume task workflow", http.StatusInternalServerError)
		return
	}

	log.Printf("[API] Task %s resumed successfully", taskID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) //nolint:errcheck
}
