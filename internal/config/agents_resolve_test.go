package config

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// newAgentTestConfig returns a Config whose AgentPaths point at a temp
// directory containing the given markdown agent files (name -> content),
// so tests can exercise the markdown merge layer without touching global
// or project directories.
func newAgentTestConfig(t *testing.T, mdFiles map[string]string) *Config {
	t.Helper()
	cfg := &Config{Options: &Options{}}
	if len(mdFiles) == 0 {
		return cfg
	}
	dir := t.TempDir()
	for name, content := range mdFiles {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0o644))
	}
	cfg.Options.AgentPaths = []string{dir}
	return cfg
}

func TestResolveAgents_ThreeLayerOverride(t *testing.T) {
	cfg := newAgentTestConfig(t, map[string]string{
		"explore": "---\ndescription: Custom explore description\n---\n",
	})
	cfg.AgentConfigs = map[string]Agent{
		AgentExplore: {Model: ModelChore},
	}

	agents := cfg.ResolveAgents()
	explore, ok := agents[AgentExplore]
	require.True(t, ok)
	require.Equal(t, "Custom explore description", explore.Description, "markdown layer should win for description")
	require.Equal(t, ModelChore, explore.Model, "JSON layer should win for model")
	require.Equal(t, AgentModeSubagent, explore.Mode, "builtin default should survive when no layer overrides it")
}

func TestResolveAgents_Idempotent(t *testing.T) {
	cfg := newAgentTestConfig(t, map[string]string{
		"reviewer": "---\ndescription: Reviews code\n---\nYou review code.",
	})

	first := cfg.ResolveAgents()
	second := cfg.ResolveAgents()
	require.Equal(t, first, second)
}

func TestResolveAgents_GlobalDisabledToolsOverridesExplicitWhitelist(t *testing.T) {
	cfg := &Config{
		Options: &Options{DisabledTools: []string{"Bash"}},
		AgentConfigs: map[string]Agent{
			"reviewer": {Description: "x", AllowedTools: &AllowedToolSet{Kind: ToolSetScope, Tools: []string{"Bash", "View"}}},
		},
	}

	agents := cfg.ResolveAgents()
	reviewer, ok := agents["reviewer"]
	require.True(t, ok)
	require.NotContains(t, reviewer.AllowedTools.Tools, "Bash", "global DisabledTools must win over an agent's explicit whitelist")
	require.Contains(t, reviewer.AllowedTools.Tools, "View")
}

func TestResolveAgents_CustomAgentDefaultsToBaseTools(t *testing.T) {
	cfg := &Config{
		Options: &Options{},
		AgentConfigs: map[string]Agent{
			"reviewer": {Description: "Reviews code", Mode: AgentModeSubagent, Prompt: "Review the diff."},
		},
	}

	agents := cfg.ResolveAgents()
	reviewer, ok := agents["reviewer"]
	require.True(t, ok)
	require.NotEmpty(t, reviewer.AllowedTools.Tools)
	require.ElementsMatch(t, filterSlice(allToolNames(), []string{"WebFetch", "WebSearch"}, false), reviewer.AllowedTools.Tools)
}

// TestResolveAgents_CustomPrimaryIsNotDowngraded pins that primary mode
// is no longer reserved for the coder. A user agent declaring
// "mode: primary" is one a session can be switched to, so silently
// rewriting it to a subagent made the declaration a lie: the agent
// showed up as a delegation target instead of a session driver.
func TestResolveAgents_CustomPrimaryIsNotDowngraded(t *testing.T) {
	cfg := &Config{
		Options: &Options{},
		AgentConfigs: map[string]Agent{
			"reviewer": {Description: "x", Mode: AgentModePrimary},
		},
	}

	agents := cfg.ResolveAgents()
	reviewer, ok := agents["reviewer"]
	require.True(t, ok)
	require.Equal(t, AgentModePrimary, reviewer.Mode,
		"a custom agent that declares primary mode must keep it")
	require.Equal(t, AgentModePrimary, agents[AgentCoder].Mode,
		"a second primary must not cost the coder its own primary mode")
}

// TestResolveAgents_DefaultModeIsSubagent pins the other half: primary
// has to be asked for. An agent that says nothing about mode is a
// delegation target, not a session driver.
func TestResolveAgents_DefaultModeIsSubagent(t *testing.T) {
	cfg := &Config{
		Options: &Options{},
		AgentConfigs: map[string]Agent{
			"reviewer": {Description: "x"},
		},
	}

	agents := cfg.ResolveAgents()
	require.Equal(t, AgentModeSubagent, agents["reviewer"].Mode)
}

// A branch is dispatched like a subagent but is not one: it forks the
// caller's transcript and then talks to the user. Angela ships no branch of
// its own, so this mode only ever arrives from user configuration — losing it
// in resolution would accept "mode: branch" and then quietly demote it to an
// ordinary delegation that nobody can reply to.
func TestResolveAgents_CustomBranchIsNotDowngraded(t *testing.T) {
	cfg := &Config{
		Options: &Options{},
		AgentConfigs: map[string]Agent{
			"pairing": {Description: "x", Mode: AgentModeBranch},
		},
	}

	agents := cfg.ResolveAgents()
	require.Equal(t, AgentModeBranch, agents["pairing"].Mode)
	require.NotEqual(t, AgentModeSubagent, agents["pairing"].Mode)
}

// Branch mode suspends the caller until a human resolves it, so it is never
// the right default for an agent the user did not ask for. plan and
// deep-research are the deliberate exceptions; this keeps any other builtin
// from drifting into it.
func TestResolveAgents_BuiltinBranchesAreExactlyPlanAndDeepResearch(t *testing.T) {
	cfg := &Config{Options: &Options{}}

	var branches []string
	for id, agent := range cfg.ResolveAgents() {
		if agent.Mode == AgentModeBranch {
			branches = append(branches, id)
		}
	}
	slices.Sort(branches)
	require.Equal(t, []string{AgentDeepResearch, AgentPlan}, branches)
}

// deep-research is the one investigating agent that may run commands: a root
// cause is a claim about what is already true, and settling it needs a
// reproduction or a git history, not more reading. Writing is still off the
// table, so the finding stays its only product.
func TestResolveAgents_DeepResearchRunsButDoesNotWrite(t *testing.T) {
	cfg := &Config{Options: &Options{}}
	allowed := cfg.ResolveAgents()[AgentDeepResearch].AllowedTools.Tools

	require.Contains(t, allowed, "Bash")
	for _, tool := range []string{"Edit", "MultiEdit", "Write", "Download", "LSPRename", "LSPReplaceSymbol"} {
		require.NotContains(t, allowed, tool)
	}
}

func TestResolveAgents_ExploreHasNoBash(t *testing.T) {
	cfg := &Config{Options: &Options{}}
	agents := cfg.ResolveAgents()
	require.NotContains(t, agents[AgentExplore].AllowedTools.Tools, "Bash")
}

// TestResolveAgents_AlwaysMaterialized locks the structural postcondition
// that replaced the old "filterSlice never returns nil" convention: no
// matter what any layer sets, every agent ResolveAgents returns has a
// non-nil AllowedTools already expanded to a concrete whitelist.
func TestResolveAgents_AlwaysMaterialized(t *testing.T) {
	tests := []struct {
		name string
		cfg  func(t *testing.T) *Config
	}{
		{"no DisabledTools", func(t *testing.T) *Config {
			return &Config{Options: &Options{}}
		}},
		{"empty DisabledTools", func(t *testing.T) *Config {
			return &Config{Options: &Options{DisabledTools: []string{}}}
		}},
		{"every read-only tool disabled", func(t *testing.T) *Config {
			return &Config{Options: &Options{DisabledTools: []string{
				"Glob", "Grep", "LS", "LSPCallHierarchy", "LSPDefinition", "LSPSymbols", "Sourcegraph", "View",
			}}}
		}},
		{"custom agent with explicit empty whitelist", func(t *testing.T) *Config {
			return &Config{
				Options: &Options{},
				AgentConfigs: map[string]Agent{
					"silent": {Description: "x", AllowedTools: &AllowedToolSet{Kind: ToolSetScope, Tools: []string{}}},
				},
			}
		}},
		{"markdown agent without allowed_tools", func(t *testing.T) *Config {
			return newAgentTestConfig(t, map[string]string{
				"reviewer": "---\ndescription: x\n---\nbody",
			})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agents := tt.cfg(t).ResolveAgents()
			require.NotEmpty(t, agents)
			for id, a := range agents {
				require.NotNilf(t, a.AllowedTools, "agent %q must have a materialized AllowedTools", id)
				require.Equalf(t, ToolSetScope, a.AllowedTools.Kind, "agent %q must be materialized to ToolSetScope", id)
			}
		})
	}

	t.Run("explicit empty whitelist stays empty, not promoted to all", func(t *testing.T) {
		agents := (&Config{
			Options: &Options{},
			AgentConfigs: map[string]Agent{
				"silent": {Description: "x", AllowedTools: &AllowedToolSet{Kind: ToolSetScope, Tools: []string{}}},
			},
		}).ResolveAgents()
		require.Empty(t, agents["silent"].AllowedTools.Tools)
	})
}

func TestResolveAgents_DisabledTriState(t *testing.T) {
	t.Run("explicit JSON false re-enables a markdown-disabled agent", func(t *testing.T) {
		cfg := newAgentTestConfig(t, map[string]string{
			"reviewer": "---\ndescription: x\ndisabled: true\n---\nbody",
		})
		enabled := false
		cfg.AgentConfigs = map[string]Agent{"reviewer": {Disabled: &enabled}}

		agents := cfg.ResolveAgents()
		_, ok := agents["reviewer"]
		require.True(t, ok, "explicit disabled: false in a higher layer should re-enable the agent")
	})

	t.Run("markdown disabled true removes the agent", func(t *testing.T) {
		cfg := newAgentTestConfig(t, map[string]string{
			"reviewer": "---\ndescription: x\ndisabled: true\n---\nbody",
		})

		agents := cfg.ResolveAgents()
		_, ok := agents["reviewer"]
		require.False(t, ok)
	})

	t.Run("coder can never be disabled", func(t *testing.T) {
		disabled := true
		cfg := &Config{
			Options:      &Options{},
			AgentConfigs: map[string]Agent{AgentCoder: {Disabled: &disabled}},
		}

		agents := cfg.ResolveAgents()
		_, ok := agents[AgentCoder]
		require.True(t, ok)
	})
}

// TestResolveAgents_HiddenTriState mirrors the Disabled tri-state: a
// hidden agent stays resolvable (unlike a disabled one, which is
// dropped), so the coordinator can still build it by ID. What Hidden
// controls is dispatch and completion visibility, not existence.
func TestResolveAgents_HiddenTriState(t *testing.T) {
	t.Run("markdown hidden true is resolvable but marked hidden", func(t *testing.T) {
		cfg := newAgentTestConfig(t, map[string]string{
			"reviewer": "---\ndescription: x\nhidden: true\n---\nbody",
		})

		agents := cfg.ResolveAgents()
		got, ok := agents["reviewer"]
		require.True(t, ok, "hidden agents must stay resolvable")
		require.True(t, got.IsHidden())
	})

	t.Run("explicit JSON false un-hides a markdown-hidden agent", func(t *testing.T) {
		cfg := newAgentTestConfig(t, map[string]string{
			"reviewer": "---\ndescription: x\nhidden: true\n---\nbody",
		})
		visible := false
		cfg.AgentConfigs = map[string]Agent{"reviewer": {Hidden: &visible}}

		agents := cfg.ResolveAgents()
		got, ok := agents["reviewer"]
		require.True(t, ok)
		require.False(t, got.IsHidden(), "explicit hidden: false in a higher layer should un-hide")
	})

	t.Run("explicit JSON true hides a visible agent", func(t *testing.T) {
		cfg := newAgentTestConfig(t, map[string]string{
			"reviewer": "---\ndescription: x\n---\nbody",
		})
		hidden := true
		cfg.AgentConfigs = map[string]Agent{"reviewer": {Hidden: &hidden}}

		agents := cfg.ResolveAgents()
		got, ok := agents["reviewer"]
		require.True(t, ok)
		require.True(t, got.IsHidden())
	})

	t.Run("unset stays visible", func(t *testing.T) {
		cfg := newAgentTestConfig(t, map[string]string{
			"reviewer": "---\ndescription: x\n---\nbody",
		})

		agents := cfg.ResolveAgents()
		got, ok := agents["reviewer"]
		require.True(t, ok)
		require.False(t, got.IsHidden())
	})
}

func TestResolveAgents_MaxTokensOverride(t *testing.T) {
	t.Parallel()

	cfg := newAgentTestConfig(t, map[string]string{
		"reviewer": "---\ndescription: x\n---\nbody",
	})
	maxTokens := int64(40)
	cfg.AgentConfigs = map[string]Agent{"reviewer": {MaxTokens: &maxTokens}}

	agents := cfg.ResolveAgents()
	got, ok := agents["reviewer"]
	require.True(t, ok)
	require.NotNil(t, got.MaxTokens)
	require.Equal(t, int64(40), *got.MaxTokens)
}

// TestResolveAgents_DisableTombstoneRemovesMarkdownAgent covers the only way
// to retract a markdown-defined agent: a disable tombstone in the JSON layer
// must suppress it, since the lower layer cannot delete itself.
func TestResolveAgents_DisableTombstoneRemovesMarkdownAgent(t *testing.T) {
	t.Parallel()

	cfg := newAgentTestConfig(t, map[string]string{
		"reviewer": "---\ndescription: Reviews code\n---\nYou review code.",
	})
	disabled := true
	cfg.AgentConfigs = map[string]Agent{"reviewer": {Disabled: &disabled}}

	agents := cfg.ResolveAgents()
	require.NotContains(t, agents, "reviewer")
}

func TestResolveAgents_InheritedFollowsCoderScope(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Options: &Options{},
		AgentConfigs: map[string]Agent{
			AgentCoder: {AllowedTools: &AllowedToolSet{Kind: ToolSetScope, Tools: []string{"View", "Grep"}}},
			"reviewer": {Description: "Reviews code"},
		},
	}

	agents := cfg.ResolveAgents()

	require.ElementsMatch(t, []string{"View", "Grep"}, agents[AgentCoder].AllowedTools.Tools)
	require.ElementsMatch(t, []string{"View", "Grep"}, agents["reviewer"].AllowedTools.Tools,
		"an agent that never mentions allowed_tools inherits the coder's resolved set")
	require.ElementsMatch(t, []string{"View", "Grep"}, agents[AgentGeneral].AllowedTools.Tools,
		"the built-in general agent inherits too")
}

func TestResolveAgents_InheritedMinusOwnDisabledTools(t *testing.T) {
	t.Parallel()

	cfg := &Config{Options: &Options{}}
	agents := cfg.ResolveAgents()

	general := agents[AgentGeneral]
	require.NotContains(t, general.AllowedTools.Tools, "Todos",
		"the agent's own disabled_tools still applies on top of the inherited set")
	require.Contains(t, general.AllowedTools.Tools, "Bash",
		"with an unrestricted coder, inheriting is equivalent to the previous all-tools default")
}

func TestResolveAgents_ExplicitScopeIsNotWidenedByInheritance(t *testing.T) {
	t.Parallel()

	cfg := &Config{Options: &Options{}}
	agents := cfg.ResolveAgents()

	// task/explore declare their own scope, so a permissive coder must
	// not widen them.
	require.NotContains(t, agents[AgentExplore].AllowedTools.Tools, "Bash")
	require.NotContains(t, agents[AgentExplore].AllowedTools.Tools, "Write")
}

func TestResolveAgents_CoderCannotInherit(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Options: &Options{},
		AgentConfigs: map[string]Agent{
			AgentCoder: {
				AllowedTools: &AllowedToolSet{Kind: ToolSetInherited},
				AllowedMCP:   &AllowedMCPSet{Kind: ToolSetInherited},
			},
		},
	}

	agents := cfg.ResolveAgents()
	coder := agents[AgentCoder]
	require.Equal(t, ToolSetScope, coder.AllowedTools.Kind)
	require.ElementsMatch(t, filterSlice(allToolNames(), []string{"WebFetch", "WebSearch"}, false), coder.AllowedTools.Tools,
		"the inheritance root falls back to every tool rather than to nothing")
	require.Equal(t, ToolSetAll, coder.AllowedMCP.Kind)
}

func TestResolveAgents_InheritedMCPFollowsCoder(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Options: &Options{},
		AgentConfigs: map[string]Agent{
			AgentCoder: {AllowedMCP: &AllowedMCPSet{
				Kind:    ToolSetScope,
				Servers: map[string][]string{"github": {"create_issue"}},
			}},
			"reviewer": {Description: "Reviews code"},
		},
	}

	agents := cfg.ResolveAgents()

	reviewer := agents["reviewer"]
	require.True(t, reviewer.AllowedMCP.Allows("github", "create_issue"))
	require.False(t, reviewer.AllowedMCP.Allows("github", "delete_repo"),
		"inheriting a scoped coder must not widen the tool list")
	require.False(t, reviewer.AllowedMCP.Allows("gitlab", "create_issue"))
}

// TestResolveAgents_ReadOnlyAgentsGetNoMCP pins the gap that made an
// explicit empty tool whitelist meaningless: the read-only built-ins
// must not pick up MCP tools through the separate MCP control plane.
func TestResolveAgents_ReadOnlyAgentsGetNoMCP(t *testing.T) {
	t.Parallel()

	agents := (&Config{Options: &Options{}}).ResolveAgents()

	for _, id := range []string{AgentExplore} {
		require.Equal(t, ToolSetScope, agents[id].AllowedMCP.Kind)
		require.False(t, agents[id].AllowedMCP.Allows("github", "create_issue"),
			"%s must not reach any MCP server", id)
	}
}

func TestResolveAgents_AlwaysMaterializesMCP(t *testing.T) {
	t.Parallel()

	cfg := newAgentTestConfig(t, map[string]string{
		"reviewer": "---\ndescription: x\n---\nbody",
	})

	for id, a := range cfg.ResolveAgents() {
		require.NotNilf(t, a.AllowedMCP, "agent %q must have a materialized AllowedMCP", id)
		require.NotEqualf(t, ToolSetInherited, a.AllowedMCP.Kind,
			"agent %q must not keep an unresolved inherited MCP set", id)
	}
}

func TestResolveAgents_RejectsInvalidJSONAgent(t *testing.T) {
	t.Parallel()

	nan := math.NaN()
	tests := []struct {
		name  string
		agent Agent
	}{
		{"unknown mode", Agent{Description: "x", Mode: AgentMode("primray")}},
		{"NaN temperature", Agent{Description: "x", Temperature: &nan}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &Config{
				Options:      &Options{},
				AgentConfigs: map[string]Agent{"reviewer": tt.agent},
			}
			require.NotContains(t, cfg.ResolveAgents(), "reviewer",
				"an invalid JSON agent must be skipped, not run with unexpected settings")
		})
	}
}

// TestResolveAgents_ArbitraryModelNameAccepted pins the open value
// domain for Agent.Model: model config names are user-defined, so
// validation must not gate on a fixed set. An unknown name is a
// resolution-time warning, not a config error.
func TestResolveAgents_ArbitraryModelNameAccepted(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Options:      &Options{},
		AgentConfigs: map[string]Agent{"reviewer": {Description: "x", Model: ModelConfigName("medium")}},
	}

	agents := cfg.ResolveAgents()
	got, ok := agents["reviewer"]
	require.True(t, ok, "an unknown model config name must not disqualify the agent")
	require.Equal(t, ModelConfigName("medium"), got.Model)
}

func TestResolveAgents_InvalidJSONAgentDoesNotDropBuiltins(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Options: &Options{},
		AgentConfigs: map[string]Agent{
			AgentExplore: {Mode: AgentMode("bogus")},
		},
	}

	agents := cfg.ResolveAgents()
	require.Contains(t, agents, AgentExplore,
		"a rejected override must leave the built-in definition intact")
	require.Equal(t, AgentModeSubagent, agents[AgentExplore].Mode)
}
