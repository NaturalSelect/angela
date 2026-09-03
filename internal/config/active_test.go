package config

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/stretchr/testify/require"
)

func activeTestConfig() *Config {
	temp := 0.5
	return &Config{
		Options: &Options{},
		Slots: map[SlotName]SelectedModel{
			SlotMain:  {Provider: "anthropic", Model: "claude"},
			SlotChore: {Provider: "openai", Model: "gpt-mini"},
			"fast":    {Provider: "groq", Model: "llama"},
		},
		// The preset that used to live on SlotMain's SelectedModel now
		// lives on the provider's catalog entry.
		Providers: csync.NewMapFrom(map[string]ProviderConfig{
			"anthropic": {
				ID: "anthropic",
				Models: []ProviderModel{
					{
						Model: catwalk.Model{
							ID:      "claude",
							Options: catwalk.ModelOptions{ProviderOptions: map[string]any{"beta": true}},
						},
						Variants: map[string]SelectedModelOverride{
							"careful": {Temperature: &temp},
						},
					},
				},
			},
			"openai": {
				ID:     "openai",
				Models: []ProviderModel{{Model: catwalk.Model{ID: "gpt-mini"}}},
			},
			"groq": {
				ID:     "groq",
				Models: []ProviderModel{{Model: catwalk.Model{ID: "llama"}}},
			},
		}),
		Agents: map[string]Agent{
			"coder":   {ID: "coder", Slot: SlotMain, DisabledTools: []string{"bash"}},
			"scout":   {ID: "scout", Slot: "fast", ContextPaths: []string{"NOTES.md"}},
			"typo":    {ID: "typo", Slot: "mian"},
			"unnamed": {ID: "unnamed"},
		},
	}
}

func TestInstantiateAgentCopiesFromGlobalConfig(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()

	active, ok := cfg.InstantiateAgent("scout")
	require.True(t, ok)
	require.Equal(t, "scout", active.Agent.ID)
	require.Equal(t, SlotName("fast"), active.Slot)
	require.Equal(t, "groq", active.Model.Provider)
	require.Equal(t, "llama", active.Model.Model)
}

func TestInstantiateAgentRejectsUnknownAgent(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()

	_, ok := cfg.InstantiateAgent("ghost")
	require.False(t, ok)
}

func TestInstantiateAgentFallsBackToMainOnBadModelName(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()

	// A typo in the model name must not brick the agent.
	active, ok := cfg.InstantiateAgent("typo")
	require.True(t, ok)
	require.Equal(t, SlotMain, active.Slot)
	require.Equal(t, "claude", active.Model.Model)

	// An agent that names no model at all resolves to main too.
	active, ok = cfg.InstantiateAgent("unnamed")
	require.True(t, ok)
	require.Equal(t, SlotMain, active.Slot)
	require.Equal(t, "claude", active.Model.Model)
}

func TestInstantiatedAgentsDoNotShareMutableState(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()

	a, ok := cfg.InstantiateAgent("coder")
	require.True(t, ok)
	b, ok := cfg.InstantiateAgent("coder")
	require.True(t, ok)

	// Editing one session's instance must not reach the other, nor
	// the published config every other reader still holds.
	a.Agent.DisabledTools[0] = "view"

	require.Equal(t, "bash", b.Agent.DisabledTools[0])
	require.Equal(t, "bash", cfg.Agents["coder"].DisabledTools[0])
}

func TestActiveAgentStateRoundTripKeepsModelAndRefreshesDefinition(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()

	active, ok := cfg.InstantiateAgent("coder")
	require.True(t, ok)

	// The session picks a model the config never named for it.
	active.Slot = "fast"
	active.Model = SelectedModel{Provider: "groq", Model: "llama"}
	state := active.State()

	require.Equal(t, "coder", state.Agent)
	require.Equal(t, SlotName("fast"), state.Slot)

	// Meanwhile the config file changed the agent's prompt.
	agent := cfg.Agents["coder"]
	agent.Prompt = "You are new."
	cfg.Agents["coder"] = agent

	restored, ok := cfg.Restore(state)
	require.True(t, ok)

	// Definition follows the config file...
	require.Equal(t, "You are new.", restored.Agent.Prompt)
	// ...while the model selection stays the session's own.
	require.Equal(t, "groq", restored.Model.Provider)
	require.Equal(t, "llama", restored.Model.Model)
}

func TestRestoreFallsBackWhenStateCarriesNoModel(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()

	// A state written before the session ever chose a model must not
	// blank out the agent's configured model.
	restored, ok := cfg.Restore(ActiveAgentState{Agent: "scout"})
	require.True(t, ok)
	require.Equal(t, SlotName("fast"), restored.Slot)
	require.Equal(t, "llama", restored.Model.Model)
}

func TestRestoreReportsAgentThatNoLongerResolves(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()

	_, ok := cfg.Restore(ActiveAgentState{Agent: "deleted"})
	require.False(t, ok)
}

func TestActiveAgentStateIsZero(t *testing.T) {
	t.Parallel()

	require.True(t, ActiveAgentState{}.IsZero())
	require.False(t, ActiveAgentState{Agent: "coder"}.IsZero())
}

// TestInternalAgentInheritsTheHostsModelOnAMatchingRole is the point of
// the host override: a session switched to a big model must compact
// with that model, not with whatever the global SlotMain slot says.
func TestInternalAgentInheritsTheHostsModelOnAMatchingRole(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()
	cfg.Agents["compact"] = Agent{ID: "compact", Slot: SlotMain}

	host, ok := cfg.InstantiateAgent("coder")
	require.True(t, ok)
	host.Model = SelectedModel{Provider: "anthropic", Model: "claude-opus"}

	compact, ok := cfg.InstantiateFor("compact", host)
	require.True(t, ok)
	require.Equal(t, "claude-opus", compact.Model.Model,
		"compaction must follow the model the session actually picked")
	require.Equal(t, SlotMain, compact.Slot)
}

// TestInternalAgentOnAnotherRoleIgnoresTheHost pins the other half:
// the session overrode one role, and titling is not on it, so titling
// stays on the cheap model the config assigns it.
func TestInternalAgentOnAnotherRoleIgnoresTheHost(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()
	cfg.Agents["title"] = Agent{ID: "title", Slot: SlotChore}

	host, ok := cfg.InstantiateAgent("coder")
	require.True(t, ok)
	host.Model = SelectedModel{Provider: "anthropic", Model: "claude-opus"}

	title, ok := cfg.InstantiateFor("title", host)
	require.True(t, ok)
	require.Equal(t, "gpt-mini", title.Model.Model,
		"a role the session never chose for must stay on its configured model")
}

// TestInternalAgentWithoutAHostFallsBackToConfig covers the callers
// that belong to no session at all.
func TestInternalAgentWithoutAHostFallsBackToConfig(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()
	cfg.Agents["generate"] = Agent{ID: "generate", Slot: SlotMain}

	generate, ok := cfg.InstantiateFor("generate", ActiveAgent{})
	require.True(t, ok)
	require.Equal(t, "claude", generate.Model.Model)
}

// TestInternalAgentDoesNotShareMutableStateWithItsHost pins that an
// inherited model is copied by value, not aliased: retuning the
// host's model after the internal agent was instantiated must not
// reach back into the instance that already copied it.
func TestInternalAgentDoesNotShareMutableStateWithItsHost(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()
	cfg.Agents["compact"] = Agent{ID: "compact", Slot: SlotMain}

	host, ok := cfg.InstantiateAgent("coder")
	require.True(t, ok)

	compact, ok := cfg.InstantiateFor("compact", host)
	require.True(t, ok)
	require.Equal(t, host.Model, compact.Model)

	host.Model = SelectedModel{Provider: "changed", Model: "changed"}

	require.Equal(t, "anthropic", compact.Model.Provider,
		"the inherited model must be copied, not aliased to the host's")
	require.Equal(t, "claude", compact.Model.Model)
}

// TestUnknownInternalAgentIsReported keeps a missing agent a caller
// decision rather than a silent empty instance.
func TestUnknownInternalAgentIsReported(t *testing.T) {
	t.Parallel()

	_, ok := activeTestConfig().InstantiateFor("nope", ActiveAgent{})
	require.False(t, ok)
}
