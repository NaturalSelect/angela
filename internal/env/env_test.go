package env

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOsEnv_Get(t *testing.T) {
	env := &osEnv{}

	// Test getting an existing environment variable
	t.Setenv("TEST_VAR", "test_value")

	value := env.Get("TEST_VAR")
	require.Equal(t, "test_value", value)

	// Test getting a non-existent environment variable
	value = env.Get("NON_EXISTENT_VAR")
	require.Equal(t, "", value)
}

func TestOsEnv_Env(t *testing.T) {
	env := &osEnv{}

	envVars := env.Env()

	// Environment should not be empty in normal circumstances
	require.NotNil(t, envVars)
	require.Greater(t, len(envVars), 0)

	// Each environment variable should be in key=value format
	for _, envVar := range envVars {
		require.Contains(t, envVar, "=")
	}
}

func TestOsEnv_Get_AngelaPrefixOverride(t *testing.T) {
	t.Setenv("TEST_OVERRIDE_VAR", "from_bare")
	t.Setenv("ANGELA_TEST_OVERRIDE_VAR", "from_angela")

	env := &osEnv{}
	require.Equal(t, "from_angela", env.Get("TEST_OVERRIDE_VAR"))
}

func TestOsEnv_Env_AngelaPrefixOverride(t *testing.T) {
	t.Setenv("ANGELA_TEST_ENV_OVERRIDE_VAR", "from_angela")

	env := &osEnv{}
	envMap := toMap(t, env.Env())

	require.Equal(t, "from_angela", envMap["TEST_ENV_OVERRIDE_VAR"])
	require.Equal(t, "from_angela", envMap["ANGELA_TEST_ENV_OVERRIDE_VAR"])
}

func TestNewFromMap(t *testing.T) {
	testMap := map[string]string{
		"KEY1": "value1",
		"KEY2": "value2",
	}

	env := NewFromMap(testMap)
	require.NotNil(t, env)
	require.IsType(t, &mapEnv{}, env)
}

func TestMapEnv_Get(t *testing.T) {
	testMap := map[string]string{
		"KEY1": "value1",
		"KEY2": "value2",
	}

	env := NewFromMap(testMap)

	// Test getting existing keys
	require.Equal(t, "value1", env.Get("KEY1"))
	require.Equal(t, "value2", env.Get("KEY2"))

	// Test getting non-existent key
	require.Equal(t, "", env.Get("NON_EXISTENT"))
}

func TestMapEnv_Get_AngelaPrefixOverride(t *testing.T) {
	env := NewFromMap(map[string]string{
		"API_KEY":        "from_bare",
		"ANGELA_API_KEY": "from_angela",
	})

	require.Equal(t, "from_angela", env.Get("API_KEY"))
}

func TestMapEnv_Env_AngelaPrefixOverride(t *testing.T) {
	env := NewFromMap(map[string]string{
		"ANGELA_API_KEY": "from_angela",
	})

	envMap := toMap(t, env.Env())
	require.Equal(t, "from_angela", envMap["API_KEY"])
	require.Equal(t, "from_angela", envMap["ANGELA_API_KEY"])
}

func TestMapEnv_Env(t *testing.T) {
	t.Run("with values", func(t *testing.T) {
		testMap := map[string]string{
			"KEY1": "value1",
			"KEY2": "value2",
		}

		env := NewFromMap(testMap)
		envVars := env.Env()

		require.Len(t, envVars, 2)

		// Convert to map for easier testing (order is not guaranteed)
		envMap := make(map[string]string)
		for _, envVar := range envVars {
			parts := strings.SplitN(envVar, "=", 2)
			require.Len(t, parts, 2)
			envMap[parts[0]] = parts[1]
		}

		require.Equal(t, "value1", envMap["KEY1"])
		require.Equal(t, "value2", envMap["KEY2"])
	})

	t.Run("empty map", func(t *testing.T) {
		env := NewFromMap(map[string]string{})
		envVars := env.Env()
		require.NotNil(t, envVars)
		require.Len(t, envVars, 0)
	})

	t.Run("nil map", func(t *testing.T) {
		env := NewFromMap(nil)
		envVars := env.Env()
		require.NotNil(t, envVars)
		require.Len(t, envVars, 0)
	})
}

func TestMapEnv_GetEmptyValue(t *testing.T) {
	testMap := map[string]string{
		"EMPTY_KEY":  "",
		"NORMAL_KEY": "value",
	}

	env := NewFromMap(testMap)

	// Test that empty values are returned correctly
	require.Equal(t, "", env.Get("EMPTY_KEY"))
	require.Equal(t, "value", env.Get("NORMAL_KEY"))
}

func TestMapEnv_EnvFormat(t *testing.T) {
	testMap := map[string]string{
		"KEY_WITH_EQUALS": "value=with=equals",
		"KEY_WITH_SPACES": "value with spaces",
	}

	env := NewFromMap(testMap)
	envVars := env.Env()

	require.Len(t, envVars, 2)

	// Check that the format is correct even with special characters
	found := make(map[string]bool)
	for _, envVar := range envVars {
		if envVar == "KEY_WITH_EQUALS=value=with=equals" {
			found["equals"] = true
		}
		if envVar == "KEY_WITH_SPACES=value with spaces" {
			found["spaces"] = true
		}
	}

	require.True(t, found["equals"], "Should handle values with equals signs")
	require.True(t, found["spaces"], "Should handle values with spaces")
}

// toMap converts a "KEY=VALUE" pair list into a map for assertions,
// since Env() order is not guaranteed.
func toMap(t *testing.T, pairs []string) map[string]string {
	t.Helper()
	m := make(map[string]string, len(pairs))
	for _, kv := range pairs {
		name, value, ok := strings.Cut(kv, "=")
		require.True(t, ok, "malformed env pair %q", kv)
		m[name] = value
	}
	return m
}
