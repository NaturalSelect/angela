// Package reminder builds the out-of-band notices Angela injects into a
// conversation to nudge the model about state it cannot otherwise observe.
//
// It is a leaf package: it depends on nothing but the standard library and
// internal/toolnames, so any layer that assembles a prompt can import it.
package reminder

import (
	"regexp"
	"strings"
)

// Tag wraps every notice sent to the model.
const Tag = "system-reminder"

// State is what a Source may consult. The agent derives it from the session
// so this package stays free of session, config and message types.
type State struct {
	IsSubAgent      bool
	TurnsSinceTodos int
	// Compacted reports that the conversation was summarized, so everything
	// before the summary is gone from the model's view.
	Compacted bool
	// TurnsSinceCompaction counts the assistant turns taken since that
	// summary.
	TurnsSinceCompaction int
	// LoadedSkills names the skills read during this session.
	LoadedSkills []string
	// FailedMCPServers and PendingMCPServers describe servers whose tools
	// are absent from this turn, one human-readable line each.
	FailedMCPServers  []string
	PendingMCPServers []string
	// UserReminders holds the standing notices from the user's config,
	// used verbatim.
	UserReminders []string
}

// Source produces at most one notice per turn. Returning an empty string
// means the source has nothing to say this turn.
type Source interface {
	Name() string
	Collect(State) string
}

// Notice is a rendered reminder together with the source that produced it,
// so callers can report which one fired.
type Notice struct {
	Source string
	Text   string
}

// DefaultSources returns the sources every main-agent turn consults. The
// user's own notices come first: the built-in ones are situational and gain
// from sitting closest to the model's next turn.
func DefaultSources() []Source {
	return []Source{
		user{},
		todoRecency{},
		mcpUnavailable{},
		skillsAfterCompaction{},
	}
}

// Collect asks every source for a notice and keeps the ones that fired.
func Collect(sources []Source, s State) []Notice {
	var notices []Notice
	for _, src := range sources {
		if text := src.Collect(s); text != "" {
			notices = append(notices, Notice{Source: src.Name(), Text: text})
		}
	}
	return notices
}

// Wrap encloses a notice in the reminder tag.
func Wrap(text string) string {
	return "<" + Tag + ">\n" + text + "\n</" + Tag + ">"
}

// tagPattern matches an opening or closing reminder tag in any case, and
// with either separator. The underscore spelling is matched because
// sessions recorded before the tag was renamed still hold it.
var tagPattern = regexp.MustCompile(`(?i)</?system[-_]reminder>`)

// Escape neutralizes reminder tags in untrusted text so file contents or
// command output cannot close the block and pose as a reminder. Text
// without a tag is returned unchanged.
func Escape(untrusted string) string {
	return tagPattern.ReplaceAllStringFunc(untrusted, func(tag string) string {
		return "&lt;" + strings.Trim(tag, "<>") + "&gt;"
	})
}

// escapeAll applies Escape to every entry of a list bound for a template.
func escapeAll(values []string) []string {
	escaped := make([]string, len(values))
	for i, v := range values {
		escaped[i] = Escape(v)
	}
	return escaped
}
