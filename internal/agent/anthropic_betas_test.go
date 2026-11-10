package agent

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAutoAnthropicBetas(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		merged map[string]any
		think  bool
		want   []string
	}{
		{name: "nothing requested"},
		{
			name:  "think turns on interleaved thinking",
			think: true,
			want:  []string{"interleaved-thinking-2025-05-14"},
		},
		{
			name:   "configured thinking turns on interleaved thinking",
			merged: map[string]any{"thinking": map[string]any{"budget_tokens": 2000}},
			want:   []string{"interleaved-thinking-2025-05-14"},
		},
		{
			name:   "context management",
			merged: extraBody("context_management", map[string]any{"edits": []any{map[string]any{"type": "clear_tool_uses_20250919"}}}),
			want:   []string{"context-management-2025-06-27"},
		},
		{
			name:   "compaction edit adds compact beta",
			merged: extraBody("context_management", map[string]any{"edits": []any{map[string]any{"type": "compact_20260112"}}}),
			want:   []string{"context-management-2025-06-27", "compact-2026-01-12"},
		},
		{
			name:   "fast speed",
			merged: extraBody("speed", "Fast"),
			want:   []string{"fast-mode-2026-02-01"},
		},
		{
			name:   "standard speed is not fast mode",
			merged: extraBody("speed", "standard"),
		},
		{
			name:   "task budget as a nested output_config",
			merged: extraBody("output_config", map[string]any{"task_budget": map[string]any{"type": "tokens", "total": 20000}}),
			want:   []string{"task-budgets-2026-03-13"},
		},
		{
			name:   "task budget as a dotted key",
			merged: extraBody("output_config.task_budget", map[string]any{"type": "tokens", "total": 20000}),
			want:   []string{"task-budgets-2026-03-13"},
		},
		{
			name:   "output_config without a task budget",
			merged: extraBody("output_config", map[string]any{"format": map[string]any{"type": "json_schema"}}),
		},
		{
			name:   "thinking display updates",
			merged: map[string]any{"thinking_display": "updates"},
			want:   []string{"thinking-display-updates-2026-08-18"},
		},
		{
			name:   "summarized display needs no beta",
			merged: map[string]any{"thinking_display": "summarized"},
		},
		{
			name:   "mcp servers",
			merged: extraBody("mcp_servers", []any{map[string]any{"name": "docs"}}),
			want:   []string{"mcp-client-2025-11-20"},
		},
		{
			name:   "rules keep their order",
			merged: map[string]any{"thinking_display": "updates", "extra_body": map[string]any{"mcp_servers": []any{}, "speed": "fast"}},
			think:  true,
			want: []string{
				"interleaved-thinking-2025-05-14",
				"fast-mode-2026-02-01",
				"thinking-display-updates-2026-08-18",
				"mcp-client-2025-11-20",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, autoAnthropicBetas(tt.merged, tt.think))
		})
	}
}

func extraBody(key string, value any) map[string]any {
	return map[string]any{"extra_body": map[string]any{key: value}}
}

func TestApplyAnthropicBetas(t *testing.T) {
	t.Parallel()

	const interleaved = "interleaved-thinking-2025-05-14"

	tests := []struct {
		name        string
		headers     map[string]string
		providerCfg config.ProviderConfig
		model       config.ProviderModel
		think       bool
		want        map[string]string
	}{
		{
			name:    "no beta leaves no header",
			headers: map[string]string{},
			want:    map[string]string{},
		},
		{
			name:    "think adds interleaved thinking",
			headers: map[string]string{},
			think:   true,
			want:    map[string]string{"anthropic-beta": interleaved},
		},
		{
			name:    "existing header values are kept in front",
			headers: map[string]string{"anthropic-beta": "files-api-2025-04-14"},
			think:   true,
			want:    map[string]string{"anthropic-beta": "files-api-2025-04-14," + interleaved},
		},
		{
			name:    "header key casing does not matter",
			headers: map[string]string{"Anthropic-Beta": "a, b", "X-Other": "1"},
			think:   true,
			want:    map[string]string{"anthropic-beta": "a,b," + interleaved, "X-Other": "1"},
		},
		{
			name:    "duplicates collapse",
			headers: map[string]string{"anthropic-beta": interleaved},
			think:   true,
			want:    map[string]string{"anthropic-beta": interleaved},
		},
		{
			name:        "provider enable is appended",
			headers:     map[string]string{},
			providerCfg: config.ProviderConfig{AnthropicBeta: &config.AnthropicBeta{Enable: []string{"mcp-client-2026-09-15"}}},
			think:       true,
			want:        map[string]string{"anthropic-beta": interleaved + ",mcp-client-2026-09-15"},
		},
		{
			name:        "disable removes an automatic beta",
			headers:     map[string]string{},
			providerCfg: config.ProviderConfig{AnthropicBeta: &config.AnthropicBeta{Disable: []string{interleaved}}},
			think:       true,
			want:        map[string]string{},
		},
		{
			name:        "disable removes a header value",
			headers:     map[string]string{"anthropic-beta": "a,b"},
			providerCfg: config.ProviderConfig{AnthropicBeta: &config.AnthropicBeta{Disable: []string{"a"}}},
			want:        map[string]string{"anthropic-beta": "b"},
		},
		{
			name:        "model layer beats provider layer",
			headers:     map[string]string{},
			providerCfg: config.ProviderConfig{AnthropicBeta: &config.AnthropicBeta{Disable: []string{"x"}, Enable: []string{"y"}}},
			model:       config.ProviderModel{AnthropicBeta: &config.AnthropicBeta{Enable: []string{"x"}, Disable: []string{"y"}}},
			want:        map[string]string{"anthropic-beta": "x"},
		},
		{
			name:    "provider options select betas without think",
			headers: map[string]string{},
			providerCfg: config.ProviderConfig{ProviderOptions: map[string]any{
				"thinking_display": "updates",
				"extra_body":       map[string]any{"mcp_servers": []any{}},
			}},
			want: map[string]string{"anthropic-beta": "thinking-display-updates-2026-08-18,mcp-client-2025-11-20"},
		},
		{
			name:    "model options select betas",
			headers: map[string]string{},
			model: config.ProviderModel{Model: catwalk.Model{Options: catwalk.ModelOptions{
				ProviderOptions: map[string]any{"extra_body": map[string]any{"speed": "fast"}},
			}}},
			want: map[string]string{"anthropic-beta": "fast-mode-2026-02-01"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			applyAnthropicBetas(tt.headers, tt.providerCfg, tt.model, tt.think)
			require.Equal(t, tt.want, tt.headers)
		})
	}
}

// A variant that re-enables a beta the provider switched off wins, because
// the model passed to applyAnthropicBetas already carries the variant.
func TestApplyAnthropicBetasVariantBeatsProvider(t *testing.T) {
	t.Parallel()

	providerCfg := config.ProviderConfig{AnthropicBeta: &config.AnthropicBeta{Disable: []string{"mcp-client-2026-09-15"}}}
	model := config.ProviderModel{
		Model: catwalk.Model{ID: "claude-sonnet-5"},
		Variants: map[string]config.SelectedModelOverride{
			"mcp": {AnthropicBeta: &config.AnthropicBeta{Enable: []string{"mcp-client-2026-09-15"}}},
		},
	}

	plain := map[string]string{}
	applyAnthropicBetas(plain, providerCfg, model, false)
	require.Empty(t, plain)

	withVariant, ok := model.WithVariant("mcp")
	require.True(t, ok)
	got := map[string]string{}
	applyAnthropicBetas(got, providerCfg, withVariant, false)
	require.Equal(t, map[string]string{"anthropic-beta": "mcp-client-2026-09-15"}, got)
}

func TestFlattenOutputConfigKeepsEffort(t *testing.T) {
	t.Parallel()

	model := Model{
		CatwalkCfg: config.ProviderModel{Model: catwalk.Model{
			ID:                     "claude-opus-4-5",
			CanReason:              true,
			ReasoningLevels:        []string{"high", "max"},
			DefaultReasoningEffort: "max",
		}},
		ModelCfg: config.SelectedModel{Provider: "gw", Model: "claude-opus-4-5"},
	}
	providerCfg := config.ProviderConfig{
		ID:   "gw",
		Type: catwalk.TypeAnthropic,
		ProviderOptions: map[string]any{"extra_body": map[string]any{
			"output_config": map[string]any{"task_budget": map[string]any{"type": "tokens", "total": 20000}},
		}},
	}

	raw, ok := getProviderOptions(model, providerCfg, "")[anthropic.Name]
	require.True(t, ok)
	parsed, ok := raw.(*anthropic.ProviderOptions)
	require.True(t, ok)

	require.NotNil(t, parsed.Effort, "effort must still be sent")
	require.NotContains(t, parsed.ExtraBody, "output_config")
	require.Contains(t, parsed.ExtraBody, "output_config.task_budget")
}

type recordedRequest struct {
	betas []string
	body  string
}

// anthropicRecordingServer answers with the canned thinking stream and keeps
// every request it saw.
func anthropicRecordingServer(t *testing.T) (*httptest.Server, func() []recordedRequest) {
	t.Helper()

	var (
		mu   sync.Mutex
		seen []recordedRequest
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var betas []string
		for _, v := range r.Header.Values("anthropic-beta") {
			for part := range strings.SplitSeq(v, ",") {
				betas = append(betas, strings.TrimSpace(part))
			}
		}
		mu.Lock()
		seen = append(seen, recordedRequest{betas: betas, body: string(body)})
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range thinkingEvents {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.event, e.data)
		}
	}))
	t.Cleanup(server.Close)

	return server, func() []recordedRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]recordedRequest(nil), seen...)
	}
}

// streamAnthropicOnce builds the provider the way a turn does and sends one
// request carrying the provider options Angela computes for it.
func streamAnthropicOnce(t *testing.T, server *httptest.Server, providerCfg config.ProviderConfig, model config.ProviderModel, think bool) {
	t.Helper()

	coord := newModelPrefTestCoordinator(t, nil)
	providerCfg.BaseURL = server.URL + "/v1"
	providerCfg.APIKey = "test-key"
	providerCfg.Type = catwalk.TypeAnthropic

	coord.cfg.Config().Providers.Set(providerCfg.ID, providerCfg)
	published, ok := coord.cfg.Config().Providers.Get(providerCfg.ID)
	require.True(t, ok)

	provider, err := coord.models().buildProvider(published, model, think, false)
	require.NoError(t, err)
	lm, err := provider.LanguageModel(t.Context(), model.ID)
	require.NoError(t, err)

	opts := getProviderOptions(Model{
		CatwalkCfg: model,
		ModelCfg:   config.SelectedModel{Provider: providerCfg.ID, Model: model.ID},
		Think:      think,
	}, published, "")

	stream, err := lm.Stream(t.Context(), fantasy.Call{
		Prompt:          fantasy.Prompt{fantasy.NewUserMessage("hi")},
		ProviderOptions: opts,
	})
	require.NoError(t, err)
	for range stream {
	}
}

func TestAnthropicProviderSendsThinkingBeta(t *testing.T) {
	server, requests := anthropicRecordingServer(t)

	streamAnthropicOnce(t, server, config.ProviderConfig{ID: "gw-think"},
		config.ProviderModel{Model: catwalk.Model{ID: "claude-sonnet-5"}}, true)

	got := requests()
	require.Len(t, got, 1)
	require.Equal(t, []string{"interleaved-thinking-2025-05-14"}, got[0].betas)
}

func TestAnthropicProviderSendsManualAndAutomaticBetas(t *testing.T) {
	server, requests := anthropicRecordingServer(t)

	streamAnthropicOnce(t, server, config.ProviderConfig{
		ID:            "gw-manual",
		AnthropicBeta: &config.AnthropicBeta{Enable: []string{"advisor-tool-2026-03-01"}},
		ProviderOptions: map[string]any{"extra_body": map[string]any{
			"mcp_servers":   []any{map[string]any{"type": "url", "name": "docs", "url": "https://example.com/mcp"}},
			"output_config": map[string]any{"task_budget": map[string]any{"type": "tokens", "total": 20000}},
		}},
	}, config.ProviderModel{Model: catwalk.Model{
		ID:                     "claude-opus-4-5",
		CanReason:              true,
		ReasoningLevels:        []string{"high", "max"},
		DefaultReasoningEffort: "max",
	}}, false)

	got := requests()
	require.Len(t, got, 1)
	require.Equal(t, []string{
		"task-budgets-2026-03-13",
		"mcp-client-2025-11-20",
		"advisor-tool-2026-03-01",
	}, got[0].betas)

	body := got[0].body
	require.Equal(t, "docs", gjson.Get(body, "mcp_servers.0.name").String())
	require.Equal(t, "max", gjson.Get(body, "output_config.effort").String(),
		"effort must survive a configured task budget")
	require.Equal(t, int64(20000), gjson.Get(body, "output_config.task_budget.total").Int())
}

func TestAnthropicProviderSendsNoBetaHeaderByDefault(t *testing.T) {
	server, requests := anthropicRecordingServer(t)

	streamAnthropicOnce(t, server, config.ProviderConfig{ID: "gw-plain"},
		config.ProviderModel{Model: catwalk.Model{ID: "claude-sonnet-5"}}, false)

	got := requests()
	require.Len(t, got, 1)
	require.Empty(t, got[0].betas)
}
