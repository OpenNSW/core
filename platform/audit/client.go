package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/OpenNSW/core/trace"

	"github.com/OpenNSW/nsw-srilanka/internal/authn"
)

// Auditor is the interface that services and handlers accept as an optional
// dependency for recording audit events. When the value is nil, no auditing
// occurs — all implementations must be nil-receiver safe.
type Auditor interface {
	Audit(ctx context.Context, e Event)
}

// Event is the domain-friendly shape a caller fills in.
type Event struct {
	EventType  EventType
	Action     Action
	TargetType TargetType
	TargetID   string
	Failure    bool
	Message    any // Marshaled to JSON; select fields deliberately
	Metadata   map[string]any
}

// ConsignmentEvent is a consignment audit event. Handlers pass the action,
// target id, whether it failed, and metadata. The event type and target type
// are fixed so each call site does not repeat them.
func ConsignmentEvent(action Action, targetID string, failure bool, metadata map[string]any) Event {
	return newEvent(EventConsignment, action, TargetConsignment, targetID, failure, metadata)
}

// TaskEvent is a task audit event. See ConsignmentEvent.
func TaskEvent(action Action, targetID string, failure bool, metadata map[string]any) Event {
	return newEvent(EventTask, action, TargetTask, targetID, failure, metadata)
}

func newEvent(eventType EventType, action Action, targetType TargetType, targetID string, failure bool, metadata map[string]any) Event {
	return Event{
		EventType:  eventType,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Failure:    failure,
		Metadata:   metadata,
	}
}

// Record is the enriched audit event delivered to every registered Sink.
type Record struct {
	Timestamp  time.Time
	EventType  EventType
	Action     Action
	Status     Status
	ActorType  ActorType
	ActorID    string
	TargetType TargetType
	TargetID   string
	TraceID    string
	Message    []byte
	Metadata   map[string]any
}

// Sink receives enriched audit records. Implementations must be safe for
// concurrent Write calls.
type Sink interface {
	Write(ctx context.Context, r Record)
}

// Client is the sink-capable audit client. Call sites use Auditor; bootstrap
// registers LogSink (category=audit, OTLP export only).
type Client struct {
	mu    sync.RWMutex
	sinks []Sink
}

// New returns a Client with no sinks. RegisterSink before use.
func New() *Client {
	return &Client{}
}

// CaptureSink records Writes for tests. It is exported so handler and router
// tests outside this package can assert audit emissions without mocking.
type CaptureSink struct {
	mu      sync.Mutex
	records []Record
}

// Write implements Sink.
func (c *CaptureSink) Write(_ context.Context, r Record) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, r)
}

// Records returns a copy of captured records.
func (c *CaptureSink) Records() []Record {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Record(nil), c.records...)
}

// NewWithCapture returns a Client that writes to a CaptureSink. Exported for
// tests that assert audit emissions.
func NewWithCapture() (*Client, *CaptureSink) {
	s := &CaptureSink{}
	c := New()
	c.RegisterSink(s)
	return c, s
}

// RegisterSink appends a sink. Nil client or sink is ignored. Intended for
// composition-root wiring only; not safe to call concurrently with Audit
// unless the caller accepts a possible missed write for the new sink.
func (c *Client) RegisterSink(s Sink) {
	if c == nil || s == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sinks = append(c.sinks, s)
}

// Audit enriches the event from context and fans out to all registered sinks.
// It is nil-receiver safe.
func (c *Client) Audit(ctx context.Context, e Event) {
	if c == nil {
		return
	}
	c.mu.RLock()
	sinks := append([]Sink(nil), c.sinks...)
	c.mu.RUnlock()
	if len(sinks) == 0 {
		return
	}

	actorType, actorID := actorFrom(ctx)

	status := StatusSuccess
	if e.Failure {
		status = StatusFailure
	}

	var msg []byte
	if e.Message != nil {
		if raw, ok := e.Message.([]byte); ok {
			msg = raw
		} else {
			var err error
			msg, err = json.Marshal(e.Message)
			if err != nil {
				slog.WarnContext(ctx, "audit: failed to marshal message", "error", err)
				msg = nil
			}
		}
	}

	rec := Record{
		Timestamp:  time.Now().UTC(),
		EventType:  e.EventType,
		Action:     e.Action,
		Status:     status,
		ActorType:  actorType,
		ActorID:    actorID,
		TargetType: e.TargetType,
		TargetID:   e.TargetID,
		Message:    msg,
		Metadata:   e.Metadata,
	}
	if tid := trace.GetTraceID(ctx); tid != "" {
		rec.TraceID = tid
	}

	// Detach from the request context after reading actor/trace so sink work
	// is not cancelled when the client disconnects.
	ctx = context.WithoutCancel(ctx)
	for _, s := range sinks {
		s.Write(ctx, rec)
	}
}

func actorFrom(ctx context.Context) (ActorType, string) {
	principal, ok := authn.FromContext(ctx)
	if !ok {
		return ActorSystem, "anonymous"
	}

	switch principal.Kind {
	case authn.KindClient:
		return ActorService, principal.Subject()
	case authn.KindUser:
		// Treated as ActorMember for now as no Admin role is defined in this phase.
		return ActorMember, principal.Subject()
	default:
		slog.ErrorContext(ctx, "audit: unrecognized principal kind encountered", "kind", principal.Kind)
		return ActorSystem, principal.Subject()
	}
}
