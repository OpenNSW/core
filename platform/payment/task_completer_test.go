package payment

import (
	"context"
	"errors"
	"maps"
	"testing"

	"github.com/OpenNSW/core/taskflow/orchestrator"
)

// tokenRecorder records what reached the task manager.
type tokenRecorder struct {
	token   string
	payload map[string]any
	err     error
}

func (r *tokenRecorder) CompleteTaskStepByToken(_ context.Context, token string, payload map[string]any) error {
	r.token, r.payload = token, payload
	return r.err
}

func TestTaskCompleter_Command(t *testing.T) {
	tests := []struct {
		name    string
		payload map[string]any
		want    any
	}{
		// What the payment service sends: the outcome, no command.
		{"none", map[string]any{"payment_status": "success", "reference_number": "R1"}, StepCommand},
		{"empty", map[string]any{orchestrator.CommandVar: "", "payment_status": "fail"}, StepCommand},
		{"nil payload", nil, StepCommand},
		{"named", map[string]any{orchestrator.CommandVar: "settle", "payment_status": "success"}, "settle"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := maps.Clone(tc.payload)
			rec := &tokenRecorder{}

			if err := NewTaskCompleter(rec).CompleteTaskStepByToken(context.Background(), "tok", tc.payload); err != nil {
				t.Fatal(err)
			}

			if rec.token != "tok" {
				t.Errorf("token = %q, want it passed on", rec.token)
			}
			if got := rec.payload[orchestrator.CommandVar]; got != tc.want {
				t.Errorf("command = %v, want %v", got, tc.want)
			}
			for k, v := range tc.payload {
				if k != orchestrator.CommandVar && rec.payload[k] != v {
					t.Errorf("payload[%q] = %v, want %v passed on", k, rec.payload[k], v)
				}
			}
			if !maps.Equal(tc.payload, before) {
				t.Errorf("the payment service's map was changed: %v, want %v", tc.payload, before)
			}
		})
	}
}

func TestTaskCompleter_ReturnsTheTaskManagersError(t *testing.T) {
	stale := errors.New("stale step")
	err := NewTaskCompleter(&tokenRecorder{err: stale}).CompleteTaskStepByToken(context.Background(), "tok", map[string]any{})
	if !errors.Is(err, stale) {
		t.Errorf("err = %v, want the task manager's", err)
	}
}
