package audit

// EventType, Action, TargetType and ActorType are the string vocabulary written
// into audit log records (category=audit).
type EventType string
type Action string
type TargetType string
type ActorType string

// Status is the outcome of an audited action.
type Status string

const (
	EventConsignment EventType = "CONSIGNMENT_EVENT"
	EventTask        EventType = "TASK_EVENT"
	EventStorage     EventType = "STORAGE_EVENT"
	EventPayment     EventType = "PAYMENT_EVENT"
	EventUserMgmt    EventType = "USER_MANAGEMENT"
	EventCustoms     EventType = "CUSTOMS_EVENT"

	ActionCreate        Action = "CREATE"
	ActionRead          Action = "READ"
	ActionUpdate        Action = "UPDATE"
	ActionDelete        Action = "DELETE"
	ActionPresignUpload Action = "PRESIGN_UPLOAD"

	TargetConsignment     TargetType = "CONSIGNMENT"
	TargetTask            TargetType = "TASK"
	TargetStorage         TargetType = "STORAGE_OBJECT"
	TargetPayment         TargetType = "PAYMENT"
	TargetDeclaration     TargetType = "CUSTOMS_DECLARATION"
	TargetDispatchNote    TargetType = "CARGO_DISPATCH_NOTE"
	TargetCustomsCallback TargetType = "CUSTOMS_CALLBACK"

	ActorAdmin   ActorType = "ADMIN"
	ActorMember  ActorType = "MEMBER"
	ActorService ActorType = "SERVICE"
	ActorSystem  ActorType = "SYSTEM"

	StatusSuccess Status = "SUCCESS"
	StatusFailure Status = "FAILURE"
)
