package acp

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/acp/protocol"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/workspace"
)

func providerModel(id, name string) config.ProviderModel {
	return config.ProviderModel{Model: catwalk.Model{ID: id, Name: name}}
}

func testConfig(providers ...config.ProviderConfig) *config.Config {
	byID := make(map[string]config.ProviderConfig, len(providers))
	for _, p := range providers {
		byID[p.ID] = p
	}
	return &config.Config{Providers: csync.NewMapFrom(byID)}
}

func twoProviders() *config.Config {
	return testConfig(
		config.ProviderConfig{
			ID: "openai", Name: "OpenAI",
			Models: []config.ProviderModel{providerModel("gpt-5", "GPT-5"), providerModel("gpt-5-mini", "")},
		},
		config.ProviderConfig{
			ID: "anthropic", Name: "Anthropic",
			Models: []config.ProviderModel{providerModel("sonnet", "Sonnet")},
		},
		config.ProviderConfig{
			ID: "off", Name: "Disabled", Disable: true,
			Models: []config.ProviderModel{providerModel("hidden", "Hidden")},
		},
	)
}

func activeOn(provider, model string, catalog config.ProviderModel) workspace.ActiveAgent {
	return workspace.ActiveAgent{
		AgentID:    config.AgentCoder,
		AgentName:  "Coder",
		ModelCfg:   config.SelectedModel{Provider: provider, Model: model},
		CatwalkCfg: catalog,
	}
}

func optionIDs(opts []protocol.ConfigOption) []string {
	ids := make([]string, len(opts))
	for i, o := range opts {
		ids[i] = o.ID
	}
	return ids
}

func valueIDs(opt protocol.ConfigOption) []string {
	ids := make([]string, len(opt.Values))
	for i, v := range opt.Values {
		ids[i] = v.Value
	}
	return ids
}

func requireOption(t *testing.T, opts []protocol.ConfigOption, id string) protocol.ConfigOption {
	t.Helper()
	opt, ok := findOption(opts, id)
	require.True(t, ok, "option %q missing from %v", id, optionIDs(opts))
	return opt
}

func requireCurrentValuesListed(t *testing.T, opts []protocol.ConfigOption) {
	t.Helper()
	for _, o := range opts {
		require.Contains(t, valueIDs(o), o.CurrentValue, "current value of %q is not one of its values", o.ID)
	}
}

func TestBuildConfigOptions(t *testing.T) {
	t.Parallel()

	reasoning := providerModel("gpt-5", "GPT-5")
	reasoning.CanReason = true
	reasoning.ReasoningLevels = []string{"low", "high"}

	toggle := providerModel("gpt-5", "GPT-5")
	toggle.CanReason = true

	custom := providerModel("gpt-5", "GPT-5")
	custom.Variants = map[string]config.SelectedModelOverride{"fast": {}, "careful": {}}

	t.Run("plain model offers permissions and model only", func(t *testing.T) {
		t.Parallel()
		active := activeOn("openai", "gpt-5", providerModel("gpt-5", "GPT-5"))

		opts := buildConfigOptions(twoProviders(), active, permission.ModeManual)

		require.Equal(t, []string{optMode, optModel}, optionIDs(opts))
		requireCurrentValuesListed(t, opts)
		mode := requireOption(t, opts, optMode)
		require.Equal(t, "manual", mode.CurrentValue)
		require.Equal(t, protocol.ConfigCategoryMode, mode.Category)
		require.Equal(t, []string{"manual", "auto_accept_edits", "yolo"}, valueIDs(mode))
	})

	t.Run("permission mode is reflected", func(t *testing.T) {
		t.Parallel()
		opts := buildConfigOptions(twoProviders(), activeOn("openai", "gpt-5", providerModel("gpt-5", "")), permission.ModeYolo)
		require.Equal(t, "yolo", requireOption(t, opts, optMode).CurrentValue)
	})

	t.Run("models come from enabled providers in provider order", func(t *testing.T) {
		t.Parallel()
		active := activeOn("openai", "gpt-5", providerModel("gpt-5", "GPT-5"))

		model := requireOption(t, buildConfigOptions(twoProviders(), active, permission.ModeManual), optModel)

		require.Equal(t, protocol.ConfigCategoryModel, model.Category)
		require.Equal(t, "openai:gpt-5", model.CurrentValue)
		require.Equal(t, []string{"anthropic:sonnet", "openai:gpt-5", "openai:gpt-5-mini"}, valueIDs(model))
		require.Equal(t, protocol.ConfigValue{Value: "anthropic:sonnet", Name: "Sonnet", Description: "Anthropic"}, model.Values[0])
		require.Equal(t, "gpt-5-mini", model.Values[2].Name, "a model without a name shows its ID")
	})

	t.Run("current model missing from the list is added", func(t *testing.T) {
		t.Parallel()
		active := activeOn("local", "llama3:8b", providerModel("llama3:8b", "Llama 3"))

		opts := buildConfigOptions(twoProviders(), active, permission.ModeManual)

		model := requireOption(t, opts, optModel)
		require.Equal(t, "local:llama3:8b", model.CurrentValue)
		require.Contains(t, valueIDs(model), "local:llama3:8b")
		requireCurrentValuesListed(t, opts)
	})

	t.Run("no model selected hides the model option", func(t *testing.T) {
		t.Parallel()
		opts := buildConfigOptions(twoProviders(), workspace.ActiveAgent{}, permission.ModeManual)
		require.Equal(t, []string{optMode}, optionIDs(opts))
	})

	t.Run("nil config is tolerated", func(t *testing.T) {
		t.Parallel()
		opts := buildConfigOptions(nil, activeOn("openai", "gpt-5", providerModel("gpt-5", "")), permission.ModeManual)
		require.Equal(t, []string{optMode, optModel}, optionIDs(opts))
		requireCurrentValuesListed(t, opts)
	})

	t.Run("reasoning levels become a thought level option", func(t *testing.T) {
		t.Parallel()
		active := activeOn("openai", "gpt-5", reasoning)
		active.Variant = "high"

		opts := buildConfigOptions(twoProviders(), active, permission.ModeManual)

		variant := requireOption(t, opts, optVariant)
		require.Equal(t, protocol.ConfigCategoryThoughtLevel, variant.Category)
		require.Equal(t, "high", variant.CurrentValue)
		require.Equal(t, []string{baselineVariant, "low", "high"}, valueIDs(variant))
		require.NotContains(t, optionIDs(opts), optThinking, "levels replace the on/off switch")
		requireCurrentValuesListed(t, opts)
	})

	t.Run("no variant selected shows the baseline", func(t *testing.T) {
		t.Parallel()
		opts := buildConfigOptions(twoProviders(), activeOn("openai", "gpt-5", reasoning), permission.ModeManual)
		require.Equal(t, baselineVariant, requireOption(t, opts, optVariant).CurrentValue)
	})

	t.Run("custom variants are model config", func(t *testing.T) {
		t.Parallel()
		opts := buildConfigOptions(twoProviders(), activeOn("openai", "gpt-5", custom), permission.ModeManual)

		variant := requireOption(t, opts, optVariant)
		require.Equal(t, protocol.ConfigCategoryModelConfig, variant.Category)
		require.Equal(t, []string{baselineVariant, "careful", "fast"}, valueIDs(variant))
	})

	t.Run("a variant no longer offered is still listed as current", func(t *testing.T) {
		t.Parallel()
		active := activeOn("openai", "gpt-5", custom)
		active.Variant = "retired"

		opts := buildConfigOptions(twoProviders(), active, permission.ModeManual)

		require.Contains(t, valueIDs(requireOption(t, opts, optVariant)), "retired")
		requireCurrentValuesListed(t, opts)
	})

	t.Run("reasoning without levels gets an on off switch", func(t *testing.T) {
		t.Parallel()
		active := activeOn("openai", "gpt-5", toggle)
		active.Think = true

		opts := buildConfigOptions(twoProviders(), active, permission.ModeManual)

		thinking := requireOption(t, opts, optThinking)
		require.Equal(t, protocol.ConfigCategoryThoughtLevel, thinking.Category)
		require.Equal(t, thinkingOn, thinking.CurrentValue)
		require.Equal(t, []string{thinkingOff, thinkingOn}, valueIDs(thinking))
		require.NotContains(t, optionIDs(opts), optVariant)

		active.Think = false
		off := requireOption(t, buildConfigOptions(twoProviders(), active, permission.ModeManual), optThinking)
		require.Equal(t, thinkingOff, off.CurrentValue)
	})

	t.Run("agent option needs a choice", func(t *testing.T) {
		t.Parallel()
		cfg := twoProviders()
		cfg.Agents = map[string]config.Agent{
			config.AgentCoder: {ID: config.AgentCoder, Name: "Coder", Mode: config.AgentModePrimary},
		}
		active := activeOn("openai", "gpt-5", providerModel("gpt-5", ""))
		require.NotContains(t, optionIDs(buildConfigOptions(cfg, active, permission.ModeManual)), optAgent)

		hidden := true
		cfg.Agents["reviewer"] = config.Agent{ID: "reviewer", Name: "Reviewer", Mode: config.AgentModePrimary}
		cfg.Agents["internal"] = config.Agent{ID: "internal", Mode: config.AgentModePrimary, Hidden: &hidden}
		cfg.Agents["helper"] = config.Agent{ID: "helper", Mode: config.AgentModeSubagent}

		opts := buildConfigOptions(cfg, active, permission.ModeManual)

		agentOpt := requireOption(t, opts, optAgent)
		require.Equal(t, config.AgentCoder, agentOpt.CurrentValue)
		require.Equal(t, []string{config.AgentCoder, "reviewer"}, valueIDs(agentOpt))
		requireCurrentValuesListed(t, opts)
	})
}

func TestParseConfigChange(t *testing.T) {
	t.Parallel()

	toggle := providerModel("gpt-5", "GPT-5")
	toggle.CanReason = true
	toggle.ReasoningLevels = []string{"low", "high"}

	cfg := twoProviders()
	cfg.Providers.Set("local", config.ProviderConfig{ID: "local", Models: []config.ProviderModel{providerModel("llama3:8b", "Llama")}})
	cfg.Agents = map[string]config.Agent{
		config.AgentCoder: {ID: config.AgentCoder, Mode: config.AgentModePrimary},
		"reviewer":        {ID: "reviewer", Mode: config.AgentModePrimary},
	}
	current := buildConfigOptions(cfg, activeOn("openai", "gpt-5", toggle), permission.ModeManual)

	t.Run("mode", func(t *testing.T) {
		t.Parallel()
		change, err := parseConfigChange(current, optMode, "auto_accept_edits")
		require.NoError(t, err)
		require.Equal(t, new(permission.ModeAutoAcceptEdits), change.mode)
		require.True(t, change.edit.IsZero())
	})

	t.Run("model splits at the first colon", func(t *testing.T) {
		t.Parallel()
		change, err := parseConfigChange(current, optModel, "local:llama3:8b")
		require.NoError(t, err)
		require.Nil(t, change.mode)
		require.Equal(t, config.ActiveAgentEdit{Model: &config.SelectedModel{Provider: "local", Model: "llama3:8b"}}, change.edit)
	})

	t.Run("variant", func(t *testing.T) {
		t.Parallel()
		change, err := parseConfigChange(current, optVariant, "high")
		require.NoError(t, err)
		require.Equal(t, config.ActiveAgentEdit{Variant: new("high")}, change.edit)
	})

	t.Run("baseline variant clears the preset", func(t *testing.T) {
		t.Parallel()
		change, err := parseConfigChange(current, optVariant, baselineVariant)
		require.NoError(t, err)
		require.Equal(t, config.ActiveAgentEdit{Variant: new("")}, change.edit)
		require.False(t, change.edit.IsZero())
	})

	t.Run("agent", func(t *testing.T) {
		t.Parallel()
		change, err := parseConfigChange(current, optAgent, "reviewer")
		require.NoError(t, err)
		require.Equal(t, config.ActiveAgentEdit{Agent: "reviewer"}, change.edit)
	})

	t.Run("thinking", func(t *testing.T) {
		t.Parallel()
		reasons := providerModel("gpt-5", "GPT-5")
		reasons.CanReason = true
		opts := buildConfigOptions(cfg, activeOn("openai", "gpt-5", reasons), permission.ModeManual)

		on, err := parseConfigChange(opts, optThinking, thinkingOn)
		require.NoError(t, err)
		require.Equal(t, config.ActiveAgentEdit{Think: new(true)}, on.edit)

		off, err := parseConfigChange(opts, optThinking, thinkingOff)
		require.NoError(t, err)
		require.Equal(t, config.ActiveAgentEdit{Think: new(false)}, off.edit)
		require.False(t, off.edit.IsZero(), "turning thinking off is still a change")
	})

	t.Run("invalid input", func(t *testing.T) {
		t.Parallel()
		for _, tt := range []struct{ name, id, value string }{
			{"unknown option", "nope", "x"},
			{"value not offered", optMode, "root"},
			{"empty value", optMode, ""},
			{"unknown model", optModel, "openai:missing"},
			{"option that is not shown", optThinking, thinkingOn},
		} {
			_, err := parseConfigChange(current, tt.id, tt.value)
			require.ErrorAs(t, err, new(protocol.Error), tt.name)
			var perr protocol.Error
			require.ErrorAs(t, err, &perr, tt.name)
			require.Equal(t, protocol.CodeInvalidParams, perr.Code, tt.name)
		}
	})
}

func TestPaginateSessions(t *testing.T) {
	t.Parallel()

	sessions := []session.Session{
		{ID: "b", UpdatedAt: 10},
		{ID: "a", UpdatedAt: 10},
		{ID: "old", UpdatedAt: 1},
		{ID: "new", UpdatedAt: 99},
		{ID: "mid", UpdatedAt: 5},
	}
	ids := func(ss []session.Session) []string {
		out := make([]string, len(ss))
		for i, s := range ss {
			out[i] = s.ID
		}
		return out
	}

	t.Run("newest first and ties by id", func(t *testing.T) {
		t.Parallel()
		page, next, err := paginateSessions(sessions, "", 10)
		require.NoError(t, err)
		require.Equal(t, []string{"new", "a", "b", "mid", "old"}, ids(page))
		require.Empty(t, next)
	})

	t.Run("exactly one page has no cursor", func(t *testing.T) {
		t.Parallel()
		page, next, err := paginateSessions(sessions, "", 5)
		require.NoError(t, err)
		require.Len(t, page, 5)
		require.Empty(t, next)
	})

	t.Run("pages chain without gaps or repeats", func(t *testing.T) {
		t.Parallel()
		var got []string
		cursor := ""
		for range 10 {
			page, next, err := paginateSessions(sessions, cursor, 2)
			require.NoError(t, err)
			got = append(got, ids(page)...)
			if next == "" {
				break
			}
			cursor = next
		}
		require.Equal(t, []string{"new", "a", "b", "mid", "old"}, got)
	})

	t.Run("cursor survives the removal of its session", func(t *testing.T) {
		t.Parallel()
		_, next, err := paginateSessions(sessions, "", 2)
		require.NoError(t, err)

		remaining := []session.Session{{ID: "b", UpdatedAt: 10}, {ID: "old", UpdatedAt: 1}, {ID: "mid", UpdatedAt: 5}}
		page, _, err := paginateSessions(remaining, next, 10)
		require.NoError(t, err)
		require.Equal(t, []string{"b", "mid", "old"}, ids(page))
	})

	t.Run("does not reorder the caller's slice", func(t *testing.T) {
		t.Parallel()
		input := []session.Session{{ID: "b", UpdatedAt: 1}, {ID: "a", UpdatedAt: 2}}
		_, _, err := paginateSessions(input, "", 10)
		require.NoError(t, err)
		require.Equal(t, "b", input[0].ID)
	})

	t.Run("invalid cursors", func(t *testing.T) {
		t.Parallel()
		for _, cursor := range []string{"!!!", "bm90LWEtY3Vyc29y", encodeCursor(1, "x")[:3]} {
			_, _, err := paginateSessions(sessions, cursor, 2)
			var perr protocol.Error
			require.ErrorAs(t, err, &perr, cursor)
			require.Equal(t, protocol.CodeInvalidParams, perr.Code, cursor)
		}
	})
}

func TestCursorRoundTrip(t *testing.T) {
	t.Parallel()

	updatedAt, id, err := decodeCursor(encodeCursor(1700000000, "id:with:colons"))
	require.NoError(t, err)
	require.Equal(t, int64(1700000000), updatedAt)
	require.Equal(t, "id:with:colons", id)
}
