package reminder

import (
	_ "embed"
	"strings"
	"text/template"

	"github.com/NaturalSelect/angela/internal/toolnames"
)

//go:embed templates/skills_after_compaction.md.tpl
var skillsAfterCompactionTemplate string

var skillsAfterCompactionTmpl = template.Must(
	template.New("skills_after_compaction").Parse(skillsAfterCompactionTemplate),
)

// skillsAfterCompaction re-states which skills were read before the
// conversation was summarized. Compaction drops the messages that carried
// their instructions, but the system prompt still lists those skills as
// available, so the model is left believing it is following guidance it can
// no longer see.
//
// It fires only on the first turn after a summary: the notice then lives in
// history like any other message, and repeating it every turn would say
// nothing new.
type skillsAfterCompaction struct{}

func (skillsAfterCompaction) Name() string { return "skills_after_compaction" }

func (skillsAfterCompaction) Collect(s State) string {
	if !s.Compacted || s.TurnsSinceCompaction > 0 || len(s.LoadedSkills) == 0 {
		return ""
	}
	var out strings.Builder
	err := skillsAfterCompactionTmpl.Execute(&out, struct {
		Skills   []string
		ViewTool string
	}{
		// Skill names come from files on disk, including ones a repository
		// under review may control.
		Skills:   escapeAll(s.LoadedSkills),
		ViewTool: toolnames.View,
	})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}
