package mcp

import (
	"log/slog"
	"slices"

	"github.com/NaturalSelect/angela/internal/config"
)

// NOTE: Stops each container server for which reason returns an error and leaves it in StateError with that error, which is the one a restart would meet. It does not wait for the containers to go; containermcp.WaitIdle does.
func StopContainerServers(cfg *config.ConfigStore, reason func(name string) error) []string {
	if reason == nil {
		return nil
	}
	current := cfg.Config().MCP
	var stopped []string
	for name, info := range states.Copy() {
		var m config.MCPConfig
		switch info.State {
		case StateStarting:
			if info.PendingConfig != nil {
				m = *info.PendingConfig
			} else {
				m = current[name]
			}
		case StateConnected:
			m = info.Config
		default:
			continue
		}
		if m.Type != config.MCPContainer {
			continue
		}
		err := reason(name)
		if err == nil {
			continue
		}
		slog.Info("Stopping container MCP server before the sandbox is entered", "name", name)
		teardown(name)
		updateState(name, StateError, err, nil, Counts{})
		stopped = append(stopped, name)
	}
	slices.Sort(stopped)
	return stopped
}
