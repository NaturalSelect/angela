package agent

import (
	"testing"

	"charm.land/fantasy/providers/openai"
	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

// newImagePickCoordinator extends newImageReadyCoordinator with an openai
// provider whose catalog lists a second image model, which is what lets
// a Switch Agent Model pick for the image agent validate.
func newImagePickCoordinator(t *testing.T) *coordinator {
	t.Helper()

	coord := newImageReadyCoordinator(t)
	coord.cfg.Config().Providers.Set("openai", config.ProviderConfig{
		ID:     "openai",
		Type:   openai.Name,
		APIKey: "test-key",
		Models: []config.ProviderModel{{ID: "gpt-image-2"}},
	})
	return coord
}

func imagePick(model string) map[string]config.AgentModelPick {
	return map[string]config.AgentModelPick{
		config.AgentImage: {Model: config.SelectedModel{Provider: "openai", Model: model}},
	}
}

// TestImageSelectionFollowsAgentModelPick pins that a model chosen with
// Switch Agent Model for the image agent outranks the slot and the
// built-in default, like a pick does for every other agent.
func TestImageSelectionFollowsAgentModelPick(t *testing.T) {
	coord := newImagePickCoordinator(t)

	model, _, err := coord.imageSelection(coord.cfg.Config(), imagePick("gpt-image-2"))
	require.NoError(t, err)
	require.Equal(t, config.SelectedModel{Provider: "openai", Model: "gpt-image-2"}, model)
}

// TestImageSelectionSkipsAPickThatNoLongerResolves pins that a pick whose
// model left the catalog falls back to the configured model instead of
// stripping the image tools from the session.
func TestImageSelectionSkipsAPickThatNoLongerResolves(t *testing.T) {
	coord := newImagePickCoordinator(t)

	model, _, err := coord.imageSelection(coord.cfg.Config(), imagePick("gone-model"))
	require.NoError(t, err)
	require.Equal(t, defaultImageProvider, model.Provider)
	require.NotEqual(t, "gone-model", model.Model)
}

// TestImageSelectionIgnoresPresetOnlyPick pins that a pick naming only a
// preset leaves the model to the configuration.
func TestImageSelectionIgnoresPresetOnlyPick(t *testing.T) {
	coord := newImagePickCoordinator(t)
	variant := "high"
	picks := map[string]config.AgentModelPick{config.AgentImage: {Variant: &variant}}

	model, _, err := coord.imageSelection(coord.cfg.Config(), picks)
	require.NoError(t, err)
	require.Equal(t, defaultImageProvider, model.Provider)
}

// TestImageToolsUseTheModelPickedForTheTree pins the whole path a user
// takes: the pick lives on the host instance buildTools receives, and
// the factory handed to the tools builds a client for the picked model.
func TestImageToolsUseTheModelPickedForTheTree(t *testing.T) {
	coord := newImagePickCoordinator(t)
	agentCfg := coord.cfg.Config().Agents[config.AgentCoder]

	host := config.ActiveAgent{Agent: agentCfg, AgentPicks: imagePick("gpt-image-2")}
	toolList, err := coord.buildTools(agentCfg, host, "", 0)
	require.NoError(t, err)

	var names []string
	for _, tool := range toolList {
		names = append(names, tool.Info().Name)
	}
	require.Contains(t, names, toolnames.ImageGenerate)
	require.Contains(t, names, toolnames.ImageEdit)

	var factory tools.ImageClientFactory = coord.newImageClient(host.AgentPicks)
	client, err := factory(t.Context())
	require.NoError(t, err)
	require.Equal(t, "gpt-image-2", client.Model())

	fallback, err := coord.newImageClient(nil)(t.Context())
	require.NoError(t, err)
	require.NotEqual(t, "gpt-image-2", fallback.Model(),
		"without a pick the client must keep using the configured model")
}
