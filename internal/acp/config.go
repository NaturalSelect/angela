package acp

import (
	"cmp"
	"slices"
	"strings"

	"github.com/NaturalSelect/angela/internal/acp/protocol"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/workspace"
)

const (
	optMode     = "mode"
	optModel    = "model"
	optVariant  = "variant"
	optThinking = "thinking"
	optAgent    = "agent"

	// NOTE: Variant names are user-defined, so the baseline value carries an
	// underscore prefix to stay clear of them.
	baselineVariant = "_baseline"

	thinkingOn  = "on"
	thinkingOff = "off"
)

// buildConfigOptions lists the session settings the editor can change. It
// mirrors when the terminal UI offers each setting.
//
// NOTE: CurrentValue of every option is always one of its Values.
func buildConfigOptions(cfg *config.Config, active workspace.ActiveAgent, mode permission.PermissionMode) []protocol.ConfigOption {
	opts := []protocol.ConfigOption{modeOption(mode)}
	if opt, ok := modelOption(cfg, active); ok {
		opts = append(opts, opt)
	}
	if opt, ok := variantOption(active); ok {
		opts = append(opts, opt)
	}
	if opt, ok := thinkingOption(active); ok {
		opts = append(opts, opt)
	}
	if opt, ok := agentOption(cfg, active); ok {
		opts = append(opts, opt)
	}
	return opts
}

func modeOption(mode permission.PermissionMode) protocol.ConfigOption {
	return protocol.ConfigOption{
		ID:           optMode,
		Name:         "Permissions",
		Category:     protocol.ConfigCategoryMode,
		CurrentValue: mode.String(),
		Values: []protocol.ConfigValue{
			{Value: permission.ModeManual.String(), Name: "Ask", Description: "Ask before risky actions"},
			{Value: permission.ModeAutoAcceptEdits.String(), Name: "Accept edits", Description: "Allow file edits without asking"},
			{Value: permission.ModeYolo.String(), Name: "Yolo", Description: "Allow everything without asking"},
		},
	}
}

func modelOption(cfg *config.Config, active workspace.ActiveAgent) (protocol.ConfigOption, bool) {
	current := modelValue(active.ModelCfg.Provider, active.ModelCfg.Model)
	if current == "" {
		return protocol.ConfigOption{}, false
	}

	var values []protocol.ConfigValue
	if cfg != nil && cfg.Providers != nil {
		providers := cfg.EnabledProviders()
		slices.SortFunc(providers, func(a, b config.ProviderConfig) int { return cmp.Compare(a.ID, b.ID) })
		for _, p := range providers {
			for _, m := range p.Models {
				values = append(values, protocol.ConfigValue{
					Value:       modelValue(p.ID, m.ID),
					Name:        cmp.Or(m.Name, m.ID),
					Description: cmp.Or(p.Name, p.ID),
				})
			}
		}
	}
	values = withCurrent(values, current, cmp.Or(active.CatwalkCfg.Name, active.ModelCfg.Model))

	return protocol.ConfigOption{
		ID:           optModel,
		Name:         "Model",
		Category:     protocol.ConfigCategoryModel,
		CurrentValue: current,
		Values:       values,
	}, true
}

func variantOption(active workspace.ActiveAgent) (protocol.ConfigOption, bool) {
	names := active.CatwalkCfg.VariantNames()
	if len(names) == 0 {
		return protocol.ConfigOption{}, false
	}

	category := protocol.ConfigCategoryModelConfig
	if len(active.CatwalkCfg.ReasoningLevels) > 0 {
		category = protocol.ConfigCategoryThoughtLevel
	}
	values := []protocol.ConfigValue{{Value: baselineVariant, Name: "Default"}}
	for _, name := range names {
		values = append(values, protocol.ConfigValue{Value: name, Name: name})
	}
	current := cmp.Or(active.Variant, baselineVariant)

	return protocol.ConfigOption{
		ID:           optVariant,
		Name:         "Reasoning",
		Category:     category,
		CurrentValue: current,
		Values:       withCurrent(values, current, current),
	}, true
}

// NOTE: A model that reasons but names no levels has a single knob, so it
// gets an on/off switch instead of a variant list.
func thinkingOption(active workspace.ActiveAgent) (protocol.ConfigOption, bool) {
	if !active.CatwalkCfg.CanReason || len(active.CatwalkCfg.ReasoningLevels) > 0 {
		return protocol.ConfigOption{}, false
	}
	current := thinkingOff
	if active.Think {
		current = thinkingOn
	}
	return protocol.ConfigOption{
		ID:           optThinking,
		Name:         "Thinking",
		Category:     protocol.ConfigCategoryThoughtLevel,
		CurrentValue: current,
		Values: []protocol.ConfigValue{
			{Value: thinkingOff, Name: "Off"},
			{Value: thinkingOn, Name: "On"},
		},
	}, true
}

func agentOption(cfg *config.Config, active workspace.ActiveAgent) (protocol.ConfigOption, bool) {
	if cfg == nil {
		return protocol.ConfigOption{}, false
	}
	agents := config.SwitchableAgents(cfg)
	if len(agents) < 2 {
		return protocol.ConfigOption{}, false
	}
	values := make([]protocol.ConfigValue, 0, len(agents))
	for _, a := range agents {
		values = append(values, protocol.ConfigValue{
			Value:       a.ID,
			Name:        cmp.Or(a.Name, a.ID),
			Description: a.Description,
		})
	}
	return protocol.ConfigOption{
		ID:           optAgent,
		Name:         "Agent",
		CurrentValue: active.AgentID,
		Values:       withCurrent(values, active.AgentID, cmp.Or(active.AgentName, active.AgentID)),
	}, true
}

func withCurrent(values []protocol.ConfigValue, current, name string) []protocol.ConfigValue {
	if current == "" || slices.ContainsFunc(values, func(v protocol.ConfigValue) bool { return v.Value == current }) {
		return values
	}
	return append(values, protocol.ConfigValue{Value: current, Name: name})
}

// NOTE: Model IDs can contain a colon, so parsing cuts at the first one.
func modelValue(providerID, modelID string) string {
	if providerID == "" || modelID == "" {
		return ""
	}
	return providerID + ":" + modelID
}

// configChange is what one set_config_option request asks for. A zero edit
// and a nil mode mean that part is untouched.
type configChange struct {
	mode *permission.PermissionMode
	edit config.ActiveAgentEdit
}

func parseConfigChange(current []protocol.ConfigOption, id, value string) (configChange, error) {
	opt, ok := findOption(current, id)
	if !ok {
		return configChange{}, invalidParams("unknown config option %q", id)
	}
	if !slices.ContainsFunc(opt.Values, func(v protocol.ConfigValue) bool { return v.Value == value }) {
		return configChange{}, invalidParams("%q is not a valid value for %q", value, id)
	}

	switch id {
	case optMode:
		mode, ok := permission.ParsePermissionMode(value)
		if !ok {
			return configChange{}, invalidParams("%q is not a valid value for %q", value, id)
		}
		return configChange{mode: &mode}, nil
	case optModel:
		provider, model, ok := strings.Cut(value, ":")
		if !ok {
			return configChange{}, invalidParams("%q is not a valid value for %q", value, id)
		}
		return configChange{edit: config.ActiveAgentEdit{Model: &config.SelectedModel{Provider: provider, Model: model}}}, nil
	case optVariant:
		variant := value
		if value == baselineVariant {
			variant = ""
		}
		return configChange{edit: config.ActiveAgentEdit{Variant: &variant}}, nil
	case optThinking:
		think := value == thinkingOn
		return configChange{edit: config.ActiveAgentEdit{Think: &think}}, nil
	case optAgent:
		return configChange{edit: config.ActiveAgentEdit{Agent: value}}, nil
	default:
		return configChange{}, invalidParams("unknown config option %q", id)
	}
}

func findOption(opts []protocol.ConfigOption, id string) (protocol.ConfigOption, bool) {
	i := slices.IndexFunc(opts, func(o protocol.ConfigOption) bool { return o.ID == id })
	if i < 0 {
		return protocol.ConfigOption{}, false
	}
	return opts[i], true
}
