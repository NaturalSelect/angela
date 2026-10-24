package protocol

import "time"

type InitializeRequest struct {
	ProtocolVersion int
	ElicitationForm bool
}

type InitializeResponse struct {
	ProtocolVersion int
	AgentName       string
	AgentVersion    string

	LoadSession    bool
	ListSessions   bool
	DeleteSessions bool

	PromptImage           bool
	PromptEmbeddedContext bool
}

type NewSessionRequest struct {
	Cwd string
}

type NewSessionResponse struct {
	SessionID     string
	ConfigOptions []ConfigOption
}

type LoadSessionRequest struct {
	SessionID string
	Cwd       string
}

type LoadSessionResponse struct {
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
