package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/agent/prompt"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// TestAgentPrompt_SubagentTemplatesRenderContextFiles pins the fix for
// subagents ignoring the project's AGENTS.md. Only the coder template
// used to render the loaded context files, so a delegated agent worked
// without the conventions the user had written down — and the data was
// present on the prompt struct the whole time, which is why asserting
// on promptData never caught it.
func TestAgentPrompt_SubagentTemplatesRenderContextFiles(t *testing.T) {
	const marker = "always use tabs in this project"

	dir := t.TempDir()
	globalDir := t.TempDir()
	t.Setenv("ANGELA_GLOBAL_CONFIG", globalDir)
	t.Setenv("ANGELA_GLOBAL_DATA", globalDir)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(marker), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "angela.json"),
		[]byte(`{
			"options": {"disable_default_providers": true},
			"providers": {
				"test": {
					"base_url": "http://127.0.0.1:0/v1",
					"api_key": "test",
					"models": [{"id": "test-model", "name": "Test"}]
				}
			}
		}`), 0o644))

	store, err := config.Init(dir, "", false)
	require.NoError(t, err)

	for _, id := range []string{
		config.AgentCoder,
		config.AgentTask,
		config.AgentExplore,
		config.AgentGeneral,
	} {
		t.Run(id, func(t *testing.T) {
			agentCfg, ok := store.Config().Agents[id]
			require.True(t, ok)

			p, err := agentPrompt(agentCfg, prompt.WithWorkingDir(dir))
			require.NoError(t, err)

			out, err := p.Build(context.Background(), "", "", store)
			require.NoError(t, err)
			require.Contains(t, out, "<project_context>",
				"%s must render the project context section", id)
			require.Contains(t, out, marker,
				"%s must include the project's AGENTS.md content", id)
		})
	}
}

// TestAgentPrompt_UnknownAgentRendersContextFiles covers the fallback
// path: an agent ID with no built-in template lands on the general
// template, which must carry context too.
func TestAgentPrompt_UnknownAgentRendersContextFiles(t *testing.T) {
	const marker = "house rule: no global state"

	dir := t.TempDir()
	globalDir := t.TempDir()
	t.Setenv("ANGELA_GLOBAL_CONFIG", globalDir)
	t.Setenv("ANGELA_GLOBAL_DATA", globalDir)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(marker), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "angela.json"),
		[]byte(`{
			"options": {"disable_default_providers": true},
			"providers": {
				"test": {
					"base_url": "http://127.0.0.1:0/v1",
					"api_key": "test",
					"models": [{"id": "test-model", "name": "Test"}]
				}
			}
		}`), 0o644))

	store, err := config.Init(dir, "", false)
	require.NoError(t, err)

	p, err := agentPrompt(config.Agent{ID: "reviewer", Name: "Reviewer"}, prompt.WithWorkingDir(dir))
	require.NoError(t, err)

	out, err := p.Build(context.Background(), "", "", store)
	require.NoError(t, err)
	require.Contains(t, out, marker)
}
