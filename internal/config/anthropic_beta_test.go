package config

import (
	"encoding/json"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/stretchr/testify/require"
)

func TestAnthropicBetaOverlay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		base *AnthropicBeta
		top  *AnthropicBeta
		want *AnthropicBeta
	}{
		{name: "both nil"},
		{
			name: "nil top keeps base",
			base: &AnthropicBeta{Enable: []string{"a"}, Disable: []string{"b"}},
			want: &AnthropicBeta{Enable: []string{"a"}, Disable: []string{"b"}},
		},
		{
			name: "nil base takes top",
			top:  &AnthropicBeta{Enable: []string{"a"}},
			want: &AnthropicBeta{Enable: []string{"a"}},
		},
		{
			name: "top enable overrides base disable",
			base: &AnthropicBeta{Disable: []string{"a", "b"}},
			top:  &AnthropicBeta{Enable: []string{"a"}},
			want: &AnthropicBeta{Enable: []string{"a"}, Disable: []string{"b"}},
		},
		{
			name: "top disable overrides base enable",
			base: &AnthropicBeta{Enable: []string{"a", "b"}},
			top:  &AnthropicBeta{Disable: []string{"a"}},
			want: &AnthropicBeta{Enable: []string{"b"}, Disable: []string{"a"}},
		},
		{
			name: "disable wins within one layer",
			top:  &AnthropicBeta{Enable: []string{"a"}, Disable: []string{"a"}},
			want: &AnthropicBeta{Disable: []string{"a"}},
		},
		{
			name: "values are trimmed and deduplicated",
			base: &AnthropicBeta{Enable: []string{" a ", "", "a"}},
			top:  &AnthropicBeta{Enable: []string{"b", "a", " b"}},
			want: &AnthropicBeta{Enable: []string{"a", "b"}},
		},
		{
			name: "empty result is nil",
			base: &AnthropicBeta{Enable: []string{" "}},
			top:  &AnthropicBeta{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, tt.base.Overlay(tt.top))
		})
	}
}

func TestAnthropicBetaOverlayLeavesInputsAlone(t *testing.T) {
	t.Parallel()

	base := &AnthropicBeta{Enable: []string{"a", "b"}, Disable: []string{"c"}}
	top := &AnthropicBeta{Enable: []string{"c"}, Disable: []string{"a"}}

	got := base.Overlay(top)
	require.Equal(t, &AnthropicBeta{Enable: []string{"b", "c"}, Disable: []string{"a"}}, got)
	require.Equal(t, &AnthropicBeta{Enable: []string{"a", "b"}, Disable: []string{"c"}}, base)
	require.Equal(t, &AnthropicBeta{Enable: []string{"c"}, Disable: []string{"a"}}, top)
}

func TestAnthropicBetaCloneSharesNothing(t *testing.T) {
	t.Parallel()

	orig := &AnthropicBeta{Enable: []string{"a"}, Disable: []string{"b"}}
	clone := orig.clone()
	clone.Enable[0] = "x"
	clone.Disable[0] = "y"

	require.Equal(t, &AnthropicBeta{Enable: []string{"a"}, Disable: []string{"b"}}, orig)
	require.Nil(t, (*AnthropicBeta)(nil).clone())
}

func TestVariantLayersAnthropicBetaOverModel(t *testing.T) {
	t.Parallel()

	base := ProviderModel{
		Model:         catwalk.Model{ID: "claude-opus-4"},
		AnthropicBeta: &AnthropicBeta{Enable: []string{"a", "b"}},
		Variants: map[string]SelectedModelOverride{
			"quiet": {AnthropicBeta: &AnthropicBeta{Enable: []string{"c"}, Disable: []string{"a"}}},
			"plain": {},
		},
	}

	got, ok := base.WithVariant("quiet")
	require.True(t, ok)
	require.Equal(t, &AnthropicBeta{Enable: []string{"b", "c"}, Disable: []string{"a"}}, got.AnthropicBeta)
	require.Equal(t, &AnthropicBeta{Enable: []string{"a", "b"}}, base.AnthropicBeta,
		"the baseline must stay untouched")

	got, ok = base.WithVariant("plain")
	require.True(t, ok)
	require.Equal(t, base.AnthropicBeta, got.AnthropicBeta,
		"a variant that does not name anthropic_beta keeps the model's")
}

func TestProviderModelCloneKeepsAnthropicBetaApart(t *testing.T) {
	t.Parallel()

	orig := ProviderModel{
		Model:         catwalk.Model{ID: "m"},
		AnthropicBeta: &AnthropicBeta{Enable: []string{"a"}},
		Variants: map[string]SelectedModelOverride{
			"v": {AnthropicBeta: &AnthropicBeta{Disable: []string{"b"}}},
		},
	}

	clone := orig.clone()
	clone.AnthropicBeta.Enable[0] = "x"
	clone.Variants["v"].AnthropicBeta.Disable[0] = "y"

	require.Equal(t, "a", orig.AnthropicBeta.Enable[0], "model anthropic_beta leaked")
	require.Equal(t, "b", orig.Variants["v"].AnthropicBeta.Disable[0], "variant anthropic_beta leaked")
}

func TestProviderModelJSON_AnthropicBetaRoundTrip(t *testing.T) {
	t.Parallel()

	want := ProviderModel{
		Model:         catwalk.Model{ID: "m"},
		AnthropicBeta: &AnthropicBeta{Enable: []string{"a"}, Disable: []string{"b"}},
		Variants: map[string]SelectedModelOverride{
			"v": {AnthropicBeta: &AnthropicBeta{Enable: []string{"c"}}},
		},
	}

	out, err := json.Marshal(want)
	require.NoError(t, err)
	require.Contains(t, string(out), `"anthropic_beta":{"enable":["a"],"disable":["b"]}`)

	var got ProviderModel
	require.NoError(t, json.Unmarshal(out, &got))
	require.Equal(t, want, got)
}

func TestWrapCatwalkModelsKeepsAnthropicBeta(t *testing.T) {
	t.Parallel()

	beta := &AnthropicBeta{Enable: []string{"a"}}
	existing := []ProviderModel{{Model: catwalk.Model{ID: "m"}, AnthropicBeta: beta}}

	got := wrapCatwalkModels([]catwalk.Model{{ID: "m"}, {ID: "other"}}, existing)
	require.Len(t, got, 2)
	require.Equal(t, beta, got[0].AnthropicBeta)
	require.Nil(t, got[1].AnthropicBeta)
}
