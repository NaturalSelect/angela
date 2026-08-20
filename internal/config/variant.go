package config

import (
	"slices"
	"sort"

	"charm.land/catwalk/pkg/catwalk"
)

// SelectedModelOverride is a named parameter preset layered over a
// model config. Every field is optional: a variant overrides the keys
// it names and leaves the rest of the baseline alone. The model
// identity (provider and model ID) is deliberately absent — a variant
// is a different way to call the same model, not a different model.
type SelectedModelOverride struct {
	ReasoningEffort *string `json:"reasoning_effort,omitempty" jsonschema:"description=Reasoning effort override,enum=low,enum=medium,enum=high"`
	Think           *bool   `json:"think,omitempty" jsonschema:"description=Thinking mode override for Anthropic models that can reason"`

	MaxTokens        *int64   `json:"max_tokens,omitempty" jsonschema:"description=Maximum number of tokens for model responses,maximum=200000"`
	Temperature      *float64 `json:"temperature,omitempty" jsonschema:"description=Sampling temperature,minimum=0,maximum=1"`
	TopP             *float64 `json:"top_p,omitempty" jsonschema:"description=Top-p (nucleus) sampling parameter,minimum=0,maximum=1"`
	TopK             *int64   `json:"top_k,omitempty" jsonschema:"description=Top-k sampling parameter"`
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty" jsonschema:"description=Frequency penalty to reduce repetition"`
	PresencePenalty  *float64 `json:"presence_penalty,omitempty" jsonschema:"description=Presence penalty to increase topic diversity"`

	// ProviderOptions merges key by key: the keys a variant names win,
	// the baseline's other keys survive.
	ProviderOptions map[string]any `json:"provider_options,omitempty" jsonschema:"description=Provider-specific options merged over the model's own"`
}

// apply overlays the override onto base and returns the result. base is
// not modified.
func (o SelectedModelOverride) apply(base SelectedModel) SelectedModel {
	if o.ReasoningEffort != nil {
		base.ReasoningEffort = *o.ReasoningEffort
	}
	if o.Think != nil {
		base.Think = *o.Think
	}
	if o.MaxTokens != nil {
		base.MaxTokens = *o.MaxTokens
	}
	if o.Temperature != nil {
		base.Temperature = o.Temperature
	}
	if o.TopP != nil {
		base.TopP = o.TopP
	}
	if o.TopK != nil {
		base.TopK = o.TopK
	}
	if o.FrequencyPenalty != nil {
		base.FrequencyPenalty = o.FrequencyPenalty
	}
	if o.PresencePenalty != nil {
		base.PresencePenalty = o.PresencePenalty
	}
	if len(o.ProviderOptions) > 0 {
		merged := make(map[string]any, len(base.ProviderOptions)+len(o.ProviderOptions))
		for k, v := range base.ProviderOptions {
			merged[k] = v
		}
		for k, v := range o.ProviderOptions {
			merged[k] = v
		}
		base.ProviderOptions = merged
	}
	return base
}

// reasoningVariant is the override a model's own reasoning level seeds.
// Providers already publish which levels a model accepts, so those
// become variants without the user restating them.
func reasoningVariant(level string) SelectedModelOverride {
	return SelectedModelOverride{ReasoningEffort: &level}
}

// VariantNames lists the variants callable on this model config: the
// model's own reasoning levels first, in the order the provider
// publishes them, then any further user-defined variants, sorted. cw
// may be nil, in which case only user-defined variants exist.
//
// A user-defined variant that reuses a reasoning level's name keeps
// that level's position and replaces its behaviour.
func (m SelectedModel) VariantNames(cw *catwalk.Model) []string {
	var names []string
	if cw != nil {
		names = append(names, cw.ReasoningLevels...)
	}
	custom := make([]string, 0, len(m.Variants))
	for name := range m.Variants {
		if slices.Contains(names, name) {
			continue
		}
		custom = append(custom, name)
	}
	sort.Strings(custom)
	return append(names, custom...)
}

// WithVariant overlays the named variant and reports whether one
// applied. An empty name means the baseline, and an unknown name
// degrades to the baseline rather than failing: a variant that
// disappeared from a provider's catalog must not brick a turn. Callers
// that switch variants on a user's behalf check the name against
// VariantNames first, where an unknown name is an error worth showing.
func (m SelectedModel) WithVariant(name string, cw *catwalk.Model) (SelectedModel, bool) {
	if name == "" {
		return m, false
	}
	if override, ok := m.Variants[name]; ok {
		return override.apply(m), true
	}
	if cw != nil && slices.Contains(cw.ReasoningLevels, name) {
		return reasoningVariant(name).apply(m), true
	}
	return m, false
}
