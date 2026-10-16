package config

import (
	"encoding/json"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/stretchr/testify/require"
)

func TestProviderModelJSON_WritesRenamedKeys(t *testing.T) {
	t.Parallel()

	out, err := json.Marshal(ProviderModel{Model: catwalk.Model{
		ID:                     "m",
		DefaultMaxTokens:       8192,
		DefaultReasoningEffort: "high",
	}})
	require.NoError(t, err)

	var keys map[string]any
	require.NoError(t, json.Unmarshal(out, &keys))
	require.EqualValues(t, 8192, keys["max_tokens"])
	require.Equal(t, "high", keys["reasoning_effort"])
	require.NotContains(t, keys, "default_max_tokens")
	require.NotContains(t, keys, "default_reasoning_effort")
}

func TestProviderModelJSON_OmitsEmptyReasoningEffort(t *testing.T) {
	t.Parallel()

	out, err := json.Marshal(ProviderModel{Model: catwalk.Model{ID: "m"}})
	require.NoError(t, err)
	require.NotContains(t, string(out), "reasoning_effort")
}

func TestProviderModelJSON_RoundTripKeepsEveryField(t *testing.T) {
	t.Parallel()

	useResponses := true
	effort := "low"
	want := ProviderModel{
		Model: catwalk.Model{
			ID:                     "m",
			Name:                   "M",
			ContextWindow:          200000,
			DefaultMaxTokens:       8192,
			CanReason:              true,
			ReasoningLevels:        []string{"low", "high"},
			DefaultReasoningEffort: "high",
			SupportsImages:         true,
		},
		Think:        true,
		UseResponses: &useResponses,
		Variants:     map[string]SelectedModelOverride{"quick": {ReasoningEffort: &effort}},
	}

	out, err := json.Marshal(want)
	require.NoError(t, err)

	var got ProviderModel
	require.NoError(t, json.Unmarshal(out, &got))
	require.Equal(t, want, got)
}

func TestProviderModelJSON_RejectsRenamedKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		message string
	}{
		{"max tokens", `{"id":"m","default_max_tokens":8192}`, "default_max_tokens was renamed to max_tokens"},
		{"reasoning effort", `{"id":"m","default_reasoning_effort":"high"}`, "default_reasoning_effort was renamed to reasoning_effort"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var m ProviderModel
			err := json.Unmarshal([]byte(tt.payload), &m)
			require.ErrorContains(t, err, tt.message)
			require.ErrorContains(t, err, `"m"`)
		})
	}
}

func TestProviderModelJSON_RenamedKeysFailTheWholeConfigLoad(t *testing.T) {
	t.Parallel()

	_, err := loadFromBytes([][]byte{[]byte(`{
		"providers": {"acme": {"models": [{"id": "m", "default_max_tokens": 8192}]}}
	}`)})
	require.ErrorContains(t, err, "default_max_tokens was renamed to max_tokens")
}

func TestProviderModelJSON_ReadsRenamedKeys(t *testing.T) {
	t.Parallel()

	var m ProviderModel
	require.NoError(t, json.Unmarshal([]byte(`{"id":"m","max_tokens":4096,"reasoning_effort":"low"}`), &m))
	require.Equal(t, int64(4096), m.DefaultMaxTokens)
	require.Equal(t, "low", m.DefaultReasoningEffort)
}

func TestProviderModelJSON_MissingKeysKeepExistingValues(t *testing.T) {
	t.Parallel()

	m := ProviderModel{Model: catwalk.Model{DefaultMaxTokens: 100, DefaultReasoningEffort: "low"}}
	require.NoError(t, json.Unmarshal([]byte(`{"id":"m"}`), &m))
	require.Equal(t, int64(100), m.DefaultMaxTokens)
	require.Equal(t, "low", m.DefaultReasoningEffort)
}
