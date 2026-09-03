package config

import (
	"fmt"
	"log/slog"
	"math"
	"slices"
	"sort"
)

// SelectedModelOverride is a named parameter preset layered over a
// provider's model. Every field is optional: a variant overrides the
// keys it names and leaves the rest of the model's own defaults
// alone. The model identity (provider and model ID) is deliberately
// absent — a variant is a different way to call the same model, not
// a different model.
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
func (o SelectedModelOverride) apply(base ProviderModel) ProviderModel {
	if o.ReasoningEffort != nil {
		base.DefaultReasoningEffort = *o.ReasoningEffort
	}
	if o.Think != nil {
		base.Think = *o.Think
	}
	if o.MaxTokens != nil {
		base.DefaultMaxTokens = *o.MaxTokens
	}
	if o.Temperature != nil {
		base.Options.Temperature = o.Temperature
	}
	if o.TopP != nil {
		base.Options.TopP = o.TopP
	}
	if o.TopK != nil {
		base.Options.TopK = o.TopK
	}
	if o.FrequencyPenalty != nil {
		base.Options.FrequencyPenalty = o.FrequencyPenalty
	}
	if o.PresencePenalty != nil {
		base.Options.PresencePenalty = o.PresencePenalty
	}
	if len(o.ProviderOptions) > 0 {
		merged := make(map[string]any, len(base.Options.ProviderOptions)+len(o.ProviderOptions))
		for k, v := range base.Options.ProviderOptions {
			merged[k] = v
		}
		for k, v := range o.ProviderOptions {
			merged[k] = v
		}
		base.Options.ProviderOptions = merged
	}
	return base
}

// validate reports why the override cannot be used, or nil. Catching
// this at load time names the variant and the field; letting it
// through surfaces much later as a provider error on some turn, with
// nothing pointing back at the variant that caused it.
func (o SelectedModelOverride) validate() error {
	if o.MaxTokens != nil && *o.MaxTokens < 0 {
		return fmt.Errorf("max_tokens must not be negative, got %d", *o.MaxTokens)
	}
	if err := checkUnitRange("temperature", o.Temperature); err != nil {
		return err
	}
	if err := checkUnitRange("top_p", o.TopP); err != nil {
		return err
	}
	if err := checkFinite("frequency_penalty", o.FrequencyPenalty); err != nil {
		return err
	}
	return checkFinite("presence_penalty", o.PresencePenalty)
}

// checkFinite rejects NaN and the infinities, which have no JSON
// representation and leave as a malformed request body.
func checkFinite(name string, v *float64) error {
	if v == nil {
		return nil
	}
	if math.IsNaN(*v) || math.IsInf(*v, 0) {
		return fmt.Errorf("%s must be a finite number", name)
	}
	return nil
}

// checkUnitRange additionally holds the value to [0,1], the range the
// config schema documents for these fields.
func checkUnitRange(name string, v *float64) error {
	if err := checkFinite(name, v); err != nil {
		return err
	}
	if v != nil && (*v < 0 || *v > 1) {
		return fmt.Errorf("%s must be between 0 and 1, got %v", name, *v)
	}
	return nil
}

// dropInvalidVariants removes variants whose parameters cannot be used,
// so an unusable name never reaches VariantNames and cannot be
// selected.
func dropInvalidVariants(cfg *Config) {
	for providerID, provider := range cfg.Providers.Seq2() {
		changed := false
		for i, model := range provider.Models {
			for variantName, override := range model.Variants {
				if err := override.validate(); err != nil {
					slog.Warn("Ignoring variant with invalid parameters",
						"provider", providerID, "model", model.ID, "variant", variantName, "error", err)
					delete(model.Variants, variantName)
					changed = true
				}
			}
			provider.Models[i] = model
		}
		if changed {
			cfg.Providers.Set(providerID, provider)
		}
	}
}

// reasoningVariant is the override a model's own reasoning level seeds.
// Providers already publish which levels a model accepts, so those
// become variants without the user restating them.
func reasoningVariant(level string) SelectedModelOverride {
	return SelectedModelOverride{ReasoningEffort: &level}
}

// VariantNames lists the variants callable on this model: its own
// reasoning levels first, in the order the provider publishes them,
// then any further user-defined variants, sorted.
//
// A user-defined variant that reuses a reasoning level's name keeps
// that level's position and replaces its behaviour.
func (m ProviderModel) VariantNames() []string {
	names := slices.Clone(m.ReasoningLevels)
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
func (m ProviderModel) WithVariant(name string) (ProviderModel, bool) {
	if name == "" {
		return m, false
	}
	if override, ok := m.Variants[name]; ok {
		return override.apply(m), true
	}
	if slices.Contains(m.ReasoningLevels, name) {
		return reasoningVariant(name).apply(m), true
	}
	return m, false
}
