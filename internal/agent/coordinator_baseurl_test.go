package agent

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// generateWithCapturedPath drives providerCfg all the way through
// buildProvider, LanguageModel, and Generate against a local test
// server, and returns the request path the underlying SDK actually
// sent. Generate's own response is discarded: the server response is
// intentionally minimal (or an error), because only the request path
// under test matters.
func generateWithCapturedPath(t *testing.T, coord *coordinator, providerCfg config.ProviderConfig, model config.SelectedModel) string {
	t.Helper()

	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	providerCfg.BaseURL = server.URL + providerCfg.BaseURL

	coord.cfg.Config().Providers.Set(providerCfg.ID, providerCfg)
	published, ok := coord.cfg.Config().Providers.Get(providerCfg.ID)
	require.True(t, ok)

	provider, err := coord.buildProvider(published, model, false)
	require.NoError(t, err)

	lm, err := provider.LanguageModel(t.Context(), model.Model)
	require.NoError(t, err)

	_, _ = lm.Generate(t.Context(), fantasy.Call{
		Prompt: fantasy.Prompt{fantasy.NewUserMessage("hi")},
	})
	return gotPath
}

// TestBuildAnthropicProviderNormalizesUserSuppliedV1 pins the exact bug
// report this fix addresses: an Anthropic-type base URL copied with a
// trailing "/v1" must not turn into "/v1/v1/messages" on the wire, since
// the Anthropic SDK already hardcodes "v1/messages" as a relative path.
func TestBuildAnthropicProviderNormalizesUserSuppliedV1(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	providerCfg := config.ProviderConfig{
		ID:      "custom-anthropic",
		Type:    catwalk.TypeAnthropic,
		BaseURL: "/v1", // appended to the httptest server URL below.
		APIKey:  "test-key",
	}
	model := config.SelectedModel{Provider: providerCfg.ID, Model: "claude-test"}

	gotPath := generateWithCapturedPath(t, coord, providerCfg, model)
	require.Equal(t, "/v1/messages", gotPath)
}

// TestBuildOpenaiCompatProviderNormalizesCopiedEndpoint pins that an
// openai-compat base URL copied straight from vendor REST docs (which
// typically show the full "/v1/chat/completions" endpoint, not just the
// base) does not get "/chat/completions" duplicated on the wire.
func TestBuildOpenaiCompatProviderNormalizesCopiedEndpoint(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	providerCfg := config.ProviderConfig{
		ID:      "custom-openai-compat",
		Type:    catwalk.TypeOpenAICompat,
		BaseURL: "/v1/chat/completions", // appended to the httptest server URL below.
		APIKey:  "test-key",
	}
	model := config.SelectedModel{Provider: providerCfg.ID, Model: "gpt-test"}

	gotPath := generateWithCapturedPath(t, coord, providerCfg, model)
	require.Equal(t, "/v1/chat/completions", gotPath)
}

// TestBuildOpenaiCompatProviderPreservesBareDomain pins that a built-in
// provider whose real endpoint sits at the domain root (like Copilot) is
// left untouched by normalization — the fix only strips accidentally
// duplicated suffixes, it never appends a missing version segment.
func TestBuildOpenaiCompatProviderPreservesBareDomain(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	providerCfg := config.ProviderConfig{
		ID:      "custom-openai-compat-bare",
		Type:    catwalk.TypeOpenAICompat,
		BaseURL: "", // the httptest server's bare URL, appended below.
		APIKey:  "test-key",
	}
	model := config.SelectedModel{Provider: providerCfg.ID, Model: "gpt-test"}

	gotPath := generateWithCapturedPath(t, coord, providerCfg, model)
	require.Equal(t, "/chat/completions", gotPath)
}
