package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/skills"
)

// ReloadReport describes what a reload picked up.
type ReloadReport struct {
	ConfigPaths []string
	Skills      skills.ReloadDiff
	// SkillsReloaded is false when the app has no skills manager or the
	// manager could not rediscover skills.
	SkillsReloaded bool
	MCPServers     int
	Warnings       []string
}

// String renders the report as a short multi-line summary for the user and
// the model.
func (r ReloadReport) String() string {
	var b strings.Builder
	b.WriteString("Reloaded from disk.\n")
	fmt.Fprintf(&b, "Config files: %d\n", len(r.ConfigPaths))
	if r.SkillsReloaded {
		fmt.Fprintf(&b, "Skills: %d active (was %d)", r.Skills.After, r.Skills.Before)
		if len(r.Skills.Added) > 0 {
			fmt.Fprintf(&b, ", added %s", strings.Join(r.Skills.Added, ", "))
		}
		if len(r.Skills.Removed) > 0 {
			fmt.Fprintf(&b, ", removed %s", strings.Join(r.Skills.Removed, ", "))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "MCP servers configured: %d\n", r.MCPServers)
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "Warning: %s\n", w)
	}
	b.WriteString("Not reloaded: permission rules, LSP server definitions (use LSPRestart), TUI startup options. Changes apply from the next turn.")
	return b.String()
}

// Reload re-reads the configuration from disk, rediscovers skills, refreshes
// the sub-agent dispatch table and reconciles MCP servers against the new
// config. A configuration that fails to load aborts the reload and leaves
// every other piece of state as it was.
func (app *App) Reload(ctx context.Context) (ReloadReport, error) {
	app.reloadMu.Lock()
	defer app.reloadMu.Unlock()

	if err := app.config.ReloadFromDisk(ctx); err != nil {
		return ReloadReport{}, err
	}

	report := ReloadReport{
		ConfigPaths: app.config.LoadedPaths(),
		MCPServers:  len(app.config.Config().MCP),
	}

	if app.Skills != nil {
		diff, err := app.Skills.Reload()
		if err != nil {
			slog.Warn("Failed to reload skills", "error", err)
			report.Warnings = append(report.Warnings, fmt.Sprintf("skills were not reloaded: %v", err))
		} else {
			report.Skills = diff
			report.SkillsReloaded = true
		}
	}

	if app.AgentCoordinator != nil {
		if err := app.AgentCoordinator.UpdateModels(ctx); err != nil {
			slog.Warn("Failed to refresh agents after reload", "error", err)
			report.Warnings = append(report.Warnings, fmt.Sprintf("agents were not refreshed: %v", err))
		}
	}

	// NOTE: MCP sessions live as long as the context they are started with,
	// so this must be the app context and not the caller's.
	mcp.Reinitialize(app.globalCtx, app.config)

	return report, nil
}

// reloadForTool adapts Reload to the signature the AngelaReload tool expects.
func (app *App) reloadForTool(ctx context.Context) (string, error) {
	report, err := app.Reload(ctx)
	if err != nil {
		return "", err
	}
	return report.String(), nil
}
