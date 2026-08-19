package config

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/stretchr/testify/require"
)

// TestCloneForWrite_Isolation verifies that mutating a clone never reaches
// back into the original Config. This is the contract the store's
// copy-on-write mutators depend on for race-free publishing.
func TestCloneForWrite_Isolation(t *testing.T) {
	t.Parallel()

	orig := &Config{
		Models: map[ModelConfigName]SelectedModel{
			ModelMain: {Provider: "openai", Model: "gpt-4"},
		},
		RecentModels: map[ModelConfigName][]SelectedModel{
			ModelMain: {{Provider: "openai", Model: "gpt-4"}},
		},
		MCP:       MCPs{"a": {}},
		Providers: csync.NewMap[string, ProviderConfig](),
		Options: &Options{
			TUI: &TUIOptions{CompactMode: false},
		},
	}

	clone := orig.cloneForWrite()

	// Mutate every field the typed mutators touch.
	clone.Models[ModelMain] = SelectedModel{Provider: "anthropic", Model: "claude"}
	clone.RecentModels[ModelMain] = []SelectedModel{{Provider: "anthropic", Model: "claude"}}
	clone.MCP["b"] = MCPConfig{}
	clone.Options.TUI.CompactMode = true
	enabled := true
	clone.Options.TUI.Transparent = &enabled

	// The original must be untouched.
	require.Equal(t, "openai", orig.Models[ModelMain].Provider, "Models leaked")
	require.Equal(t, "openai", orig.RecentModels[ModelMain][0].Provider, "RecentModels leaked")
	require.NotContains(t, orig.MCP, "b", "MCP leaked")
	require.False(t, orig.Options.TUI.CompactMode, "Options.TUI.CompactMode leaked")
	require.Nil(t, orig.Options.TUI.Transparent, "Options.TUI.Transparent leaked")
}

// TestActiveAgentCloneSharesNothing pins C2. A session's instance is
// handed out from the published config, so anything the clone still
// aliases is state two sessions share — and that the global config
// hands out again on the next instantiation. The shallow copy reached
// exactly one level; every field below walks one of the paths it
// missed.
func TestActiveAgentCloneSharesNothing(t *testing.T) {
	t.Parallel()

	// Every field gets its own variable: sharing one would let a
	// single mutation trip an unrelated assertion and point at the
	// wrong path.
	agentDisabled, agentHidden := true, true
	agentTokens, agentTemp := int64(100), 0.5
	modelTemp, modelTopK := 0.5, int64(100)
	variantEffort, variantTokens := "low", int64(100)
	pick := "low"

	orig := ActiveAgent{
		Agent: Agent{
			ID:            "coder",
			Disabled:      &agentDisabled,
			Hidden:        &agentHidden,
			MaxTokens:     &agentTokens,
			Temperature:   &agentTemp,
			DisabledTools: []string{"bash"},
			ContextPaths:  []string{"AGENTS.md"},
			AllowedTools:  &AllowedToolSet{Kind: ToolSetScope, Tools: []string{"view"}},
			AllowedMCP: &AllowedMCPSet{
				Kind:    ToolSetScope,
				Servers: map[string][]string{"github": {"create_issue"}},
			},
		},
		Model: SelectedModel{
			Provider:    "openai",
			Model:       "gpt-4",
			Temperature: &modelTemp,
			TopK:        &modelTopK,
			ProviderOptions: map[string]any{
				"extra_body": map[string]any{"beta": "on"},
				"stop":       []any{"END"},
			},
			Variants: map[string]SelectedModelOverride{
				"deep": {
					ReasoningEffort: &variantEffort,
					MaxTokens:       &variantTokens,
					ProviderOptions: map[string]any{"nested": map[string]any{"k": "v"}},
				},
			},
		},
		VariantPick: &pick,
	}

	clone := orig.Clone()

	// Mutate every mutable thing the clone reaches.
	*clone.Agent.Disabled = false
	*clone.Agent.Hidden = false
	*clone.Agent.MaxTokens = 999
	*clone.Agent.Temperature = 9.9
	clone.Agent.DisabledTools[0] = "edit"
	clone.Agent.ContextPaths[0] = "OTHER.md"
	clone.Agent.AllowedTools.Tools[0] = "write"
	clone.Agent.AllowedMCP.Servers["github"][0] = "delete_repo"
	clone.Agent.AllowedMCP.Servers["gitlab"] = []string{"anything"}

	*clone.Model.Temperature = 9.9
	*clone.Model.TopK = 999
	clone.Model.ProviderOptions["extra_body"].(map[string]any)["beta"] = "off"
	clone.Model.ProviderOptions["stop"].([]any)[0] = "STOP"
	clone.Model.ProviderOptions["added"] = true

	deepVariant := clone.Model.Variants["deep"]
	*deepVariant.ReasoningEffort = "high"
	*deepVariant.MaxTokens = 999
	deepVariant.ProviderOptions["nested"].(map[string]any)["k"] = "changed"
	clone.Model.Variants["extra"] = SelectedModelOverride{}

	*clone.VariantPick = "high"

	// Nothing above may be visible through the original.
	require.True(t, *orig.Agent.Disabled, "Agent.Disabled leaked")
	require.True(t, *orig.Agent.Hidden, "Agent.Hidden leaked")
	require.Equal(t, int64(100), *orig.Agent.MaxTokens, "Agent.MaxTokens leaked")
	require.InDelta(t, 0.5, *orig.Agent.Temperature, 0.0001, "Agent.Temperature leaked")
	require.Equal(t, []string{"bash"}, orig.Agent.DisabledTools, "Agent.DisabledTools leaked")
	require.Equal(t, []string{"AGENTS.md"}, orig.Agent.ContextPaths, "Agent.ContextPaths leaked")
	require.Equal(t, []string{"view"}, orig.Agent.AllowedTools.Tools, "AllowedTools.Tools leaked")
	require.Equal(t, []string{"create_issue"}, orig.Agent.AllowedMCP.Servers["github"], "AllowedMCP tool list leaked")
	require.NotContains(t, orig.Agent.AllowedMCP.Servers, "gitlab", "AllowedMCP.Servers leaked")

	require.InDelta(t, 0.5, *orig.Model.Temperature, 0.0001, "Model.Temperature leaked")
	require.Equal(t, int64(100), *orig.Model.TopK, "Model.TopK leaked")
	require.Equal(t, "on", orig.Model.ProviderOptions["extra_body"].(map[string]any)["beta"],
		"nested ProviderOptions map leaked")
	require.Equal(t, "END", orig.Model.ProviderOptions["stop"].([]any)[0],
		"nested ProviderOptions slice leaked")
	require.NotContains(t, orig.Model.ProviderOptions, "added", "ProviderOptions leaked")

	origVariant := orig.Model.Variants["deep"]
	require.Equal(t, "low", *origVariant.ReasoningEffort, "variant ReasoningEffort leaked")
	require.Equal(t, int64(100), *origVariant.MaxTokens, "variant MaxTokens leaked")
	require.Equal(t, "v", origVariant.ProviderOptions["nested"].(map[string]any)["k"],
		"variant nested ProviderOptions leaked")
	require.NotContains(t, orig.Model.Variants, "extra", "Variants leaked")

	require.Equal(t, "low", *orig.VariantPick, "VariantPick leaked")
}
