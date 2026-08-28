package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

// TestInitializePrompt_OverridableViaConfig pins step 2.8's whole
// point: initialize makes no LLM call of its own, so its prompt is the
// only thing it owns — and that prompt must be reachable through the
// normal agent config path rather than frozen in the binary.
func TestInitializePrompt_OverridableViaConfig(t *testing.T) {
	const marker = "CUSTOM INITIALIZE MARKER"

	globalDir := t.TempDir()
	t.Setenv("ANGELA_GLOBAL_CONFIG", globalDir)
	t.Setenv("ANGELA_GLOBAL_DATA", globalDir)

	t.Run("override wins", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "angela.json"),
			[]byte(`{
				"options": {"disable_default_providers": true},
				"providers": {"test": {"base_url": "http://127.0.0.1:0/v1", "api_key": "test",
					"models": [{"id": "test-model", "name": "Test"}]}},
				"agents": {"initialize": {"prompt": "`+marker+`"}}
			}`), 0o644))

		store, err := config.Init(dir, "", false)
		require.NoError(t, err)

		out, err := InitializePrompt(store)
		require.NoError(t, err)
		require.Contains(t, out, marker)
	})

	t.Run("built-in template by default", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "angela.json"),
			[]byte(`{
				"options": {"disable_default_providers": true},
				"providers": {"test": {"base_url": "http://127.0.0.1:0/v1", "api_key": "test",
					"models": [{"id": "test-model", "name": "Test"}]}}
			}`), 0o644))

		store, err := config.Init(dir, "", false)
		require.NoError(t, err)

		out, err := InitializePrompt(store)
		require.NoError(t, err)
		require.NotContains(t, out, marker)
		require.NotEmpty(t, out)
	})
}

// TestAgentPrompt_BranchPreamble covers the three ways agentPrompt resolves a
// prompt. A branch runs on an agent the user wrote, so the preamble cannot
// live in any template: whichever way the body is resolved — and even when a
// custom prompt replaces the template outright — the branch rules have to
// still be there, and still be first.
func TestAgentPrompt_BranchPreamble(t *testing.T) {
	const customMarker = "REVIEW EVERY DIFF TWICE"

	dir := t.TempDir()
	globalDir := t.TempDir()
	t.Setenv("ANGELA_GLOBAL_CONFIG", globalDir)
	t.Setenv("ANGELA_GLOBAL_DATA", globalDir)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "angela.json"),
		[]byte(`{
			"options": {"disable_default_providers": true},
			"providers": {"test": {"base_url": "http://127.0.0.1:0/v1", "api_key": "test",
				"models": [{"id": "test-model", "name": "Test"}]}}
		}`), 0o644))

	store, err := config.Init(dir, "", false)
	require.NoError(t, err)

	preamble := strings.TrimRight(string(branchPreambleTmpl), "\n")

	build := func(t *testing.T, agentCfg config.Agent) string {
		t.Helper()
		p, err := agentPrompt(agentCfg, prompt.WithWorkingDir(dir))
		require.NoError(t, err)
		out, err := p.Build(context.Background(), "", "", store)
		require.NoError(t, err)
		return out
	}

	t.Run("custom prompt", func(t *testing.T) {
		out := build(t, config.Agent{
			ID: "pairing", Mode: config.AgentModeBranch, Prompt: customMarker,
		})
		require.True(t, strings.HasPrefix(out, preamble),
			"the branch rules must lead the prompt, not trail the user's")
		require.Contains(t, out, customMarker,
			"the preamble replaced the user's prompt instead of fronting it")
	})

	t.Run("built-in template", func(t *testing.T) {
		out := build(t, config.Agent{ID: config.AgentGeneral, Mode: config.AgentModeBranch})
		require.True(t, strings.HasPrefix(out, preamble))
	})

	t.Run("unknown agent falls back to general", func(t *testing.T) {
		out := build(t, config.Agent{ID: "nobody", Mode: config.AgentModeBranch})
		require.True(t, strings.HasPrefix(out, preamble))
	})

	t.Run("not a branch", func(t *testing.T) {
		out := build(t, config.Agent{
			ID: "pairing", Mode: config.AgentModeSubagent, Prompt: customMarker,
		})
		require.False(t, strings.HasPrefix(out, preamble),
			"an ordinary subagent was told it is a branch")
		require.Contains(t, out, customMarker)
	})
}
