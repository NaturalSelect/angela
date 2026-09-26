package agent

import (
	"context"
	"testing"

	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/openai"
	"charm.land/fantasy/providers/openaicompat"
	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/imagegen"
	"github.com/NaturalSelect/angela/internal/images"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

// fakeImageStore is a minimal images.Service: the gating tests below
// only need a non-nil store, never a working one.
type fakeImageStore struct{}

var _ images.Service = fakeImageStore{}

func (fakeImageStore) Create(context.Context, images.CreateParams) (images.Image, error) {
	return images.Image{}, nil
}

func (fakeImageStore) Get(context.Context, string) (images.Image, error) {
	return images.Image{}, images.ErrNotFound
}

// newImageReadyCoordinator builds a coordinator on top of
// newGateTestCoordinator's hermetic config, with every precondition
// the built-in image tools need already satisfied: an "openai"
// provider that imageSelection's zero-config fallback finds by
// default, a fake image store, and a client that has proven
// Kitty-graphics support. Individual tests then flip exactly one
// condition to pin what turns the tools back off.
func newImageReadyCoordinator(t *testing.T) *coordinator {
	t.Helper()

	coord := newGateTestCoordinator(t, true)
	coord.cfg.Config().Providers.Set("openai", config.ProviderConfig{
		ID:     "openai",
		Type:   openai.Name,
		APIKey: "test-key",
	})
	coord.images = fakeImageStore{}
	coord.imageSupport = func() bool { return true }
	return coord
}

// TestBuildToolsImageToolsGating pins every precondition ImageGenerate
// and ImageEdit share: both are registered only when every one of
// depth, interactivity, client image support, the images store,
// config's disable switches, and a usable image-model provider holds.
// Flipping any single one must drop both tools, and appending them
// ahead of the allowed_tools/disabled_tools filter (rather than
// special-casing it) must still let that filter exclude them.
func TestBuildToolsImageToolsGating(t *testing.T) {
	type gateCase struct {
		name   string
		depth  int
		mutate func(t *testing.T, coord *coordinator)
		wantOK bool
	}

	cases := []gateCase{
		{
			name:   "all conditions satisfied",
			wantOK: true,
		},
		{
			name: "non-interactive session",
			mutate: func(t *testing.T, coord *coordinator) {
				coord.interactive = false
			},
		},
		{
			name:  "sub-agent depth",
			depth: 1,
		},
		{
			name: "client never reported image support",
			mutate: func(t *testing.T, coord *coordinator) {
				coord.imageSupport = nil
			},
		},
		{
			name: "client image support explicitly false",
			mutate: func(t *testing.T, coord *coordinator) {
				coord.imageSupport = func() bool { return false }
			},
		},
		{
			name: "no image storage service configured",
			mutate: func(t *testing.T, coord *coordinator) {
				coord.images = nil
			},
		},
		{
			name: "disabled via options.disable_image_tools",
			mutate: func(t *testing.T, coord *coordinator) {
				coord.cfg.Config().Options.DisableImageTools = true
			},
		},
		{
			name: "disabled via --disable-image-tools runtime override",
			mutate: func(t *testing.T, coord *coordinator) {
				coord.cfg.Overrides().DisableImageTools = true
			},
		},
		{
			name: "disabled_tools lists ImageGenerate and ImageEdit",
			mutate: func(t *testing.T, coord *coordinator) {
				coord.cfg.Config().Options.DisabledTools = []string{toolnames.ImageGenerate, toolnames.ImageEdit}
				coord.cfg.SetupAgents()
			},
		},
		{
			name: "image agent's provider missing from config",
			mutate: func(t *testing.T, coord *coordinator) {
				coord.cfg.Config().Providers.Del("openai")
			},
		},
		{
			name: "image agent's provider is a non-OpenAI-family type",
			mutate: func(t *testing.T, coord *coordinator) {
				coord.cfg.Config().Providers.Set("claude", config.ProviderConfig{
					ID:     "claude",
					Type:   anthropic.Name,
					APIKey: "test-key",
				})
				coord.cfg.Config().Slots[config.SlotImage] = config.SelectedModel{
					Provider: "claude",
					Model:    "claude-3-5-sonnet",
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			coord := newImageReadyCoordinator(t)
			if tc.mutate != nil {
				tc.mutate(t, coord)
			}

			// Fetched after mutate, not before: a mutation that calls
			// SetupAgents publishes a brand-new *config.Config, and an
			// agent snapshot taken earlier would still carry the
			// stale, pre-mutation AllowedTools.
			agentCfg, ok := coord.cfg.Config().Agents[config.AgentCoder]
			require.True(t, ok, "coder agent must be configured")

			toolList, err := coord.buildTools(agentCfg, config.ActiveAgent{}, "", tc.depth)
			require.NoError(t, err)

			var names []string
			for _, tool := range toolList {
				names = append(names, tool.Info().Name)
			}

			if tc.wantOK {
				require.Contains(t, names, toolnames.ImageGenerate)
				require.Contains(t, names, toolnames.ImageEdit)
			} else {
				require.NotContains(t, names, toolnames.ImageGenerate,
					"ImageGenerate must be absent")
				require.NotContains(t, names, toolnames.ImageEdit,
					"ImageEdit must be absent")
			}
		})
	}
}

// TestImageToolsAvailableAlwaysReturnsAReason pins the contract that
// imageToolsAvailable's second return value is populated whether or
// not the tools end up available, since buildTools logs it either way.
func TestImageToolsAvailableAlwaysReturnsAReason(t *testing.T) {
	coord := newImageReadyCoordinator(t)
	agentCfg := coord.cfg.Config().Agents[config.AgentCoder]

	ok, reason := coord.imageToolsAvailable(agentCfg, 0)
	require.True(t, ok)
	require.NotEmpty(t, reason)

	coord.interactive = false
	ok, reason = coord.imageToolsAvailable(agentCfg, 0)
	require.False(t, ok)
	require.NotEmpty(t, reason)
}

// TestImageSelectionUsesConfiguredSlot pins that imageSelection reads
// slots.image when the config sets one, rather than always falling
// back to its default "openai"/imagegen.DefaultModel pair. An
// openai-compat provider is used here to also cover that provider
// family alongside the plain "openai" type exercised elsewhere.
func TestImageSelectionUsesConfiguredSlot(t *testing.T) {
	coord := newImageReadyCoordinator(t)
	coord.cfg.Config().Providers.Set("gateway", config.ProviderConfig{
		ID:     "gateway",
		Type:   openaicompat.Name,
		APIKey: "gateway-key",
	})
	coord.cfg.Config().Slots[config.SlotImage] = config.SelectedModel{
		Provider: "gateway",
		Model:    "custom-image-model",
	}

	model, providerCfg, err := coord.imageSelection(coord.cfg.Config())
	require.NoError(t, err)
	require.Equal(t, config.SelectedModel{Provider: "gateway", Model: "custom-image-model"}, model)
	require.Equal(t, "gateway", providerCfg.ID)
	require.Equal(t, openaicompat.Name, string(providerCfg.Type))
}

// TestImageSelectionTreatsInheritedSlotAsUnset pins that the image
// agent has no dispatcher to inherit a model from — imageSelection is
// only ever consulted for the primary agent — so slot: "inherited" on
// it must fall back to the default image model exactly as an unset
// slot would, rather than trying to resolve "inherited" as a real
// slot name (which would fail, since it names no entry in
// Config.Slots) and losing the image tools entirely.
func TestImageSelectionTreatsInheritedSlotAsUnset(t *testing.T) {
	coord := newImageReadyCoordinator(t)
	agentCfg := coord.cfg.Config().Agents[config.AgentImage]
	agentCfg.Slot = config.SlotInherited
	coord.cfg.Config().Agents[config.AgentImage] = agentCfg

	model, providerCfg, err := coord.imageSelection(coord.cfg.Config())
	require.NoError(t, err)
	require.Equal(t, defaultImageProvider, model.Provider)
	require.Equal(t, imagegen.DefaultModel, model.Model)
	require.Equal(t, defaultImageProvider, providerCfg.ID)
}
