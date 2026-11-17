package env

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// NOTE: These tests change the real process environment, so they cannot
// run in parallel.

func TestValidateName(t *testing.T) {
	for _, name := range []string{"A", "_", "_A1", "api_key", "GITHUB_TOKEN2"} {
		require.NoError(t, ValidateName(name), name)
	}
	for _, name := range []string{"", "1A", "A-B", "A B", "A=B", "A.B", "ÄB", "A\x00"} {
		require.Error(t, ValidateName(name), name)
	}
}

func TestOverrides_SetIsVisibleToChildren(t *testing.T) {
	const name = "ANGELA_TEST_OVERRIDE_CHILD"
	t.Cleanup(func() { _ = os.Unsetenv(name) })

	var o Overrides
	require.NoError(t, o.Set(name, "s3cret"))

	require.Equal(t, "s3cret", os.Getenv(name))
	require.Contains(t, os.Environ(), name+"=s3cret")
	require.Equal(t, []string{name}, o.Names())
}

func TestOverrides_UnsetRemovesNewVariable(t *testing.T) {
	const name = "ANGELA_TEST_OVERRIDE_NEW"
	t.Cleanup(func() { _ = os.Unsetenv(name) })

	var o Overrides
	require.NoError(t, o.Set(name, "x"))
	require.NoError(t, o.Unset(name))

	_, ok := os.LookupEnv(name)
	require.False(t, ok)
	require.Empty(t, o.Names())
}

func TestOverrides_UnsetRestoresOriginal(t *testing.T) {
	const name = "ANGELA_TEST_OVERRIDE_EXISTING"
	t.Setenv(name, "original")

	var o Overrides
	require.NoError(t, o.Set(name, "first"))
	require.NoError(t, o.Set(name, "second"))
	require.Equal(t, "second", os.Getenv(name))
	require.Equal(t, []string{name}, o.Names())

	require.NoError(t, o.Unset(name))
	require.Equal(t, "original", os.Getenv(name))
	require.Empty(t, o.Names())
}

func TestOverrides_UnsetRestoresEmptyOriginal(t *testing.T) {
	const name = "ANGELA_TEST_OVERRIDE_EMPTY"
	t.Setenv(name, "")

	var o Overrides
	require.NoError(t, o.Set(name, "x"))
	require.NoError(t, o.Unset(name))

	value, ok := os.LookupEnv(name)
	require.True(t, ok)
	require.Empty(t, value)
}

func TestOverrides_UnsetLeavesForeignVariablesAlone(t *testing.T) {
	const name = "ANGELA_TEST_OVERRIDE_FOREIGN"
	t.Setenv(name, "mine")

	var o Overrides
	err := o.Unset(name)

	require.ErrorIs(t, err, ErrNotOverridden)
	require.Equal(t, "mine", os.Getenv(name))
}

func TestOverrides_SetRejectsInvalidInput(t *testing.T) {
	var o Overrides

	require.Error(t, o.Set("BAD-NAME", "x"))
	require.Error(t, o.Set("ANGELA_TEST_OVERRIDE_NUL", "a\x00b"))
	require.Empty(t, o.Names())
}

func TestOverrides_NamesAreSorted(t *testing.T) {
	names := []string{"ANGELA_TEST_OVERRIDE_C", "ANGELA_TEST_OVERRIDE_A", "ANGELA_TEST_OVERRIDE_B"}
	t.Cleanup(func() {
		for _, name := range names {
			_ = os.Unsetenv(name)
		}
	})

	var o Overrides
	for _, name := range names {
		require.NoError(t, o.Set(name, "x"))
	}

	require.Equal(t, []string{
		"ANGELA_TEST_OVERRIDE_A",
		"ANGELA_TEST_OVERRIDE_B",
		"ANGELA_TEST_OVERRIDE_C",
	}, o.Names())
}
