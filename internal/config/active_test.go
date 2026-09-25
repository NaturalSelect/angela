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
	a.Agent.DisabledTools[0] = "read"

	require.Equal(t, "bash", b.Agent.DisabledTools[0])
	require.Equal(t, "bash", cfg.Agents["coder"].DisabledTools[0])
}

func TestActiveAgentStateRoundTripKeepsModelAndRefreshesDefinition(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()

	active, ok := cfg.InstantiateAgent("coder")
	require.True(t, ok)

	// The session picks a model the config never named for it.
	picked := SelectedModel{Provider: "groq", Model: "llama"}
	active.Model = picked
	active.ModelPick = &picked
	state := active.State()

	require.Equal(t, "coder", state.Agent)
	require.Equal(t, picked, state.Model)

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

// TestRestoreSlotAlwaysFollowsTheCurrentConfigLayout pins the A1 root-
// cause fix: Slot is never carried by the persisted state (the type
// does not even have such a field any more), so restoring the exact
// same state under two different slot layouts must follow the layout
// as it stands at restore time, not whatever it was when the state was
// written.
func TestRestoreSlotAlwaysFollowsTheCurrentConfigLayout(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()

	picked := SelectedModel{Provider: "groq", Model: "llama"}
	state := ActiveAgentState{Agent: "coder", Model: picked}

	restored, ok := cfg.Restore(state)
	require.True(t, ok)
	require.Equal(t, SlotMain, restored.Slot, "coder's own config currently names SlotMain")

	// The config is reorganized: "coder" now runs on a different slot.
	agent := cfg.Agents["coder"]
	agent.Slot = SlotChore
	cfg.Agents["coder"] = agent

	restored, ok = cfg.Restore(state)
	require.True(t, ok)
	require.Equal(t, SlotChore, restored.Slot,
		"restoring the exact same persisted state must follow the slot layout as it stands now")
}

// TestStateOmitsModelWhenNothingWasPicked pins the other half of A1: a
// session that never picked a model must not freeze whatever the
// config happened to resolve at instantiation time into its persisted
// state, or a later config change would stop reaching it.
func TestStateOmitsModelWhenNothingWasPicked(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()

	active, ok := cfg.InstantiateAgent("coder")
	require.True(t, ok)
	require.Nil(t, active.ModelPick, "a freshly instantiated agent has picked nothing")

	state := active.State()
	require.Zero(t, state.Model, "the model must not be persisted until the user actually picks one")
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

// TestInstantiateAgentFallsBackToSlotVariant pins the new fallback: a
// slot's own Variant takes effect for an agent that names none of its
// own, the same way its model and Think default already do.
func TestInstantiateAgentFallsBackToSlotVariant(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()
	slot := cfg.Slots[SlotMain]
	slot.Variant = "careful"
	cfg.Slots[SlotMain] = slot

	active, ok := cfg.InstantiateAgent("coder")
	require.True(t, ok)
	require.Empty(t, active.Agent.Variant, "the agent config itself still names nothing")
	require.Equal(t, "careful", active.EffectiveVariant())
}

// TestInstantiateAgentVariantOutranksSlotVariant pins the priority
// order: when both the agent and its slot name a variant, the
// agent's own wins.
func TestInstantiateAgentVariantOutranksSlotVariant(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()
	slot := cfg.Slots[SlotMain]
	slot.Variant = "careful"
	cfg.Slots[SlotMain] = slot
	agent := cfg.Agents["coder"]
	agent.Variant = "custom"
	cfg.Agents["coder"] = agent

	active, ok := cfg.InstantiateAgent("coder")
	require.True(t, ok)
	require.Equal(t, "custom", active.EffectiveVariant())
}

// TestRestoreExplicitBaselinePickOutranksSlotVariant pins that backing
// out of a preset is a hard opt-out: it must not fall through to the
// slot's own default, or a user could never actually reach baseline
// on a slot that names one.
func TestRestoreExplicitBaselinePickOutranksSlotVariant(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()
	slot := cfg.Slots[SlotMain]
	slot.Variant = "careful"
	cfg.Slots[SlotMain] = slot

	pick := ""
	active, ok := cfg.Restore(ActiveAgentState{Agent: "coder", Variant: &pick})
	require.True(t, ok)
	require.Empty(t, active.EffectiveVariant(),
		"an explicit baseline pick must not fall back to the slot's variant")
}

// TestAgentModelOverrideOutranksConfig pins the "switch agent model"
// override contract: it replaces Model outright and outranks the
// agent's own configured Variant, the same way a session's
// VariantPick would. Slot stays whatever the agent's own config
// says, so InstantiateFor's same-slot inheritance keeps working
// against the overridden model.
func TestAgentModelOverrideOutranksConfig(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()
	agent := cfg.Agents["coder"]
	agent.Variant = "custom"
	cfg.Agents["coder"] = agent
	cfg.AgentModelOverrides = map[string]SelectedModel{
		"coder": {Provider: "groq", Model: "llama", Variant: "careful"},
	}

	active, ok := cfg.InstantiateAgent("coder")
	require.True(t, ok)
	require.Equal(t, "groq", active.Model.Provider)
	require.Equal(t, "llama", active.Model.Model)
	require.Equal(t, "careful", active.EffectiveVariant(),
		"the override's variant must outrank the agent's own configured variant")
	require.Equal(t, SlotMain, active.Slot,
		"the slot label stays the agent's own, not the override's origin")
}

// TestInstantiateForIgnoresHostWhenTargetHasItsOwnOverride pins that
// an explicit override on an internal agent wins over inheriting the
// host's model, even when both sit on the same slot: the user asked
// to override that agent specifically.
func TestInstantiateForIgnoresHostWhenTargetHasItsOwnOverride(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()
	cfg.Agents["compact"] = Agent{ID: "compact", Slot: SlotMain}
	cfg.AgentModelOverrides = map[string]SelectedModel{
		"compact": {Provider: "groq", Model: "llama"},
	}

	host, ok := cfg.InstantiateAgent("coder")
	require.True(t, ok)
	host.Model = SelectedModel{Provider: "anthropic", Model: "claude-opus"}

	compact, ok := cfg.InstantiateFor("compact", host)
	require.True(t, ok)
	require.Equal(t, "llama", compact.Model.Model,
		"compact's own override must win over inheriting the host's model")
}

// TestRestoreSessionModelOutranksProcessOverride pins that a
// session's own persisted model pick keeps winning over a
// process-level override set on that agent afterward: switching an
// agent's model process-wide must not silently change a session that
// already made its own choice.
func TestRestoreSessionModelOutranksProcessOverride(t *testing.T) {
	t.Parallel()

	cfg := activeTestConfig()
	cfg.AgentModelOverrides = map[string]SelectedModel{
		"coder": {Provider: "groq", Model: "llama"},
	}

	state := ActiveAgentState{
		Agent: "coder",
		Model: SelectedModel{Provider: "openai", Model: "gpt-mini"},
	}
	restored, ok := cfg.Restore(state)
	require.True(t, ok)
	require.Equal(t, "openai", restored.Model.Provider,
		"a session's own persisted model pick must outrank a process-level override")
	require.Equal(t, "gpt-mini", restored.Model.Model)
}
