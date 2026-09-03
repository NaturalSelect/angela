package config

import "fmt"

// Scope determines which config file is targeted for read/write operations.
type Scope int

const (
	// ScopeGlobal targets the global config (~/.config/angela/angela.json).
	ScopeGlobal Scope = iota
	// ScopeWorkspace targets the workspace config (.angela/angela.json).
	ScopeWorkspace
	// ScopeEphemeral applies the change in memory for the current
	// process only. It never touches a config file, so it does not
	// survive a config reload or a restart.
	ScopeEphemeral
)

// String returns a human-readable label for the scope.
func (s Scope) String() string {
	switch s {
	case ScopeGlobal:
		return "global"
	case ScopeWorkspace:
		return "workspace"
	case ScopeEphemeral:
		return "ephemeral"
	default:
		return fmt.Sprintf("Scope(%d)", int(s))
	}
}

// ErrNoWorkspaceConfig is returned when a workspace-scoped write is
// attempted on a ConfigStore that has no workspace config path.
var ErrNoWorkspaceConfig = fmt.Errorf("no workspace config path configured")
