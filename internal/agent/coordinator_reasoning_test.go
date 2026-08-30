package agent

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/bedrock"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// reasoningChunks is a chat-completions stream that carries its reasoning
// trace in delta.reasoning_content — the shape OpenAI-compatible gateways
// use, and the one a plain "openai" provider used to drop on the floor.
var reasoningChunks = []string{
	`{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"weighing the options"}}]}`,
	`{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":" then deciding"}}]}`,
	`{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"42"}}]}`,
	`{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
}

func chatCompletionsSSEServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range reasoningChunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

// thinkingEvents is an Anthropic-native stream whose trace arrives as
// thinking_delta parts on a leading thinking content block.
var thinkingEvents = []struct{ event, data string }{
	{"message_start", `{"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","model":"claude-sonnet-5","content":[],"stop_reason":null,"usage":{"input_tokens":1,"output_tokens":1}}}`},
	{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`},
	{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"weighing the options"}}`},
	{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":" then deciding"}}`},
	{"content_block_stop", `{"type":"content_block_stop","index":0}`},
	{"content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`},
	{"content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"42"}}`},
	{"content_block_stop", `{"type":"content_block_stop","index":1}`},
	{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":10}}`},
	{"message_stop", `{"type":"message_stop"}`},
}

func anthropicSSEServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range thinkingEvents {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.event, e.data)
		}
	}))
}

// streamReasoning drives providerCfg through buildProvider and Stream, and
// returns the reasoning text the provider surfaced along with the content.
func streamReasoning(t *testing.T, server *httptest.Server, providerCfg config.ProviderConfig, modelID string) (reasoning, content string) {
	t.Helper()

	coord := newModelPrefTestCoordinator(t, nil)
	defer server.Close()
	providerCfg.BaseURL = server.URL + "/v1"

	coord.cfg.Config().Providers.Set(providerCfg.ID, providerCfg)
	published, ok := coord.cfg.Config().Providers.Get(providerCfg.ID)
	require.True(t, ok)

	model := config.SelectedModel{Provider: providerCfg.ID, Model: modelID}
	provider, err := coord.buildProvider(published, model, false)
	require.NoError(t, err)

	lm, err := provider.LanguageModel(t.Context(), model.Model)
	require.NoError(t, err)

	stream, err := lm.Stream(t.Context(), fantasy.Call{
		Prompt: fantasy.Prompt{fantasy.NewUserMessage("hi")},
	})
	require.NoError(t, err)

	for part := range stream {
		switch part.Type {
		case fantasy.StreamPartTypeReasoningDelta:
			reasoning += part.Delta
		case fantasy.StreamPartTypeTextDelta:
			content += part.Delta
		}
	}
	return reasoning, content
}

// A gateway reached through the default "openai" provider type must still
// surface its reasoning. The provider parses reasoning only on its Responses
// path, so a model ID that does not look like an OpenAI Responses model takes
// chat-completions — where the trace was silently discarded and the turn
// rendered with no thinking at all.
func TestBuildOpenaiProviderSurfacesReasoningContent(t *testing.T) {
	reasoning, content := streamReasoning(t, chatCompletionsSSEServer(t), config.ProviderConfig{
		ID:     "custom-gateway",
		Type:   catwalk.TypeOpenAI,
		APIKey: "test-key",
	}, "gpt-codex-sol")

	require.Equal(t, "weighing the options then deciding", reasoning)
	require.Equal(t, "42", content)
}

// The openai-compat type already worked; pin it so the shared hooks cannot
// regress for one type while staying green for the other.
func TestBuildOpenaiCompatProviderSurfacesReasoningContent(t *testing.T) {
	reasoning, content := streamReasoning(t, chatCompletionsSSEServer(t), config.ProviderConfig{
		ID:     "custom-gateway-compat",
		Type:   catwalk.TypeOpenAICompat,
		APIKey: "test-key",
	}, "gpt-codex-sol")

	require.Equal(t, "weighing the options then deciding", reasoning)
	require.Equal(t, "42", content)
}

// The Anthropic type reads its trace from thinking_delta parts rather than
// delta.reasoning_content, so it needs no extra hook. Pin it anyway: when a
// gateway serves Claude with the thinking text stripped, this test is what
// distinguishes a gateway that sends nothing from a client that drops it.
func TestBuildAnthropicProviderSurfacesThinking(t *testing.T) {
	reasoning, content := streamReasoning(t, anthropicSSEServer(t), config.ProviderConfig{
		ID:     "custom-gateway-anthropic",
		Type:   catwalk.TypeAnthropic,
		APIKey: "test-key",
	}, "claude-sonnet-5-free")

	require.Equal(t, "weighing the options then deciding", reasoning)
	require.Equal(t, "42", content)
}

// anthropicThinkingDisplay builds the provider options for modelID and
// returns the thinking display Angela ended up requesting.
func anthropicThinkingDisplay(t *testing.T, providerType, modelID string) *anthropic.ThinkingDisplay {
	t.Helper()

	model := Model{
		CatwalkCfg: catwalk.Model{
			ID:              modelID,
			CanReason:       true,
			ReasoningLevels: []string{"high", "max"},
		},
		ModelCfg: config.SelectedModel{
			Provider:        "gw",
			Model:           modelID,
			ReasoningEffort: "max",
		},
	}
	providerCfg := config.ProviderConfig{
		ID:   "gw",
		Type: catwalk.Type(providerType),
	}

	raw, ok := getProviderOptions(model, providerCfg, "")[anthropic.Name]
	require.True(t, ok, "no anthropic options produced")
	parsed, ok := raw.(*anthropic.ProviderOptions)
	require.True(t, ok)
	return parsed.ThinkingDisplay
}

// Whether a thinking trace is visible must not depend on the model ID.
// Anthropic's adaptive thinking hides its trace by default and fantasy
// only opts specific model IDs back in, so any renamed or gateway-served
// model would think with nothing to show.
func TestGetProviderOptionsAlwaysRequestsVisibleThinking(t *testing.T) {
	for _, modelID := range []string{
		"claude-sonnet-5-free",
		"claude-mythos-preview",
		"claude-opus-4-5",
		"some-vendor-rename",
	} {
		t.Run(modelID, func(t *testing.T) {
			display := anthropicThinkingDisplay(t, string(anthropic.Name), modelID)
			require.NotNil(t, display, "thinking display must be set for %s", modelID)
			require.Equal(t, anthropic.ThinkingDisplaySummarized, *display)
		})
	}
}

// Bedrock shares the Anthropic option path and must get the same
// treatment; the rule is per-request, not per-provider.
func TestGetProviderOptionsRequestsVisibleThinkingOnBedrock(t *testing.T) {
	display := anthropicThinkingDisplay(t, string(bedrock.Name), "claude-sonnet-5-free")
	require.NotNil(t, display)
	require.Equal(t, anthropic.ThinkingDisplaySummarized, *display)
}
