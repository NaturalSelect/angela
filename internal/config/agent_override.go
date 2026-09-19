package config

import (
	"fmt"
	"slices"
)

// ErrAgentOverrideUnknownAgent is returned when a "switch agent
// model" override names an agent ID that does not resolve.
var ErrAgentOverrideUnknownAgent = fmt.Errorf("agent not found")

// ErrAgentOverrideUnknownModel is returned when a "switch agent
// model" override names a model that is not available: its provider
// is disabled or missing, or the model ID is not in that provider's
// catalog.
var ErrAgentOverrideUnknownModel = fmt.Errorf("model not available")

// ErrAgentOverrideUnknownVariant is returned when a "switch agent
// model" override names a variant the target model does not define.
var ErrAgentOverrideUnknownVariant = fmt.Errorf("variant not found on model")

// ValidateAgentModelOverride reports why agentID cannot be pinned to
// model, or nil when the pin resolves cleanly. It is checked both
// when the override is first set and again on every config reload,
// since a reload can remove the agent, disable the provider, or drop
// the variant out from under a pin set earlier.
func (c *Config) ValidateAgentModelOverride(agentID string, model SelectedModel) error {
	if _, ok := c.Agents[agentID]; !ok {
		return fmt.Errorf("%w: %s", ErrAgentOverrideUnknownAgent, agentID)
	}
	if !c.IsModelAvailable(model.Provider, model.Model) {
		return fmt.Errorf("%w: %s/%s", ErrAgentOverrideUnknownModel, model.Provider, model.Model)
	}
	if model.Variant != "" {
		catalog := c.GetModel(model.Provider, model.Model)
		if catalog == nil || !slices.Contains(catalog.VariantNames(), model.Variant) {
			return fmt.Errorf("%w: %s", ErrAgentOverrideUnknownVariant, model.Variant)
		}
	}
	return nil
}
