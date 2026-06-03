// Package main is the entry point for the NSW Task Flow demo.
//
// Run from the repo root:
//
//	go run ./demo
//
// It wires together the Temporal orchestrators and serves the split-pane
// portal UI on http://localhost:8080.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	engine "github.com/OpenNSW/go-temporal-workflow"
	"github.com/OpenNSW/nsw-task-flow/orchestrator"
	"github.com/OpenNSW/nsw-task-flow/plugins"
	"go.temporal.io/sdk/client"
)

func main() {
	// 1. Temporal client
	c, err := client.Dial(client.Options{
		HostPort: client.DefaultHostPort,
	})
	if err != nil {
		log.Fatalln("Unable to create Temporal client", err)
	}
	defer c.Close()

	// 2. Store & Task Template Registry
	// Templates are loaded from ./demo/templates/*.json — add a new file to register a new flow.
	db := NewTaskDB()
	registry := NewTemplateRegistry()
	if err := loadTemplates(registry, "demo/templates"); err != nil {
		log.Fatalln("Failed to load task template registry:", err)
	}

	// 3. Set up Task Plugins Registry
	pluginsRegistry := plugins.NewRegistry()
	if err := pluginsRegistry.Register("USER_INPUT", plugins.NewUserInputPlugin()); err != nil {
		log.Fatalln("Failed to register user input plugin:", err)
	}

	// Pure local demo dispatcher: avoids external HTTP calls to keep the demo self-contained
	// and fast. The Task's status transitions to QUEUED_EXTERNALLY, allowing the local reviewer
	// dashboard to query it and submit responses locally.
	demoDispatcher := func(ctx context.Context, url string, taskID string, payload map[string]any) error {
		log.Printf("[Demo Dispatcher] Asynchronously queued task %s in local Reviewer dashboard queue (mock destination: %s). Waiting for reviewer action...", taskID, url)
		return nil
	}

	if err := pluginsRegistry.Register("EXTERNAL_REVIEW", plugins.NewExternalReviewPlugin(demoDispatcher)); err != nil {
		log.Fatalln("Failed to register external review plugin:", err)
	}

	if err := pluginsRegistry.Register("PAYMENT", plugins.NewPaymentPlugin(demoDispatcher)); err != nil {
		log.Fatalln("Failed to register payment plugin:", err)
	}

	if err := pluginsRegistry.Register("FIRE_AND_FORGET", plugins.NewAPICallPlugin(demoDispatcher)); err != nil {
		log.Fatalln("Failed to register http post plugin:", err)
	}

	// 4. Set up Temporal Managers (parent and task) with deferred task manager wiring
	var tm *orchestrator.TaskManager

	// --- Parent Workflow handlers ---
	parentTaskHandler := func(payload engine.TaskPayload) (map[string]any, error) {
		log.Printf("\n[Parent Workflow] Task activated: node=%s template=%s\n", payload.NodeID, payload.TaskTemplateID)
		if tm == nil {
			return nil, fmt.Errorf("task manager is not initialized (misconfiguration)")
		}
		return tm.StartTask(payload)
	}

	parentCompletionHandler := func(workflowID string, finalVariables map[string]any) error {
		log.Printf("\n[Parent Workflow] Completed. Final state: %v\n", finalVariables)
		return nil
	}

	parentWorkflowManager := engine.NewTemporalManager(
		c,
		"nsw-parent-workflow-queue",
		parentTaskHandler,
		parentCompletionHandler,
	)

	// --- Task Workflow handlers ---
	taskHandler := func(payload engine.TaskPayload) (map[string]any, error) {
		log.Printf("\n[Task Workflow] Step activated: node=%s template=%s\n", payload.NodeID, payload.TaskTemplateID)
		if tm == nil {
			return nil, fmt.Errorf("task manager is not initialized (misconfiguration)")
		}
		return tm.StartSubTask(payload)
	}

	taskCompletionHandler := func(workflowID string, finalVariables map[string]any) error {
		log.Printf("\n[Task Workflow] Completed. Final state: %v\n", finalVariables)
		if tm != nil {
			return tm.HandleTaskCompletion(context.Background(), workflowID, finalVariables)
		}
		return nil
	}

	taskWorkflowManager := engine.NewTemporalManager(
		c,
		"nsw-task-workflow-queue",
		taskHandler,
		taskCompletionHandler,
	)

	// 5. Wire everything together
	onTaskCompleted := func(parentWorkflowID string, parentRunID string, parentNodeID string, finalVariables map[string]any) error {
		err := parentWorkflowManager.TaskDone(context.Background(), parentWorkflowID, parentRunID, parentNodeID, finalVariables)
		if err != nil {
			log.Printf("[TaskManager] Failed to wake parent workflow %s: %v", parentWorkflowID, err)
			return err
		}
		log.Printf("[TaskManager] Woke parent workflow %s node %s", parentWorkflowID, parentNodeID)
		return nil
	}

	tm = orchestrator.NewTaskManager(db, registry, pluginsRegistry, taskWorkflowManager, onTaskCompleted, SimpleRenderer{})

	apiServer := newServer(tm, parentWorkflowManager)
	apiServer.start(":8080")

	// 6. Start workers
	log.Println("Starting Parent Workflow Temporal Worker...")
	if err := parentWorkflowManager.StartWorker(); err != nil {
		log.Fatalln("Unable to start parent workflow worker:", err)
	}
	defer parentWorkflowManager.StopWorker()

	log.Println("Starting Task Workflow Temporal Worker...")
	if err := taskWorkflowManager.StartWorker(); err != nil {
		log.Fatalln("Unable to start task workflow worker:", err)
	}
	defer taskWorkflowManager.StopWorker()

	// Wait for interrupt
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan
	log.Println("Shutting down gracefully...")
}
