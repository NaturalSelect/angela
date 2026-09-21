package config

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/env"
	"github.com/stretchr/testify/require"
)

// TestComputeDataFieldTrust_UntrustedLayerMarksProviderFieldUntrusted pins
// that a provider data field set by an untrusted layer (e.g. a
// project-level angela.json, auto-loaded even from a freshly cloned,
// untrusted repository) is recorded as untrusted.
func TestComputeDataFieldTrust_UntrustedLayerMarksProviderFieldUntrusted(t *testing.T) {
	t.Parallel()

	layers := [][]byte{[]byte(`{"providers":{"mock":{"api_key":"$(echo x)"}}}`)}
	trust := computeDataFieldTrust([]string{"/project/angela.json"}, layers, func(string) bool { return false })

	require.True(t, trust.providerUntrusted("mock", "api_key"))
}

// TestComputeDataFieldTrust_TrustedLayerLeavesProviderFieldTrusted pins
// the counterpart: a field set only by the system or global config,
// which is always user- or admin-authored, stays trusted.
func TestComputeDataFieldTrust_TrustedLayerLeavesProviderFieldTrusted(t *testing.T) {
	t.Parallel()

	layers := [][]byte{[]byte(`{"providers":{"mock":{"api_key":"$(echo x)"}}}`)}
	trust := computeDataFieldTrust([]string{"/global/angela.json"}, layers, func(string) bool { return true })

	require.False(t, trust.providerUntrusted("mock", "api_key"))
}

// TestComputeDataFieldTrust_LastWriterWins pins that when both a trusted
// and an untrusted layer set the same field, trust follows whichever
// layer supplies the post-merge value -- the layer processed last,
// matching jsons.Merge's key-by-key "later layer wins" semantics -- not
// whichever layer happened to touch it first.
func TestComputeDataFieldTrust_LastWriterWins(t *testing.T) {
	t.Parallel()

	paths := []string{"/global/angela.json", "/project/angela.json"}
	layers := [][]byte{
		[]byte(`{"providers":{"mock":{"api_key":"global-value"}}}`),
		[]byte(`{"providers":{"mock":{"api_key":"project-value"}}}`),
	}
	isTrusted := func(path string) bool { return path == "/global/angela.json" }

	trust := computeDataFieldTrust(paths, layers, isTrusted)

	require.True(t, trust.providerUntrusted("mock", "api_key"),
		"the project layer, processed last, supplies the merged value")
}

// TestComputeDataFieldTrust_TrustedOverrideRestoresTrust pins the
// reverse of TestComputeDataFieldTrust_LastWriterWins: trust is
// recomputed from scratch on every sighting rather than sticky once
// untrusted, so a later trusted layer clears an earlier untrusted
// sighting of the same field.
func TestComputeDataFieldTrust_TrustedOverrideRestoresTrust(t *testing.T) {
	t.Parallel()

	paths := []string{"/project/angela.json", "/global/angela.json"}
	layers := [][]byte{
		[]byte(`{"providers":{"mock":{"api_key":"project-value"}}}`),
		[]byte(`{"providers":{"mock":{"api_key":"global-value"}}}`),
	}
	isTrusted := func(path string) bool { return path == "/global/angela.json" }

	trust := computeDataFieldTrust(paths, layers, isTrusted)

	require.False(t, trust.providerUntrusted("mock", "api_key"))
}

// TestComputeDataFieldTrust_PerFieldGranularity pins that trust is
// tracked per field, not per provider: overriding one field from an
// untrusted layer must not drag down a sibling field the untrusted
// layer never touched.
func TestComputeDataFieldTrust_PerFieldGranularity(t *testing.T) {
	t.Parallel()

	paths := []string{"/global/angela.json", "/project/angela.json"}
	layers := [][]byte{
		[]byte(`{"providers":{"mock":{"api_key":"global-value","base_url":"https://global.example.com"}}}`),
		[]byte(`{"providers":{"mock":{"base_url":"https://project.example.com"}}}`),
	}
	isTrusted := func(path string) bool { return path == "/global/angela.json" }

	trust := computeDataFieldTrust(paths, layers, isTrusted)

	require.False(t, trust.providerUntrusted("mock", "api_key"), "api_key was never touched by the project layer")
	require.True(t, trust.providerUntrusted("mock", "base_url"), "base_url was overridden by the project layer")
}

// TestComputeDataFieldTrust_UnsetFieldStaysTrusted pins that a field no
// layer ever set (e.g. a known provider's built-in catwalk default) has
// no entry at all, and therefore reads as trusted.
func TestComputeDataFieldTrust_UnsetFieldStaysTrusted(t *testing.T) {
	t.Parallel()

	layers := [][]byte{[]byte(`{"providers":{"mock":{"base_url":"https://project.example.com"}}}`)}
	trust := computeDataFieldTrust([]string{"/project/angela.json"}, layers, func(string) bool { return false })

	require.False(t, trust.providerUntrusted("mock", "api_key"), "a field no layer ever set has no origin to distrust")
}

// TestComputeDataFieldTrust_MCPCommandArgsEnvNeverTracked pins the
// explicit exclusion documented on mcpTrustFields: command, args, and
// env already grant arbitrary program execution regardless of whether
// "$(...)" inside them is allowed to run, so gating them buys no safety
// margin and would only break legitimate untrusted-repo use, like a
// project-local MCP server launched via a repo-relative script.
func TestComputeDataFieldTrust_MCPCommandArgsEnvNeverTracked(t *testing.T) {
	t.Parallel()

	layers := [][]byte{[]byte(`{"mcp":{"svc":{"command":"$(rm -rf /)","args":["$(x)"],"env":{"A":"$(x)"}}}}`)}
	trust := computeDataFieldTrust([]string{"/project/angela.json"}, layers, func(string) bool { return false })

	require.False(t, trust.mcpUntrusted("svc", "command"))
	require.False(t, trust.mcpUntrusted("svc", "args"))
	require.False(t, trust.mcpUntrusted("svc", "env"))
}

// TestComputeDataFieldTrust_ExtraHeadersTrackedAsWholeUnit pins that
// extra_headers is tracked as a single unit covering every key in the
// map, not per header key: headers collectively form one request's
// identity, so letting an untrusted layer poison just one key while the
// rest keep the trusted layer's shell-expansion privilege would offer
// no real safety margin.
func TestComputeDataFieldTrust_ExtraHeadersTrackedAsWholeUnit(t *testing.T) {
	t.Parallel()

	layers := [][]byte{[]byte(`{"providers":{"mock":{"extra_headers":{"X-Foo":"$(echo x)"}}}}`)}
	trust := computeDataFieldTrust([]string{"/project/angela.json"}, layers, func(string) bool { return false })

	require.True(t, trust.providerUntrusted("mock", "extra_headers"))
}

// TestComputeDataFieldTrust_MCPFieldFromUntrustedLayer pins that the
// same untrusted-layer classification applies to MCP entries, not just
// providers.
func TestComputeDataFieldTrust_MCPFieldFromUntrustedLayer(t *testing.T) {
	t.Parallel()

	layers := [][]byte{[]byte(`{"mcp":{"svc":{"url":"$(echo x)"}}}`)}
	trust := computeDataFieldTrust([]string{"/project/angela.json"}, layers, func(string) bool { return false })

	require.True(t, trust.mcpUntrusted("svc", "url"))
}

// TestDataFieldTrust_NilTreatsEverythingAsTrusted pins the backward
// compatibility contract configureProvidersOptions documents: the many
// existing tests that build a Config/ConfigStore directly and call
// configureProviders without ever supplying withDataTrust get a nil
// *dataFieldTrust, which must behave exactly like the pre-trust-split
// world -- every field trusted.
func TestDataFieldTrust_NilTreatsEverythingAsTrusted(t *testing.T) {
	t.Parallel()

	var trust *dataFieldTrust
	require.False(t, trust.providerUntrusted("mock", "api_key"))
	require.False(t, trust.mcpUntrusted("svc", "url"))
}

// TestProviderFieldResolver_UntrustedFieldBlocksCommandSubstitution pins
// the end-to-end behavior ConfigStore.ProviderFieldResolver exists for:
// a provider api_key last set by an untrusted layer resolves through
// the env-only resolver, so "$(...)" is left as a literal instead of
// being executed.
func TestProviderFieldResolver_UntrustedFieldBlocksCommandSubstitution(t *testing.T) {
	t.Parallel()

	e := env.New()
	store := &ConfigStore{
		resolver:        NewShellVariableResolver(e),
		envOnlyResolver: NewEnvOnlyVariableResolver(e),
		dataTrust: computeDataFieldTrust(
			[]string{"/project/angela.json"},
			[][]byte{[]byte(`{"providers":{"mock":{"api_key":"placeholder"}}}`)},
			func(string) bool { return false },
		),
	}

	got, err := store.ProviderFieldResolver("mock", "api_key").ResolveValue("$(echo pwned)")
	require.NoError(t, err)
	require.Equal(t, "$(echo pwned)", got, "an untrusted layer's api_key must never gain command-execution power")
}

// TestProviderFieldResolver_TrustedFieldAllowsCommandSubstitution pins
// the counterpart: a field last set by the system or global config
// resolves through the full shell resolver, so "$(...)" still runs.
func TestProviderFieldResolver_TrustedFieldAllowsCommandSubstitution(t *testing.T) {
	t.Parallel()

	e := env.New()
	store := &ConfigStore{
		resolver:        NewShellVariableResolver(e),
		envOnlyResolver: NewEnvOnlyVariableResolver(e),
		dataTrust: computeDataFieldTrust(
			[]string{"/global/angela.json"},
			[][]byte{[]byte(`{"providers":{"mock":{"api_key":"placeholder"}}}`)},
			func(string) bool { return true },
		),
	}

	got, err := store.ProviderFieldResolver("mock", "api_key").ResolveValue("$(echo trusted-secret)")
	require.NoError(t, err)
	require.Equal(t, "trusted-secret", got)
}

// TestProviderFieldResolver_PerFieldGranularityAcrossLayers pins that
// ProviderFieldResolver's choice is made independently for each field
// of the same provider: api_key set only by the global config keeps
// full shell resolution even though the project layer overrides a
// sibling field (base_url) on the very same provider entry.
func TestProviderFieldResolver_PerFieldGranularityAcrossLayers(t *testing.T) {
	t.Parallel()

	e := env.New()
	paths := []string{"/global/angela.json", "/project/angela.json"}
	layers := [][]byte{
		[]byte(`{"providers":{"mock":{"api_key":"placeholder","base_url":"placeholder"}}}`),
		[]byte(`{"providers":{"mock":{"base_url":"placeholder"}}}`),
	}
	store := &ConfigStore{
		resolver:        NewShellVariableResolver(e),
		envOnlyResolver: NewEnvOnlyVariableResolver(e),
		dataTrust: computeDataFieldTrust(paths, layers, func(path string) bool {
			return path == "/global/angela.json"
		}),
	}

	apiKey, err := store.ProviderFieldResolver("mock", "api_key").ResolveValue("$(echo trusted)")
	require.NoError(t, err)
	require.Equal(t, "trusted", apiKey, "api_key came only from the global layer")

	baseURL, err := store.ProviderFieldResolver("mock", "base_url").ResolveValue("$(echo pwned)")
	require.NoError(t, err)
	require.Equal(t, "$(echo pwned)", baseURL, "base_url was overridden by the project layer")
}

// TestMCPFieldResolver_UntrustedURLBlocksCommandSubstitution mirrors
// TestProviderFieldResolver_UntrustedFieldBlocksCommandSubstitution for
// MCP config: a server URL last set by an untrusted layer never gains
// command-execution power.
func TestMCPFieldResolver_UntrustedURLBlocksCommandSubstitution(t *testing.T) {
	t.Parallel()

	e := env.New()
	store := &ConfigStore{
		resolver:        NewShellVariableResolver(e),
		envOnlyResolver: NewEnvOnlyVariableResolver(e),
		dataTrust: computeDataFieldTrust(
			[]string{"/project/angela.json"},
			[][]byte{[]byte(`{"mcp":{"svc":{"url":"placeholder"}}}`)},
			func(string) bool { return false },
		),
	}

	got, err := store.MCPFieldResolver("svc", "url").ResolveValue("$(echo pwned)")
	require.NoError(t, err)
	require.Equal(t, "$(echo pwned)", got)
}

// TestMCPFieldResolver_TrustedURLAllowsCommandSubstitution mirrors
// TestProviderFieldResolver_TrustedFieldAllowsCommandSubstitution for
// MCP config.
func TestMCPFieldResolver_TrustedURLAllowsCommandSubstitution(t *testing.T) {
	t.Parallel()

	e := env.New()
	store := &ConfigStore{
		resolver:        NewShellVariableResolver(e),
		envOnlyResolver: NewEnvOnlyVariableResolver(e),
		dataTrust: computeDataFieldTrust(
			[]string{"/global/angela.json"},
			[][]byte{[]byte(`{"mcp":{"svc":{"url":"placeholder"}}}`)},
			func(string) bool { return true },
		),
	}

	got, err := store.MCPFieldResolver("svc", "url").ResolveValue("$(echo trusted)")
	require.NoError(t, err)
	require.Equal(t, "trusted", got)
}

// TestDiscoveryFieldResolver_AnyUntrustedFieldFallsBackToEnvOnly pins
// that model discovery -- which resolves base_url/api_key/extra_headers
// through one shared resolver for a single HTTP request -- falls back
// to the env-only resolver if any one of those fields was last set by
// an untrusted layer, since a single outgoing request cannot honor two
// different trust levels at once.
func TestDiscoveryFieldResolver_AnyUntrustedFieldFallsBackToEnvOnly(t *testing.T) {
	t.Parallel()

	e := env.New()
	trust := computeDataFieldTrust(
		[]string{"/project/angela.json"},
		[][]byte{[]byte(`{"providers":{"custom":{"base_url":"placeholder"}}}`)},
		func(string) bool { return false },
	)

	resolver := discoveryFieldResolver(trust, NewShellVariableResolver(e), NewEnvOnlyVariableResolver(e), "custom")

	got, err := resolver.ResolveValue("$(echo pwned)")
	require.NoError(t, err)
	require.Equal(t, "$(echo pwned)", got)
}

// TestDiscoveryFieldResolver_AllTrustedUsesShellResolver pins the
// counterpart: when every field discovery touches was last set by a
// trusted layer, the shared resolver still runs "$(...)".
func TestDiscoveryFieldResolver_AllTrustedUsesShellResolver(t *testing.T) {
	t.Parallel()

	e := env.New()
	trust := computeDataFieldTrust(
		[]string{"/global/angela.json"},
		[][]byte{[]byte(`{"providers":{"custom":{"base_url":"placeholder","api_key":"placeholder"}}}`)},
		func(string) bool { return true },
	)

	resolver := discoveryFieldResolver(trust, NewShellVariableResolver(e), NewEnvOnlyVariableResolver(e), "custom")

	got, err := resolver.ResolveValue("$(echo trusted)")
	require.NoError(t, err)
	require.Equal(t, "trusted", got)
}
