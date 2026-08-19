package proto

import (
	"charm.land/catwalk/pkg/catwalk"

	"github.com/NaturalSelect/angela/internal/config"
)

// Session represents a session in the proto layer.
//
// IsBusy is computed on read (it is not persisted with the session) and
// reflects whether an agent run is currently in flight for this session.
// It is populated by REST handlers in internal/server/proto.go from the
// workspace's AgentCoordinator. The Session SSE event path does not set
// it, since SSE consumers can compute presence from other agent signals.
//
// AttachedClients counts the number of clients currently viewing this
// session — i.e. entries in the workspace's clients map whose
// currentSessionID equals this session's ID and which have at least one
// live SSE stream. Hold-only clients (streams == 0) do not contribute.
// Like IsBusy, it is computed on read by REST handlers.
type Session struct {
	ID               string                  `json:"id"`
	ParentSessionID  string                  `json:"parent_session_id"`
	Title            string                  `json:"title"`
	Agent            string                  `json:"agent,omitempty"`
	ActiveAgent      config.ActiveAgentState `json:"active_agent,omitzero"`
	MessageCount     int64                   `json:"message_count"`
	PromptTokens     int64                   `json:"prompt_tokens"`
	CompletionTokens int64                   `json:"completion_tokens"`
	SummaryMessageID string                  `json:"summary_message_id"`
	Cost             float64                 `json:"cost"`
	Todos            []Todo                  `json:"todos,omitempty"`
	CreatedAt        int64                   `json:"created_at"`
	UpdatedAt        int64                   `json:"updated_at"`
	IsBusy           bool                    `json:"is_busy"`
	AttachedClients  int                     `json:"attached_clients"`
}

// ActiveAgent is everything the UI needs to name what a session is
// running: the agent, and the model with the session's parameter
// preset already folded in. The preset is applied server-side so a
// client never has to re-derive it and disagree.
type ActiveAgent struct {
	AgentID    string                 `json:"agent_id"`
	AgentName  string                 `json:"agent_name"`
	ModelName  config.ModelConfigName `json:"model_name,omitempty"`
	ModelCfg   config.SelectedModel   `json:"model_cfg,omitzero"`
	CatwalkCfg catwalk.Model          `json:"catwalk_cfg,omitzero"`
	Variant    string                 `json:"variant,omitempty"`
}

// Todo represents a single todo entry on a session in the proto layer.
type Todo struct {
	Content    string `json:"content"`
	Status     string `json:"status"`
	ActiveForm string `json:"active_form"`
}
