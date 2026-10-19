package tools

import "time"

// SubSessionKind says how a child session was dispatched.
type SubSessionKind string

const (
	// SubSessionKindUnknown marks a child dispatched by an earlier process,
	// whose dispatch mode was not persisted.
	SubSessionKindUnknown    SubSessionKind = ""
	SubSessionKindForeground SubSessionKind = "foreground"
	SubSessionKindBackground SubSessionKind = "background"
	SubSessionKindBranch     SubSessionKind = "branch"
)

// SubSessionStatus is the lifecycle state of a child session.
type SubSessionStatus string

const (
	SubSessionRunning   SubSessionStatus = "running"
	SubSessionCompleted SubSessionStatus = "completed"
	SubSessionFailed    SubSessionStatus = "failed"
	SubSessionStopped   SubSessionStatus = "stopped"
	// SubSessionEnded marks a child dispatched by an earlier process, whose
	// outcome was not persisted.
	SubSessionEnded SubSessionStatus = "ended"
)

// SubSession describes one direct child of a session.
type SubSession struct {
	ID        string           `json:"id"`
	ParentID  string           `json:"parent_id"`
	Agent     string           `json:"agent"`
	Task      string           `json:"task"`
	Kind      SubSessionKind   `json:"kind"`
	Status    SubSessionStatus `json:"status"`
	StartedAt time.Time        `json:"started_at"`
}

// SubSessionStatusOf maps the outcome of a background-style run onto the
// sub-session lifecycle.
func SubSessionStatusOf(status BackgroundAgentStatus) SubSessionStatus {
	switch status {
	case BackgroundAgentCompleted:
		return SubSessionCompleted
	case BackgroundAgentFailed:
		return SubSessionFailed
	case BackgroundAgentStopped:
		return SubSessionStopped
	default:
		return SubSessionRunning
	}
}
