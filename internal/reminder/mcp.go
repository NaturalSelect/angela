package reminder

import (
	_ "embed"
	"strings"
	"text/template"
)

// The two conditions are separate files because they are separate notices:
// a server that failed needs the user told, a server still starting only
// needs the model to wait.
var (
	//go:embed templates/mcp_failed.md.tpl
	mcpFailedTemplate string
	//go:embed templates/mcp_pending.md.tpl
	mcpPendingTemplate string

	mcpFailedTmpl  = template.Must(template.New("mcp_failed").Parse(mcpFailedTemplate))
	mcpPendingTmpl = template.Must(template.New("mcp_pending").Parse(mcpPendingTemplate))
)

// mcpUnavailable tells the model which MCP servers are missing from this
// turn. Without it a server that failed to connect is indistinguishable
// from a capability that was never configured, and the model reports the
// integration as nonexistent rather than broken.
//
// It fires on every turn the condition holds. That is deliberate: the tools
// stay missing for as long as the server is down, and the model cannot know
// which turn is the one where it would have mattered.
type mcpUnavailable struct{}

func (mcpUnavailable) Name() string { return "mcp_unavailable" }

func (mcpUnavailable) Collect(s State) string {
	var sections []string
	// Server names and connection errors originate in user config and in the
	// endpoints themselves, so neither can be trusted to stay inside the
	// reminder block.
	if text := renderList(mcpFailedTmpl, s.FailedMCPServers); text != "" {
		sections = append(sections, text)
	}
	if text := renderList(mcpPendingTmpl, s.PendingMCPServers); text != "" {
		sections = append(sections, text)
	}
	return strings.Join(sections, "\n\n")
}

// renderList renders a template over an escaped list, yielding "" for an
// empty list so callers can drop the section entirely.
func renderList(tmpl *template.Template, values []string) string {
	if len(values) == 0 {
		return ""
	}
	var out strings.Builder
	if err := tmpl.Execute(&out, escapeAll(values)); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}
