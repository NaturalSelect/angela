package reminder

import (
	_ "embed"
	"strings"
	"text/template"

	"github.com/NaturalSelect/angela/internal/toolnames"
)

//go:embed templates/background_agents_after_compaction.md.tpl
var backgroundAgentsAfterCompactionTemplate string

var backgroundAgentsAfterCompactionTmpl = template.Must(
	template.New("background_agents_after_compaction").Parse(backgroundAgentsAfterCompactionTemplate),
)

// backgroundAgentsAfterCompaction re-states which background agents are
// still running when the conversation was summarized. Their results arrive
// as notifications addressed by agent id, and the summary is the only place
// those ids survive, so a model that lost track of them launches the same
// work again.
//
// It fires only on the first turn after a summary, same as
// skillsAfterCompaction: the notice then lives in history like any other
// message.
type backgroundAgentsAfterCompaction struct{}

func (backgroundAgentsAfterCompaction) Name() string { return "background_agents_after_compaction" }

func (backgroundAgentsAfterCompaction) Collect(s State) string {
	if !s.Compacted || s.TurnsSinceCompaction > 0 || len(s.RunningBackgroundAgents) == 0 {
		return ""
	}
	var out strings.Builder
	err := backgroundAgentsAfterCompactionTmpl.Execute(&out, struct {
		Agents     []string
		StatusTool string
		StopTool   string
	}{
		// The task of an agent is text the model wrote from whatever it was
		// reading, including files a repository under review controls.
		Agents:     escapeAll(s.RunningBackgroundAgents),
		StatusTool: toolnames.AgentStatus,
		StopTool:   toolnames.AgentStop,
	})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}
