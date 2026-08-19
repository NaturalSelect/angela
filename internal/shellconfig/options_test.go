package shellconfig

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOption_Bool(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option debug true
option progress false`
	path := filepath.Join(dir, "angelarc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	opts := result["options"].(map[string]any)
	require.Equal(t, true, opts["debug"])
	require.Equal(t, false, opts["progress"])
}

func TestOption_BoolCaseInsensitive(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option debug TRUE
option progress False
option metrics YES`
	path := filepath.Join(dir, "angelarc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	opts := result["options"].(map[string]any)
	require.Equal(t, true, opts["debug"])
	require.Equal(t, false, opts["progress"])
	require.Equal(t, false, opts["disable_metrics"])
}

func TestOption_String(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option data-directory .angela
option notifications osc`
	path := filepath.Join(dir, "angelarc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	opts := result["options"].(map[string]any)
	require.Equal(t, ".angela", opts["data_directory"])
	require.Equal(t, "osc", opts["notifications"])
}

func TestOption_List(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option context-path .cursorrules
option context-path ANGELA.md`
	path := filepath.Join(dir, "angelarc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	opts := result["options"].(map[string]any)
	paths := opts["context_paths"].([]any)
	require.Len(t, paths, 2)
	require.Equal(t, ".cursorrules", paths[0])
	require.Equal(t, "ANGELA.md", paths[1])
}

func TestOption_Reset(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option skill-path ./a
option skill-path ./b
option reset skill-path`
	path := filepath.Join(dir, "angelarc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	opts := result["options"].(map[string]any)
	require.Empty(t, opts["skills_paths"].([]any))
}

func TestOption_ResetThenReadd(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option skill-path ./inherited-a
option skill-path ./inherited-b
option reset skill-path
option skill-path ./mine`
	path := filepath.Join(dir, "angelarc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	opts := result["options"].(map[string]any)
	paths := opts["skills_paths"].([]any)
	require.Len(t, paths, 1)
	require.Equal(t, "./mine", paths[0])
}

func TestOption_ResetUnknownKey(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option reset bogus-key`
	path := filepath.Join(dir, "angelarc")

	_, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown key")
}

func TestOption_ResetNonListKey(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option reset debug`
	path := filepath.Join(dir, "angelarc")

	_, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.Error(t, err)
	require.Contains(t, err.Error(), "not one")
}

func TestOption_UIUnknownKey(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "angelarc")
	_, err := LoadShellConfig(t.Context(), path, []byte(`option ui bogus true`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown key")
}

func TestOption_BoolShorthand(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option debug
option metrics`
	path := filepath.Join(dir, "angelarc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	opts := result["options"].(map[string]any)
	require.Equal(t, true, opts["debug"])
	require.Equal(t, false, opts["disable_metrics"])
}

func TestOption_InvertedBool(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option metrics false`
	path := filepath.Join(dir, "angelarc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	opts := result["options"].(map[string]any)
	require.Equal(t, true, opts["disable_metrics"])
}

func TestOption_UnknownKey(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option bogus-key value`
	path := filepath.Join(dir, "angelarc")

	_, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown key")
}

func TestOption_Int(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option subagent-depth 3`
	path := filepath.Join(dir, "angelarc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	opts := result["options"].(map[string]any)
	require.Equal(t, float64(3), opts["subagent_depth"])
}

func TestOption_IntZero(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	script := `option subagent-depth 0`
	path := filepath.Join(dir, "angelarc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	opts := result["options"].(map[string]any)
	require.Equal(t, float64(0), opts["subagent_depth"])
}

func TestOption_IntRejectsNegative(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "angelarc")
	_, err := LoadShellConfig(t.Context(), path, []byte(`option subagent-depth -1`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "non-negative integer")
}

func TestOption_IntRejectsNonNumeric(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "angelarc")
	_, err := LoadShellConfig(t.Context(), path, []byte(`option subagent-depth abc`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "non-negative integer")
}

func TestOption_IntRequiresValue(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "angelarc")
	_, err := LoadShellConfig(t.Context(), path, []byte(`option subagent-depth`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires a value")
}

func TestOption_ResetIntKeyRejected(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "angelarc")
	_, err := LoadShellConfig(t.Context(), path, []byte(`option reset subagent-depth`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "not one")
}
