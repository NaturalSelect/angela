package reminder

import (
	_ "embed"
	"strings"
	"text/template"
)

//go:embed templates/user.md.tpl
var userTemplate string

var userTmpl = template.Must(template.New("user").Parse(userTemplate))

// user carries the standing notices the user put in their config. A context
// file would say the same thing, but it sits in the system prompt and fades
// as the conversation grows; a reminder is re-sent at the end of every turn,
// so it stays in view.
//
// It fires on every turn, for subagents too: a standing instruction about
// how Angela should behave applies to any turn that writes code.
type user struct{}

func (user) Name() string { return "user" }

func (user) Collect(s State) string {
	entries := nonBlank(s.UserReminders)
	if len(entries) == 0 {
		return ""
	}
	var out strings.Builder
	// The text is the user's own config, as trusted as a hook command, so
	// it goes through verbatim. Escaping it would mangle any angle bracket
	// the user meant to write.
	if err := userTmpl.Execute(&out, strings.Join(entries, "\n\n")); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

// nonBlank drops entries that would render as an empty line, so a stray ""
// in the config does not produce a reminder with nothing in it.
func nonBlank(values []string) []string {
	kept := make([]string, 0, len(values))
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			kept = append(kept, trimmed)
		}
	}
	return kept
}
