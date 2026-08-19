package shellconfig

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func loadScript(t *testing.T, script string) map[string]any {
	t.Helper()
	path := filepath.Join(t.TempDir(), "angelarc")
	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))
	return result
}

func TestModelAdd(t *testing.T) {
	t.Parallel()

	result := loadScript(t, `provider add openai --api-key k
model add openai/gpt-5.6-sol --name "GPT 5.6 Sol" --context-window 200000 --can-reason true`)

	providers := result["providers"].(map[string]any)
	openai := providers["openai"].(map[string]any)
	models := openai["models"].([]any)
	require.Len(t, models, 1)
	m := models[0].(map[string]any)
	require.Equal(t, "gpt-5.6-sol", m["id"])
	require.Equal(t, "GPT 5.6 Sol", m["name"])
	require.Equal(t, float64(200000), m["context_window"])
	require.Equal(t, true, m["can_reason"])
}

// TestModelAddReplacesDuplicateID verifies that re-adding a model id updates
// the existing entry in place rather than appending a duplicate, matching the
// update-in-place behavior of `provider add` and `lsp add`.
func TestModelAddReplacesDuplicateID(t *testing.T) {
	t.Parallel()

	result := loadScript(t, `provider add openai --api-key k
model add openai/gpt-x --name "first"
model add openai/gpt-x --name "second"`)

	models := result["providers"].(map[string]any)["openai"].(map[string]any)["models"].([]any)
	require.Len(t, models, 1, "re-adding a model id must not create a duplicate")
	require.Equal(t, "second", models[0].(map[string]any)["name"])
}

func TestModelAddPricingFlags(t *testing.T) {
	t.Parallel()

	result := loadScript(t, `provider add anthropic --api-key k
model add anthropic/claude-x --price-input 3 --price-output 15 --price-cache-create 3.75 --price-cache-hit 0.3`)

	model := result["providers"].(map[string]any)["anthropic"].(map[string]any)["models"].([]any)[0].(map[string]any)
	require.Equal(t, 3.0, model["cost_per_1m_in"])
	require.Equal(t, 15.0, model["cost_per_1m_out"])
	require.Equal(t, 3.75, model["cost_per_1m_out_cached"])
	require.Equal(t, 0.3, model["cost_per_1m_in_cached"])
}

// TestModelAddReasoningLevels verifies --reasoning-level is repeatable and
// accumulates into the reasoning_levels array, mirroring --filetypes on
// `lsp add`. A custom model's declared levels gate whether
// --reasoning-effort ever reaches the provider (see effectiveReasoningEffort
// in internal/agent/coordinator.go), so this is the only way to make
// --reasoning-effort do anything for a provider outside the built-in
// catalog.
func TestModelAddReasoningLevels(t *testing.T) {
	t.Parallel()

	result := loadScript(t, `provider add openai --api-key k
model add openai/gpt-x --can-reason true --reasoning-level low --reasoning-level medium --reasoning-level high --reasoning-level max --reasoning-effort max`)

	model := result["providers"].(map[string]any)["openai"].(map[string]any)["models"].([]any)[0].(map[string]any)
	levels := model["reasoning_levels"].([]any)
	require.Len(t, levels, 4)
	require.Equal(t, []any{"low", "medium", "high", "max"}, levels)
	require.Equal(t, "max", model["default_reasoning_effort"])
}

func TestModelAddRejectsLegacyPricingFlags(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "angelarc")
	_, err := LoadShellConfig(t.Context(), path, []byte(`provider add openai --api-key k
model add openai/gpt-x --cost-per-1m-in 1`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown flag")
}

func TestModelSelectRejectsInvalidTopP(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "angelarc")
	_, err := LoadShellConfig(t.Context(), path, []byte(`model main openai/gpt-x --top-p 1.5`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "between 0 and 1")
}

func TestModelSelectRejectsNonObjectProviderOptions(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "angelarc")
	_, err := LoadShellConfig(t.Context(), path, []byte(`model main openai/gpt-x --provider-options '[]'`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "expects a JSON object")
}

func TestModelAddUnknownProvider(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "angelarc")
	_, err := LoadShellConfig(t.Context(), path, []byte(`model add openai/gpt-5.6-sol --name "x"`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not exist")
}

func TestModelAddNoSlash(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "angelarc")
	_, err := LoadShellConfig(t.Context(), path, []byte(`provider add openai --api-key k
model add gpt-5.6-sol --name "x"`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "<provider>/<id>")
}

func TestModelAddSlashInID(t *testing.T) {
	t.Parallel()

	result := loadScript(t, `provider add openrouter --api-key k
model add openrouter/anthropic/claude --name "Claude via OR"`)

	providers := result["providers"].(map[string]any)
	models := providers["openrouter"].(map[string]any)["models"].([]any)
	require.Equal(t, "anthropic/claude", models[0].(map[string]any)["id"])
}

func TestModelUnset(t *testing.T) {
	t.Parallel()

	result := loadScript(t, `provider add openai --api-key k
model add openai/a --name "A"
model add openai/b --name "B"
model remove openai/a`)

	models := result["providers"].(map[string]any)["openai"].(map[string]any)["models"].([]any)
	require.Len(t, models, 1)
	require.Equal(t, "b", models[0].(map[string]any)["id"])
}

func TestModelLargeSmall(t *testing.T) {
	t.Parallel()

	result := loadScript(t, `model main openai/gpt-4o --think
model chore anthropic/claude-3-5-haiku`)

	models := result["models"].(map[string]any)
	large := models["main"].(map[string]any)
	require.Equal(t, "openai", large["provider"])
	require.Equal(t, "gpt-4o", large["model"])
	require.Equal(t, true, large["think"])

	small := models["chore"].(map[string]any)
	require.Equal(t, "anthropic", small["provider"])
	require.Equal(t, "claude-3-5-haiku", small["model"])
}

// TestModelLargePrint verifies that `model large` with no argument prints the
// current selection, capturable via command substitution.
func TestModelLargePrint(t *testing.T) {
	t.Parallel()

	result := loadScript(t, `model main openai/gpt-4o
option data-directory "$(model main)"`)

	require.Equal(t, "openai/gpt-4o", result["options"].(map[string]any)["data_directory"])
}

func TestProviderUnset(t *testing.T) {
	t.Parallel()

	result := loadScript(t, `provider add openai --api-key k
provider add anthropic --api-key k
provider remove openai`)

	providers := result["providers"].(map[string]any)
	require.NotContains(t, providers, "openai")
	require.Contains(t, providers, "anthropic")
}

// TestRemoveRmAlias verifies that "rm" works as an alias for "remove" on both
// provider and model.
func TestRemoveRmAlias(t *testing.T) {
	t.Parallel()

	result := loadScript(t, `provider add openai --api-key k
provider add anthropic --api-key k
model add openai/a --name "A"
model add openai/b --name "B"
model rm openai/a
provider rm anthropic`)

	providers := result["providers"].(map[string]any)
	require.NotContains(t, providers, "anthropic")
	models := providers["openai"].(map[string]any)["models"].([]any)
	require.Len(t, models, 1)
	require.Equal(t, "b", models[0].(map[string]any)["id"])
}

// TestModelVariantDeclaresPreset pins the angelarc surface for
// variants: the preset lands under the model config it belongs to and
// carries only the keys it named.
func TestModelVariantDeclaresPreset(t *testing.T) {
	t.Parallel()

	result := loadScript(t, `model main anthropic/claude-opus-4 --reasoning-effort medium --think
model main variant deep --reasoning-effort high --max-tokens 32000
model main variant quick --reasoning-effort low --think false`)

	main := result["models"].(map[string]any)["main"].(map[string]any)
	require.Equal(t, "medium", main["reasoning_effort"], "the baseline is untouched")
	require.Equal(t, true, main["think"])

	variants := main["variants"].(map[string]any)
	deep := variants["deep"].(map[string]any)
	require.Equal(t, "high", deep["reasoning_effort"])
	require.Equal(t, float64(32000), deep["max_tokens"])
	require.NotContains(t, deep, "think", "an unnamed key stays absent so the baseline survives")

	quick := variants["quick"].(map[string]any)
	require.Equal(t, false, quick["think"], "a variant can turn off what the baseline turned on")
}

func TestModelVariantRequiresAName(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "angelarc")
	_, err := LoadShellConfig(t.Context(), path, []byte(`model main variant`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "usage: model main variant")
}
