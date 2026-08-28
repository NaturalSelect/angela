package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// nonSystemPromptTemplates are the templates under templates/ that no agent
// ID maps to, because none of them is an agent's system prompt: one describes
// a tool to the model calling it, one is a turn's user message, and one is a
// preamble the branch machinery prepends to whatever prompt the user
// configured. The preamble is deliberately not overridable — it states the
// rules that keep a suspended parent from hanging forever.
var nonSystemPromptTemplates = map[string]bool{
	"agent_tool.md.tpl":         true,
	"branch_fork_prompt.md.tpl": true,
	"branch_preamble.md.tpl":    true,
}

// TestEveryTemplateIsReachableAsAnAgentPrompt is the standing guard
// against a shadow agent: a prompt shipped in the binary that no agent
// ID maps to cannot be overridden through config, so the user has no
// way to reach it. Registering the template is what makes it
// customizable.
func TestEveryTemplateIsReachableAsAnAgentPrompt(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir("templates")
	require.NoError(t, err)

	registered := make(map[string]string, len(builtinPromptTemplateFile))
	for agentID, file := range builtinPromptTemplateFile {
		registered[file] = agentID
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if ext := filepath.Ext(name); ext != ".md" && ext != ".tpl" {
			continue
		}
		if nonSystemPromptTemplates[name] {
			continue
		}
		require.Contains(t, registered, name,
			"template %q is embedded but no agent ID maps to it, so a user cannot override it: "+
				"register it in builtinPromptForAgent and builtinPromptTemplateFile, "+
				"or add it to nonSystemPromptTemplates if it is not a system prompt", name)
	}
}

// TestEveryRegisteredTemplateExists catches the reverse drift: a
// renamed or deleted template that the tables still point at.
func TestEveryRegisteredTemplateExists(t *testing.T) {
	t.Parallel()

	for agentID, file := range builtinPromptTemplateFile {
		_, err := os.Stat(filepath.Join("templates", file))
		require.NoError(t, err, "agent %q maps to a template that does not exist", agentID)

		require.Contains(t, builtinPromptForAgent, agentID,
			"agent %q names a template but has no prompt builder", agentID)
	}

	for agentID := range builtinPromptForAgent {
		require.Contains(t, builtinPromptTemplateFile, agentID,
			"agent %q has a prompt builder but names no template", agentID)
	}
}
