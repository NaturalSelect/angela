package agent

import (
	"context"
	"fmt"
	"maps"

	"charm.land/fantasy/providers/openai"
	"charm.land/fantasy/providers/openaicompat"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/imagegen"
)

// defaultImageProvider is the provider ID imageSelection falls back to
// when the image agent's slot has no model configured.
const defaultImageProvider = "openai"

// imageToolsAvailable reports whether the built-in ImageGenerate and
// ImageEdit tools should be added to an agent's tool list. It is the
// single gate buildTools consults for both tools together, so every
// precondition they need to actually work is decided here instead of
// surfacing later as a runtime tool failure. The second return value
// is a short, human-readable reason suitable for a debug log; it is
// set regardless of the boolean outcome.
func (c *coordinator) imageToolsAvailable(agent config.Agent, depth int) (bool, string) {
	if depth != 0 {
		return false, "image tools are only available to the primary agent, not a dispatched sub-agent"
	}
	if !c.interactive {
		return false, "image tools require an interactive session"
	}
	if c.cfg.ImageToolsDisabled() {
		return false, "image tools are disabled by configuration"
	}
	if c.imageSupport == nil || !c.imageSupport() {
		return false, "client has not reported image rendering support"
	}
	if c.images == nil {
		return false, "no image storage service is configured"
	}
	if _, _, err := c.imageSelection(c.cfg.Config()); err != nil {
		return false, fmt.Sprintf("no usable image model provider is configured: %s", err)
	}
	return true, fmt.Sprintf("image tools available for agent %q", agent.ID)
}

// imageSelection resolves the model and provider the built-in image
// tools would use. It performs only static, cheap checks and never
// resolves a $VAR/$(cmd) shell expansion in a credential — that is
// deferred to newImageClient, which runs only once a tool is actually
// invoked. An error return means the image tools have nothing usable
// to call and must stay unregistered.
func (c *coordinator) imageSelection(cfg *config.Config) (config.SelectedModel, config.ProviderConfig, error) {
	agentCfg, ok := cfg.Agents[config.AgentImage]
	if !ok {
		return config.SelectedModel{}, config.ProviderConfig{}, fmt.Errorf("agent %q is not configured", config.AgentImage)
	}

	model, ok := cfg.ModelForSlot(agentCfg.Slot)
	if !ok {
		model = config.SelectedModel{Provider: defaultImageProvider, Model: imagegen.DefaultModel}
	}

	providerCfg, ok := cfg.Providers.Get(model.Provider)
	if !ok {
		return config.SelectedModel{}, config.ProviderConfig{}, fmt.Errorf("provider %q is not configured", model.Provider)
	}
	if providerCfg.Disable {
		return config.SelectedModel{}, config.ProviderConfig{}, fmt.Errorf("provider %q is disabled", model.Provider)
	}

	switch providerCfg.Type {
	case openai.Name, openaicompat.Name:
		// Supported: the OpenAI Images API itself, and
		// OpenAI-compatible gateways that proxy it.
	default:
		return config.SelectedModel{}, config.ProviderConfig{}, fmt.Errorf(
			"provider %q has type %q, which does not support image generation", model.Provider, providerCfg.Type)
	}

	if providerCfg.APIKey == "" {
		return config.SelectedModel{}, config.ProviderConfig{}, fmt.Errorf("provider %q has no api_key configured", model.Provider)
	}

	return model, providerCfg, nil
}

// newImageClient is the tools.ImageClientFactory passed to the
// built-in image tools. It re-resolves imageSelection on every call
// instead of caching it, which keeps it cheap, self-contained, and
// safe to invoke concurrently. Only here, immediately before a tool
// actually runs, are the provider's api_key and base_url resolved
// through the shell-expansion-aware resolver — the same mechanism
// buildProvider uses to build a real LLM client.
func (c *coordinator) newImageClient(ctx context.Context) (imagegen.Client, error) {
	model, providerCfg, err := c.imageSelection(c.cfg.Config())
	if err != nil {
		return nil, err
	}

	apiKey, _ := c.cfg.ProviderFieldResolver(providerCfg.ID, "api_key").ResolveValue(providerCfg.APIKey)
	baseURL, _ := c.cfg.ProviderFieldResolver(providerCfg.ID, "base_url").ResolveValue(providerCfg.BaseURL)

	return imagegen.New(imagegen.Endpoint{
		ProviderID: providerCfg.ID,
		Model:      model.Model,
		BaseURL:    baseURL,
		APIKey:     apiKey,
		Headers:    maps.Clone(providerCfg.ExtraHeaders),
		Timeout:    c.cfg.Config().Tools.Image.GetTimeout(),
	}), nil
}
