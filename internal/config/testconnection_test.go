package config

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/stretchr/testify/require"
)

// capturePath starts a test server that records the request path it
// received and responds with statusCode and an empty JSON document, which
// is the shape TestConnection requires of a real API endpoint.
func capturePath(statusCode int) (*httptest.Server, *string) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(statusCode)
		w.Write([]byte(`{}`))
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

// spaGateway mimics new-api/one-api: the API lives under "/v1" and every
// other route falls through to the admin single-page app, which answers
// 200 with an HTML shell. Probing the wrong root therefore looks like
// success to anything that only reads the status code.
func spaGateway() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"object":"list","data":[]}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<!doctype html><html><head><title>New API</title></head></html>"))
	}))
}

// TestTestConnectionRejectsSPAFallback pins the bug this guards against:
// a base_url missing the "/v1" segment lands on the gateway's HTML shell,
// which used to pass the probe on its 200 alone and then left every real
// request hitting that same HTML.
func TestTestConnectionRejectsSPAFallback(t *testing.T) {
	t.Parallel()

	server := spaGateway()
	defer server.Close()

	c := &ProviderConfig{ID: "openai", Type: catwalk.TypeOpenAI, BaseURL: server.URL, APIKey: "key"}
	err := c.TestConnection(IdentityResolver())
	require.Error(t, err)
	require.ErrorContains(t, err, "not JSON")
	require.ErrorContains(t, err, "no version segment")
}

// TestTestConnectionAcceptsVersionedGatewayRoot pins the other half: the
// same gateway passes once base_url names the actual API root.
func TestTestConnectionAcceptsVersionedGatewayRoot(t *testing.T) {
	t.Parallel()

	server := spaGateway()
	defer server.Close()

	c := &ProviderConfig{ID: "openai", Type: catwalk.TypeOpenAI, BaseURL: server.URL + "/v1", APIKey: "key"}
	require.NoError(t, c.TestConnection(IdentityResolver()))
}
