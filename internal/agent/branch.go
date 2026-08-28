package agent

import (
	"bytes"
	_ "embed"
	"text/template"
)

//go:embed templates/branch_fork_prompt.md.tpl
var branchForkPromptTmpl string

// branchForkPrompt is the first user message of a forked session. It arrives
// after the copied history, so it is what tells the model that the transcript
// above is inherited rather than its own.
type branchForkPrompt struct {
	ParentTitle string
	Prompt      string
}

func renderBranchForkPrompt(data branchForkPrompt) (string, error) {
	tmpl, err := template.New("branch_fork_prompt").Parse(branchForkPromptTmpl)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
