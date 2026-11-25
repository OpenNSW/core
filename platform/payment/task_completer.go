package payment

import (
	"context"
	"maps"

	corepayment "github.com/OpenNSW/core/payment"
	"github.com/OpenNSW/core/taskflow/orchestrator"
)

// StepCommand is the command a payment step is completed with. An authz rule on a
// payment step names it, e.g. "PENDING_PAYMENT": {"submit": [...]}.
const StepCommand = "submit"

// TaskCompleter is the corepayment.TaskCompleter the payment service settles a
// payment step through. core/payment completes the step with the payment's outcome
// and no command, and a step's authz rule is keyed by command, so this names
// StepCommand on the completion when it carries none.
type TaskCompleter struct {
	next corepayment.TaskCompleter
}

// NewTaskCompleter returns a TaskCompleter that completes payment steps through next.
func NewTaskCompleter(next corepayment.TaskCompleter) TaskCompleter {
	return TaskCompleter{next: next}
}

// CompleteTaskStepByToken implements corepayment.TaskCompleter. It copies payload
// rather than writing to it: the map belongs to the payment service.
func (c TaskCompleter) CompleteTaskStepByToken(ctx context.Context, token string, payload map[string]any) error {
	if command, _ := payload[orchestrator.CommandVar].(string); command != "" {
		return c.next.CompleteTaskStepByToken(ctx, token, payload)
	}
	withCommand := make(map[string]any, len(payload)+1)
	maps.Copy(withCommand, payload)
	withCommand[orchestrator.CommandVar] = StepCommand
	return c.next.CompleteTaskStepByToken(ctx, token, withCommand)
}
