package reminder

import (
	_ "embed"
	"strings"
	"text/template"

	"github.com/NaturalSelect/angela/internal/toolnames"
)

//go:embed templates/todo_recency.md.tpl
var todoRecencyTemplate string

// todoRecencyNudgeEvery is how many turns without a todos call must pass
// before the model is nudged, and the gap between repeats. Without the
// repeat gap the nudge would fire on every turn once the threshold is
// crossed.
const todoRecencyNudgeEvery = 3

// todoRecencyText is rendered once at startup: the only value it
// interpolates is a compile-time constant.
var todoRecencyText = renderTodoRecency()

func renderTodoRecency() string {
	tmpl := template.Must(template.New("todo_recency").Parse(todoRecencyTemplate))
	var out strings.Builder
	if err := tmpl.Execute(&out, struct{ TodosTool string }{TodosTool: toolnames.Todos}); err != nil {
		panic("rendering the todo recency reminder: " + err.Error())
	}
	return strings.TrimSpace(out.String())
}

// todoRecency nudges the model toward the todos tool when it has gone
// several turns without touching the list. It deliberately asserts nothing
// about the list's contents: the reminder it replaced claimed the list was
// empty without ever checking, which misled the model whenever it wasn't.
type todoRecency struct{}

func (todoRecency) Name() string { return "todo_recency" }

func (todoRecency) Collect(s State) string {
	if s.IsSubAgent {
		return ""
	}
	if s.TurnsSinceTodos < todoRecencyNudgeEvery || s.TurnsSinceTodos%todoRecencyNudgeEvery != 0 {
		return ""
	}
	return todoRecencyText
}
