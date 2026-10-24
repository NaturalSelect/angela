package protocol

import "encoding/json"

type SessionNotification struct {
	SessionID string
	Update    SessionUpdate
}

// NOTE: Closed union; only the update types in this file implement it.
type SessionUpdate interface{ isSessionUpdate() }

// NOTE: Sent only while replaying history for session/load.
type UserMessageChunk struct {
	MessageID string
	Content   ContentBlock
}

type AgentMessageChunk struct {
	MessageID string
	Content   ContentBlock
}

type AgentThoughtChunk struct {
	MessageID string
	Content   ContentBlock
}

type ToolKind string

const (
	ToolKindRead    ToolKind = "read"
	ToolKindEdit    ToolKind = "edit"
	ToolKindDelete  ToolKind = "delete"
	ToolKindMove    ToolKind = "move"
	ToolKindSearch  ToolKind = "search"
	ToolKindExecute ToolKind = "execute"
	ToolKindThink   ToolKind = "think"
	ToolKindFetch   ToolKind = "fetch"
	ToolKindOther   ToolKind = "other"
)

type ToolCallStatus string

const (
	ToolCallStatusPending    ToolCallStatus = "pending"
	ToolCallStatusInProgress ToolCallStatus = "in_progress"
	ToolCallStatusCompleted  ToolCallStatus = "completed"
	ToolCallStatusFailed     ToolCallStatus = "failed"
)

type ToolCallStart struct {
	ID        string
	Name      string
	Title     string
	Kind      ToolKind
	Status    ToolCallStatus
	Locations []Location
	RawInput  json.RawMessage
}

// NOTE: Patch semantics: a nil pointer or nil slice leaves that field
// unchanged.
type ToolCallUpdate struct {
	ID        string
	Title     *string
	Status    *ToolCallStatus
	Content   []ToolCallContent
	Locations []Location
}

type ToolCallContent interface{ isToolCallContent() }

type ToolContent struct {
	Block ContentBlock
}

// NOTE: Path is absolute; OldText is nil for a new file.
type ToolDiff struct {
	Path    string
	OldText *string
	NewText string
}

type Location struct {
	Path string
	Line *uint32
}

type PlanPriority string

const (
	PlanPriorityHigh   PlanPriority = "high"
	PlanPriorityMedium PlanPriority = "medium"
	PlanPriorityLow    PlanPriority = "low"
)

type PlanStatus string

const (
	PlanStatusPending    PlanStatus = "pending"
	PlanStatusInProgress PlanStatus = "in_progress"
	PlanStatusCompleted  PlanStatus = "completed"
)

type PlanEntry struct {
	Content  string
	Priority PlanPriority
	Status   PlanStatus
}

type Plan struct {
	Entries []PlanEntry
}

type SessionInfoUpdate struct {
	Title *string
}

type UsageUpdate struct {
	Used int64
	Size int64
	Cost *Cost
}

type Cost struct {
	Amount   float64
	Currency string
}

type ConfigOptionUpdate struct {
	ConfigOptions []ConfigOption
}

func (UserMessageChunk) isSessionUpdate()   {}
func (AgentMessageChunk) isSessionUpdate()  {}
func (AgentThoughtChunk) isSessionUpdate()  {}
func (ToolCallStart) isSessionUpdate()      {}
func (ToolCallUpdate) isSessionUpdate()     {}
func (Plan) isSessionUpdate()               {}
func (SessionInfoUpdate) isSessionUpdate()  {}
func (UsageUpdate) isSessionUpdate()        {}
func (ConfigOptionUpdate) isSessionUpdate() {}

func (ToolContent) isToolCallContent() {}
func (ToolDiff) isToolCallContent()    {}
