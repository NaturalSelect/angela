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
