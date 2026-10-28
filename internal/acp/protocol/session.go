package protocol

import (
	"context"
	"time"
)

type InitializeRequest struct {
	ProtocolVersion int
	ElicitationForm bool
	// NOTE: Compaction means the editor can show a compaction as its own
	// entry. The agent must not send CompactionUpdate without it.
	Compaction bool
}

type InitializeResponse struct {
	ProtocolVersion int
	AgentName       string
	AgentVersion    string

	LoadSession    bool
	ResumeSession  bool
	ListSessions   bool
	DeleteSessions bool
	CloseSession   bool

	PromptImage           bool
	PromptEmbeddedContext bool

	// NOTE: An editor passes MCP servers over HTTP or SSE only when the
	// agent says it can connect them.
	MCPHTTP bool
	MCPSSE  bool
}

type MCPTransport int

const (
	MCPTransportStdio MCPTransport = iota
	MCPTransportHTTP
	MCPTransportSSE
)

type NameValue struct {
	Name  string
	Value string
}

// NOTE: MCPServer is one server the editor wants the agent to use. Command,
// Args and Env apply to stdio; URL and Headers apply to HTTP and SSE.
type MCPServer struct {
	Name      string
	Transport MCPTransport
	Command   string
	Args      []string
	Env       []NameValue
	URL       string
	Headers   []NameValue
}

type NewSessionRequest struct {
	Cwd        string
	MCPServers []MCPServer
}

type NewSessionResponse struct {
	SessionID     string
	ConfigOptions []ConfigOption
	// NOTE: AfterSent runs once the response has been written. The editor
	// learns the session ID from that response and cannot route updates
	// for a session it has not heard of yet.
	AfterSent func(ctx context.Context)
}

type LoadSessionRequest struct {
	SessionID  string
	Cwd        string
	MCPServers []MCPServer
}

type LoadSessionResponse struct {
	ConfigOptions []ConfigOption
}

type ResumeSessionRequest struct {
	SessionID  string
	Cwd        string
	MCPServers []MCPServer
}

type ResumeSessionResponse struct {
	ConfigOptions []ConfigOption
}

type ListSessionsRequest struct {
	Cwd    string
	Cursor string
}

type ListSessionsResponse struct {
	Sessions   []SessionInfo
	NextCursor string
}

type SessionInfo struct {
	SessionID string
	Cwd       string
	Title     string
	UpdatedAt time.Time
}

type DeleteSessionRequest struct {
	SessionID string
}

type CloseSessionRequest struct {
	SessionID string
}

type CancelRequest struct {
	SessionID string
}

type ConfigCategory string

const (
	ConfigCategoryMode         ConfigCategory = "mode"
	ConfigCategoryModel        ConfigCategory = "model"
	ConfigCategoryModelConfig  ConfigCategory = "model_config"
	ConfigCategoryThoughtLevel ConfigCategory = "thought_level"
)

// NOTE: CurrentValue must always be one of Values.
type ConfigOption struct {
	ID           string
	Name         string
	Description  string
	Category     ConfigCategory
	CurrentValue string
	Values       []ConfigValue
}

type ConfigValue struct {
	Value       string
	Name        string
	Description string
}

type SetConfigOptionRequest struct {
	SessionID string
	ConfigID  string
	Value     string
}

type SetConfigOptionResponse struct {
	ConfigOptions []ConfigOption
}
