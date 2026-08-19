package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// toolDescriptionTemplates are the templates under templates/ that are
// not system prompts: they describe a tool to the model calling it, so
// they belong to a tool rather than to an agent and have no entry in
// builtinPromptForAgent.
var toolDescriptionTemplates = map[string]bool{
	"agent_tool.md.tpl": true,
	"agentic_fetch.md":  true,
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
		if toolDescriptionTemplates[name] {
			continue
		}
		require.Contains(t, registered, name,
			"template %q is embedded but no agent ID maps to it, so a user cannot override it: "+
				"register it in builtinPromptForAgent and builtinPromptTemplateFile, "+
				"or add it to toolDescriptionTemplates if it describes a tool", name)
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
