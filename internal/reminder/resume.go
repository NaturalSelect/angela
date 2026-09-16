package reminder

import (
	_ "embed"
	"strings"
)

//go:embed templates/resume_after_compaction.md.tpl
var resumeAfterCompactionTemplate string

var resumeAfterCompactionText = strings.TrimSpace(resumeAfterCompactionTemplate)

// resumeAfterCompaction tells the model, on the first turn after a summary
// replaces the conversation, that the summary's own "All User Messages"
// section may quote the compaction trigger itself (e.g. "Provide a
// detailed summary of our conversation above"). Without this a model can
// read that quote as the live request, answer it by producing another
// summary, and stop — instead of picking up the real task the summary's
// "Next Step" section describes.
//
// It fires only on the first turn after a summary, same as
// skillsAfterCompaction: the notice then lives in history like any other
// message, and repeating it every turn would say nothing new.
type resumeAfterCompaction struct{}

func (resumeAfterCompaction) Name() string { return "resume_after_compaction" }

func (resumeAfterCompaction) Collect(s State) string {
	if !s.Compacted || s.TurnsSinceCompaction > 0 {
		return ""
	}
	return resumeAfterCompactionText
}
