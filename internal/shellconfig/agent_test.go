package shellconfig

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoadShellConfig_AgentAdd verifies that `agent add` writes every flag
// to its documented JSON key, matching the agentAddFlags declarations in
// agent.go.
func TestLoadShellConfig_AgentAdd(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `agent add reviewer --description "Reviews code" --mode subagent --model small --prompt "Review the diff." --temperature 0.3 --tool bash --tool view --disable-tool sourcegraph`
	path := filepath.Join(dir, "angelarc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	agents := result["agents"].(map[string]any)
	reviewer := agents["reviewer"].(map[string]any)
	require.Equal(t, "Reviews code", reviewer["description"])
	require.Equal(t, "subagent", reviewer["mode"])
	require.Equal(t, "small", reviewer["model"])
	require.Equal(t, "Review the diff.", reviewer["prompt"])
	require.InDelta(t, 0.3, reviewer["temperature"], 0.0001)
	require.Equal(t, []any{"bash", "view"}, reviewer["allowed_tools"])
	require.Equal(t, []any{"sourcegraph"}, reviewer["disabled_tools"])
}

// TestLoadShellConfig_AgentAddDisabledFalse pins M3's shellconfig link: an
// explicit `--disabled false` must serialize as a present `"disabled":
// false` key, not be dropped as a Go zero value. Only a present false
// survives JSON unmarshal into *bool as a non-nil pointer, which is what
// lets this layer re-enable an agent a lower layer disabled.
func TestLoadShellConfig_AgentAddDisabledFalse(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `agent add reviewer --disabled false`
	path := filepath.Join(dir, "angelarc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	agents := result["agents"].(map[string]any)
	reviewer := agents["reviewer"].(map[string]any)
	require.Contains(t, reviewer, "disabled", "an explicit --disabled false must still write the key")
	require.Equal(t, false, reviewer["disabled"])
}

// TestLoadShellConfig_AgentRemove verifies remove/rm writes a disable
// tombstone. Dropping the key would only undo a definition made earlier
// in the same script; agents also come from built-in defaults and
// markdown files, which this layer can only suppress by overriding.
func TestLoadShellConfig_AgentRemove(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := "agent add reviewer --description \"Reviews code\"\nagent remove reviewer"
	path := filepath.Join(dir, "angelarc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	agents := result["agents"].(map[string]any)
	reviewer := agents["reviewer"].(map[string]any)
	require.Equal(t, true, reviewer["disabled"])
	require.NotContains(t, reviewer, "description",
		"remove must clear the earlier definition, not merge with it")
}

// TestLoadShellConfig_AgentRemoveWithoutPriorAdd covers the case the
// tombstone exists for: suppressing an agent this script never defined.
func TestLoadShellConfig_AgentRemoveWithoutPriorAdd(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "angelarc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte("agent remove explore"))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	agents := result["agents"].(map[string]any)
	require.Equal(t, true, agents["explore"].(map[string]any)["disabled"])
}

func TestLoadShellConfig_AgentRemoveCoderFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "angelarc")

	_, err := LoadShellConfig(t.Context(), path, []byte("agent remove coder"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot be removed")
}

func TestLoadShellConfig_AgentAddRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
	}{
		{"mode all was removed", `agent add reviewer --mode all`},
		{"unknown mode", `agent add reviewer --mode primray`},
		{"NaN temperature", `agent add reviewer --temperature NaN`},
		{"infinite temperature", `agent add reviewer --temperature Inf`},
		{"out of range temperature", `agent add reviewer --temperature 1.5`},
		{"unknown tools literal", `agent add reviewer --tools everything`},
		{"unknown mcp literal", `agent add reviewer --mcp everything`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "angelarc")
			_, err := LoadShellConfig(t.Context(), path, []byte(tt.script))
			require.Error(t, err)
		})
	}
}

func TestLoadShellConfig_AgentAddPermissionSets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `agent add reviewer --tools inherited --mcp-scope '{"github":["create_issue"]}'`
	path := filepath.Join(dir, "angelarc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	reviewer := result["agents"].(map[string]any)["reviewer"].(map[string]any)
	require.Equal(t, "inherited", reviewer["allowed_tools"])
	require.Equal(t, map[string]any{"github": []any{"create_issue"}}, reviewer["allowed_mcp"])
}
