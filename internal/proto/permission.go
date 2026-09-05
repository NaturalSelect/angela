package proto

import (
	"encoding/json"

	"github.com/NaturalSelect/angela/internal/toolnames"
)

// CreatePermissionRequest represents a request to create a permission.
type CreatePermissionRequest struct {
	SessionID   string `json:"session_id"`
	ToolCallID  string `json:"tool_call_id"`
	ToolName    string `json:"tool_name"`
	Description string `json:"description"`
	Action      string `json:"action"`
	Params      any    `json:"params"`
	Path        string `json:"path"`
}

// PermissionNotification represents a notification about a permission change.
type PermissionNotification struct {
	ToolCallID string `json:"tool_call_id"`
	Granted    bool   `json:"granted"`
	Denied     bool   `json:"denied"`
}

// PermissionRequest represents a pending permission request.
type PermissionRequest struct {
	ID          string `json:"id"`
	SessionID   string `json:"session_id"`
	ToolCallID  string `json:"tool_call_id"`
	ToolName    string `json:"tool_name"`
	Description string `json:"description"`
	Action      string `json:"action"`
	Params      any    `json:"params"`
	Path        string `json:"path"`
	// DenyReason is an optional explanation the user attaches when
	// denying this request.
	DenyReason string `json:"deny_reason,omitempty"`
}

// UnmarshalJSON implements the json.Unmarshaler interface. This is needed
// because the Params field is of type any, so we need to unmarshal it into
// its appropriate type based on the [PermissionRequest.ToolName].
func (p *PermissionRequest) UnmarshalJSON(data []byte) error {
	type Alias PermissionRequest
	aux := &struct {
		Params json.RawMessage `json:"params"`
		*Alias
	}{
		Alias: (*Alias)(p),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	params, err := unmarshalToolParams(p.ToolName, aux.Params)
	if err != nil {
		return err
	}
	p.Params = params
	return nil
}

// UnmarshalJSON implements the json.Unmarshaler interface. This is needed
// because the Params field is of type any, so we need to unmarshal it into
// its appropriate type based on the [CreatePermissionRequest.ToolName].
func (p *CreatePermissionRequest) UnmarshalJSON(data []byte) error {
	type Alias CreatePermissionRequest
	aux := &struct {
		Params json.RawMessage `json:"params"`
		*Alias
	}{
		Alias: (*Alias)(p),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	params, err := unmarshalToolParams(p.ToolName, aux.Params)
	if err != nil {
		return err
	}
	p.Params = params
	return nil
}

func unmarshalToolParams(toolName string, raw json.RawMessage) (any, error) {
	switch toolName {
	case toolnames.Bash:
		var params BashPermissionsParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return params, nil
	case toolnames.Download:
		var params DownloadPermissionsParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return params, nil
	case toolnames.Edit:
		var params EditPermissionsParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return params, nil
	case toolnames.Write:
		var params WritePermissionsParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return params, nil
	case toolnames.MultiEdit:
		var params MultiEditPermissionsParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return params, nil
	case toolnames.Fetch:
		var params FetchPermissionsParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return params, nil
	case toolnames.WebFetch:
		var params WebFetchPermissionsParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return params, nil
	case toolnames.WebSearch:
		var params WebSearchPermissionsParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return params, nil
	case toolnames.View:
		var params ViewPermissionsParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return params, nil
	case toolnames.LS:
		var params LSPermissionsParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return params, nil
	case toolnames.LSPReplaceSymbol:
		var params ReplaceSymbolPermissionsParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return params, nil
	case toolnames.ListMCPResources:
		var params ListMCPResourcesPermissionsParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return params, nil
	case toolnames.ReadMCPResource:
		var params ReadMCPResourcePermissionsParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return params, nil
	case toolnames.LSPRename:
		var params RenamePermissionsParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		return params, nil
	default:
		// For unknown tools, keep the raw JSON as-is.
		var generic map[string]any
		if err := json.Unmarshal(raw, &generic); err != nil {
			return nil, err
		}
		return generic, nil
	}
}
