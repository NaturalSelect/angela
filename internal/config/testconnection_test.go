package config

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/stretchr/testify/require"
)

// capturePath starts a test server that records the request path it
// received and responds with statusCode. The returned pointer is
// populated once the server has been hit.
func capturePath(statusCode int) (*httptest.Server, *string) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(statusCode)
	}))
	return server, &gotPath
}

// TestTestConnectionNormalizesAnthropicV1 pins that TestConnection hits
// "/v1/models" exactly once even when the user's base_url already
// carries a "/v1" suffix — mirroring what buildAnthropicProvider now
// does for the real request path.
func TestTestConnectionNormalizesAnthropicV1(t *testing.T) {
	t.Parallel()

	server, gotPath := capturePath(http.StatusOK)
	defer server.Close()

	c := &ProviderConfig{ID: "custom-anthropic", Type: catwalk.TypeAnthropic, BaseURL: server.URL + "/v1", APIKey: "key"}
	require.NoError(t, c.TestConnection(IdentityResolver()))
	require.Equal(t, "/v1/models", *gotPath)
}

// TestTestConnectionOpenAICompatNormalizesCopiedEndpoint pins that a
// base_url copied from vendor docs with a trailing "/chat/completions"
// does not end up duplicated in the probe request.
func TestTestConnectionOpenAICompatNormalizesCopiedEndpoint(t *testing.T) {
	t.Parallel()

	server, gotPath := capturePath(http.StatusOK)
	defer server.Close()

	c := &ProviderConfig{ID: "custom-openai-compat", Type: catwalk.TypeOpenAICompat, BaseURL: server.URL + "/v1/chat/completions", APIKey: "key"}
	require.NoError(t, c.TestConnection(IdentityResolver()))
	require.Equal(t, "/v1/models", *gotPath)
}

// TestTestConnectionOpenCodeGoStillStripsGoSegment pins that the
// pre-existing opencode-go "/go" endpoint rewrite still applies after
// normalization is inserted ahead of it.
func TestTestConnectionOpenCodeGoStillStripsGoSegment(t *testing.T) {
	t.Parallel()

	server, gotPath := capturePath(http.StatusOK)
	defer server.Close()

	c := &ProviderConfig{ID: string(catwalk.InferenceProviderOpenCodeGo), Type: catwalk.TypeOpenAI, BaseURL: server.URL + "/zen/v1/go", APIKey: "key"}
	require.NoError(t, c.TestConnection(IdentityResolver()))
	require.Equal(t, "/zen/v1/models", *gotPath)
}

// TestTestConnectionOpenRouterNormalizesBaseURL pins that OpenRouter's
// "/credits" probe still fires against a normalized base_url.
func TestTestConnectionOpenRouterNormalizesBaseURL(t *testing.T) {
	t.Parallel()

	server, gotPath := capturePath(http.StatusOK)
	defer server.Close()

	c := &ProviderConfig{ID: string(catwalk.InferenceProviderOpenRouter), Type: catwalk.TypeOpenRouter, BaseURL: server.URL + "/api/v1/responses", APIKey: "key"}
	require.NoError(t, c.TestConnection(IdentityResolver()))
	require.Equal(t, "/api/v1/credits", *gotPath)
}

// TestTestConnection404HintsMissingVersion pins that a 404 against an
// unversioned OpenAI-style base_url produces an error hinting at the
// missing "/v1" segment, so users get an actionable message instead of a
// bare status code.
func TestTestConnection404HintsMissingVersion(t *testing.T) {
	t.Parallel()

	server, _ := capturePath(http.StatusNotFound)
	defer server.Close()

	c := &ProviderConfig{ID: "custom-openai-compat", Type: catwalk.TypeOpenAICompat, BaseURL: server.URL, APIKey: "key"}
	err := c.TestConnection(IdentityResolver())
	require.ErrorContains(t, err, "no version segment")
}

// TestTestConnection404NoHintWhenAlreadyVersioned pins that a 404
// against an already-versioned base_url does not print a misleading
// "missing version" hint — the failure is something else.
func TestTestConnection404NoHintWhenAlreadyVersioned(t *testing.T) {
	t.Parallel()

	server, _ := capturePath(http.StatusNotFound)
	defer server.Close()

	c := &ProviderConfig{ID: "custom-openai-compat", Type: catwalk.TypeOpenAICompat, BaseURL: server.URL + "/v1", APIKey: "key"}
	err := c.TestConnection(IdentityResolver())
	require.ErrorContains(t, err, "404")
	require.NotContains(t, err.Error(), "no version segment")
}
