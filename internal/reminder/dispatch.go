package reminder

import (
	_ "embed"
	"strings"
	"text/template"

	"github.com/NaturalSelect/angela/internal/toolnames"
)

//go:embed templates/dispatch.md.tpl
var dispatchTemplate string

// dispatchText is rendered once at startup: the only value it
// interpolates is a compile-time constant.
var dispatchText = renderDispatch()

func renderDispatch() string {
	tmpl := template.Must(template.New("dispatch").Parse(dispatchTemplate))
	var out strings.Builder
	if err := tmpl.Execute(&out, struct{ AgentTool string }{AgentTool: toolnames.Agent}); err != nil {
		panic("rendering the dispatch reminder: " + err.Error())
	}
	return strings.TrimSpace(out.String())
}

// dispatch keeps the delegation question in front of the model. Which
// agents are worth reaching for unprompted is stated only in their own
// descriptions, and those sit in the tool list the model read many turns
// ago; this puts the question back next to the work.
type dispatch struct{}

func (dispatch) Name() string { return "dispatch" }

func (dispatch) Collect(s State) string {
	// A sub-agent is already the dedicated agent for its task, and an
	// agent the tool was filtered out of has nowhere to delegate to.
	if s.IsSubAgent || !s.CanDispatch {
		return ""
	}
	return dispatchText
}
