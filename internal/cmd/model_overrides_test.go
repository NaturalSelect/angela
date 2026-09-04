package cmd

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

func overrideProviders() map[string]config.ProviderConfig {
	return map[string]config.ProviderConfig{
		"openai": {
			ID: "openai",
			Models: []config.ProviderModel{
				{Model: catwalk.Model{ID: "gpt-4o"}},
				{Model: catwalk.Model{ID: "gpt-4o-mini"}},
			},
		},
	}
}

// TestABadMainModelResolvesNothing is B8. The chore write is persisted
// server-side, so resolving and applying it before the main flag was
// checked left a rejected `angela run` with a permanently changed
// config: the prompt never ran, but the chore model stayed changed.
func TestABadMainModelResolvesNothing(t *testing.T) {
	t.Parallel()

	small, large, err := resolveModelOverrides(overrideProviders(), "no-such-model", "gpt-4o-mini")

	require.Error(t, err)
	require.Nil(t, small, "a rejected run must not carry a chore model to apply")
	require.Nil(t, large)
}

func TestBothModelsResolveTogether(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		large, small      string
		wantLarge         string
		wantSmall         string
		wantErrorContains string
	}{
		{
			name:  "both flags",
			large: "gpt-4o", small: "gpt-4o-mini",
			wantLarge: "gpt-4o", wantSmall: "gpt-4o-mini",
		},
		{
			name:  "only the main flag",
			large: "gpt-4o", wantLarge: "gpt-4o",
		},
		{
			name:  "only the chore flag",
			small: "gpt-4o-mini", wantSmall: "gpt-4o-mini",
		},
		{
			name: "neither flag",
		},
		{
			name:  "a bad chore model rejects the run",
			large: "gpt-4o", small: "no-such-model",
			wantErrorContains: "no-such-model",
		},
		{
			name:  "a provider-qualified name resolves",
			large: "openai/gpt-4o", wantLarge: "gpt-4o",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			small, large, err := resolveModelOverrides(overrideProviders(), tt.large, tt.small)

			if tt.wantErrorContains != "" {
				require.ErrorContains(t, err, tt.wantErrorContains)
				require.Nil(t, small)
				require.Nil(t, large)
				return
			}
			require.NoError(t, err)

			if tt.wantSmall == "" {
				require.Nil(t, small)
			} else {
				require.NotNil(t, small)
				require.Equal(t, tt.wantSmall, small.modelID)
			}
			if tt.wantLarge == "" {
				require.Nil(t, large)
			} else {
				require.NotNil(t, large)
				require.Equal(t, tt.wantLarge, large.modelID)
			}
		})
	}
}

// TestValidateModelMatches covers all three branches directly: no
// match, exactly one match, and an ambiguous match across multiple
// providers that must name every candidate provider in the error.
func TestValidateModelMatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		matches           []modelMatch
		wantMatch         modelMatch
		wantErrorContains []string
	}{
		{
			name:              "no matches",
			matches:           nil,
			wantErrorContains: []string{"main", `"gpt-4o"`, "not found"},
		},
		{
			name:      "exactly one match",
			matches:   []modelMatch{{provider: "openai", modelID: "gpt-4o"}},
			wantMatch: modelMatch{provider: "openai", modelID: "gpt-4o"},
		},
		{
			name: "ambiguous match names every provider",
			matches: []modelMatch{
				{provider: "openai", modelID: "gpt-4o"},
				{provider: "azure", modelID: "gpt-4o"},
			},
			wantErrorContains: []string{
				"main", `"gpt-4o"`, "multiple providers",
				"openai, azure", "provider/model",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := validateModelMatches(tt.matches, "gpt-4o", "main")

			if len(tt.wantErrorContains) > 0 {
				require.Error(t, err)
				for _, want := range tt.wantErrorContains {
					require.Contains(t, err.Error(), want)
				}
				require.Equal(t, modelMatch{}, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantMatch, got)
		})
	}
}

// TestMatchesModel covers every branch directly: an empty target ID
// never matches, a provider-qualified filter rejects a same-named model
// from a different provider, an unqualified filter matches under any
// provider, and matching is case-insensitive.
func TestMatchesModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                  string
		wantID, wantProvider  string
		modelID, providerName string
		want                  bool
	}{
		{name: "empty wanted id never matches", wantID: "", modelID: "gpt-4o", providerName: "openai", want: false},
		{name: "unqualified filter matches any provider", wantID: "gpt-4o", modelID: "gpt-4o", providerName: "azure", want: true},
		{name: "qualified filter matches its own provider", wantID: "gpt-4o", wantProvider: "openai", modelID: "gpt-4o", providerName: "openai", want: true},
		{name: "qualified filter rejects a different provider", wantID: "gpt-4o", wantProvider: "openai", modelID: "gpt-4o", providerName: "azure", want: false},
		{name: "match is case-insensitive", wantID: "GPT-4O", modelID: "gpt-4o", providerName: "openai", want: true},
		{name: "model id mismatch", wantID: "gpt-4o", modelID: "gpt-4o-mini", providerName: "openai", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := matchesModel(tt.wantID, tt.wantProvider, tt.modelID, tt.providerName)
			require.Equal(t, tt.want, got)
		})
	}
}
