package prompt

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

func TestNewPrompt_RejectsMalformedTemplate(t *testing.T) {
	_, err := NewPrompt("x", "{{ unclosed")
	require.Error(t, err)
}

func TestPrompt_AgentContextPathsOverrideGlobal(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "global.md"), []byte("global content"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.md"), []byte("agent content"), 0o644))

	// Isolate the global config dir and disable default providers so
	// Init does not reach out to catwalk over the network or
	// touch the real user's ~/.local/share/angela.
	globalDir := t.TempDir()
	t.Setenv("ANGELA_GLOBAL_CONFIG", globalDir)
	t.Setenv("ANGELA_GLOBAL_DATA", globalDir)
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
	store.Config().Options.ContextPaths = []string{"global.md"}

	// No override: falls back to Config.Options.ContextPaths.
	pGlobal, err := NewPrompt("x", "body")
	require.NoError(t, err)
	dataGlobal, err := pGlobal.promptData(context.Background(), "", "", store)
	require.NoError(t, err)
	require.Len(t, dataGlobal.ContextFiles, 1)
	require.Equal(t, "global content", dataGlobal.ContextFiles[0].Content)

	// Agent-level override: only the agent's own path loads, not the global one.
	pAgent, err := NewPrompt("x", "body", WithContextPaths([]string{"agent.md"}))
	require.NoError(t, err)
	dataAgent, err := pAgent.promptData(context.Background(), "", "", store)
	require.NoError(t, err)
	require.Len(t, dataAgent.ContextFiles, 1)
	require.Equal(t, "agent content", dataAgent.ContextFiles[0].Content)
}

// TestPrompt_ContextPathsIgnoresEmptyEntry guards against an empty
// context path resolving to the working directory itself (Join drops
// empty elements) and pulling every file in the project in as
// "context" instead of being treated as absent.
func TestPrompt_ContextPathsIgnoresEmptyEntry(t *testing.T) {
	store := newContextTestStore(t, "remember the house style")
	require.NoError(t, os.WriteFile(filepath.Join(store.WorkingDir(), "other.md"), []byte("unrelated file"), 0o644))

	p, err := NewPrompt("x", "body", WithContextPaths([]string{""}))
	require.NoError(t, err)

	data, err := p.promptData(context.Background(), "", "", store)
	require.NoError(t, err)
	require.Empty(t, data.ContextFiles)
}

// TestPrompt_BuildRendersContextFiles asserts on the string Build
// actually returns. Checking promptData alone is what let a subagent
// template ship without ever rendering the context it had loaded: the
// data was right, the prompt still never mentioned the file.
func TestPrompt_BuildRendersContextFiles(t *testing.T) {
	store := newContextTestStore(t, "remember the house style")

	p, err := NewPrompt("x", `body
{{template "context_files" .}}`, WithContextPaths([]string{"AGENTS.md"}))
	require.NoError(t, err)

	out, err := p.Build(context.Background(), "", "", store)
	require.NoError(t, err)
	require.Contains(t, out, "<project_context>")
	require.Contains(t, out, "remember the house style")
	require.Contains(t, out, "AGENTS.md")
}

// TestPrompt_BuildOmitsContextSectionWhenEmpty keeps the sub-template
// from injecting an empty section into every prompt that has no
// context files to show.
func TestPrompt_BuildOmitsContextSectionWhenEmpty(t *testing.T) {
	store := newContextTestStore(t, "")

	p, err := NewPrompt("x", `body
{{template "context_files" .}}`, WithContextPaths([]string{"does-not-exist.md"}))
	require.NoError(t, err)

	out, err := p.Build(context.Background(), "", "", store)
	require.NoError(t, err)
	require.NotContains(t, out, "<project_context>")
}

// newContextTestStore builds a hermetic ConfigStore whose working dir
// holds an AGENTS.md with the given content.
func newContextTestStore(t *testing.T, agentsMD string) *config.ConfigStore {
	t.Helper()

	dir := t.TempDir()
	globalDir := t.TempDir()
	t.Setenv("ANGELA_GLOBAL_CONFIG", globalDir)
	t.Setenv("ANGELA_GLOBAL_DATA", globalDir)

	if agentsMD != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(agentsMD), 0o644))
	}
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
	return store
}
