package permission

import (
	"encoding/json"
	"fmt"
)

// Action is what a tool call wants to do, independent of which tool
// asked. Rules, grants and prompts are all keyed on it, so a new tool
// cannot invent a category that the policy has never seen.
type Action uint8

const (
	// ActionRead reads a path, or reads nothing at all.
	ActionRead Action = iota
	// ActionList enumerates a directory.
	ActionList
	// ActionEdit creates, modifies or removes a path.
	ActionEdit
	// ActionExecute runs a shell command.
	ActionExecute
	// ActionNetwork reaches a remote host.
	ActionNetwork
	// ActionMCP calls a tool or resource on an MCP server.
	ActionMCP
)

var actionNames = [...]string{
	ActionRead:    "read",
	ActionList:    "list",
	ActionEdit:    "edit",
	ActionExecute: "execute",
	ActionNetwork: "network",
	ActionMCP:     "mcp",
}

// actionAliases keep configuration written against the older free-form
// action strings working.
var actionAliases = map[string]Action{
	"download": ActionNetwork,
	"fetch":    ActionNetwork,
	"search":   ActionNetwork,
	"write":    ActionEdit,
}

func (a Action) String() string {
	if int(a) >= len(actionNames) {
		return "unknown"
	}
	return actionNames[a]
}

// ParseAction resolves a configured action name, accepting the legacy
// aliases that predate the enum.
func ParseAction(s string) (Action, bool) {
	for i, name := range actionNames {
		if name == s {
			return Action(i), true
		}
	}
	action, ok := actionAliases[s]
	return action, ok
}

func (a Action) MarshalJSON() ([]byte, error) {
	return json.Marshal(a.String())
}

func (a *Action) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return err
	}
	action, ok := ParseAction(name)
	if !ok {
		return fmt.Errorf("unknown permission action %q", name)
	}
	*a = action
	return nil
}

// Access describes one thing a tool call wants to do. Which payload
// fields carry meaning follows from Action: paths for read, list and
// edit, Command for execute, URL for network, and the server and tool
// name for MCP. A network access that lands on disk also carries Path,
// so the file it writes can be judged as an edit.
type Access struct {
	// Tool is the tool that asked, for display and telemetry only. No
	// decision may be made from it.
	Tool string
	// Action is the category the decision is keyed on.
	Action Action
	// Path is an absolute filesystem path, empty when the access
	// touches no file.
	Path string
	// Command is the shell command for ActionExecute.
	Command string
	// URL is the target for ActionNetwork.
	URL string
	// Server is the MCP server name for ActionMCP.
	Server string
	// MCPTool is the bare tool name on the MCP server.
	MCPTool string
}
