package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/invopop/jsonschema"
	"github.com/stretchr/testify/require"
)

func TestSchemaNoBrokenRefs(t *testing.T) {
	t.Parallel()

	reflector := new(jsonschema.Reflector)
	bts, err := json.Marshal(reflector.Reflect(&config.Config{}))
	require.NoError(t, err)

	var schema struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	require.NoError(t, json.Unmarshal(bts, &schema))
	require.NotEmpty(t, schema.Defs, "schema should have definitions")

	for name := range schema.Defs {
		require.NotContains(t, name, "/", "schema $def key %q contains '/' which breaks JSON Pointer $ref resolution", name)
	}
}

func TestSchemaProvidersHasAdditionalProperties(t *testing.T) {
	t.Parallel()

	reflector := new(jsonschema.Reflector)
	bts, err := json.Marshal(reflector.Reflect(&config.Config{}))
	require.NoError(t, err)

	var schema struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	require.NoError(t, json.Unmarshal(bts, &schema))

	var cfg struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(schema.Defs["Config"], &cfg))

	providersRaw, ok := cfg.Properties["providers"]
	require.True(t, ok, "Config should have a providers property")

	var providers struct {
		Type                 string          `json:"type"`
		AdditionalProperties json.RawMessage `json:"additionalProperties"`
	}
	require.NoError(t, json.Unmarshal(providersRaw, &providers))
	require.Equal(t, "object", providers.Type)
	require.True(t, strings.Contains(string(providers.AdditionalProperties), "ProviderConfig"),
		"providers should use additionalProperties with a ProviderConfig ref, got: %s", string(providers.AdditionalProperties))
}

// reflectSchemaDefs returns the $defs of a freshly reflected Config, so
// shape assertions read the types as they are now rather than whatever
// schema.json was last generated from.
func reflectSchemaDefs(t *testing.T) map[string]json.RawMessage {
	t.Helper()

	reflector := new(jsonschema.Reflector)
	bts, err := json.Marshal(reflector.Reflect(&config.Config{}))
	require.NoError(t, err)

	var schema struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	require.NoError(t, json.Unmarshal(bts, &schema))
	return schema.Defs
}

// TestSchemaIncludesAgents pins what the committed schema.json was
// missing entirely: editors validating angela.json had no idea the
// agents section or agent_paths existed, so every key in them showed as
// an error.
func TestSchemaIncludesAgents(t *testing.T) {
	t.Parallel()

	defs := reflectSchemaDefs(t)
	require.Contains(t, defs, "Agent")

	var cfg struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(defs["Config"], &cfg))
	require.Contains(t, cfg.Properties, "agents")

	var options struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(defs["Options"], &options))
	require.Contains(t, options.Properties, "agent_paths")
}

// TestSchemaAgentModeHasNoAll guards the enum against the removed mode
// reappearing: an editor still offering "all" would hand users a value
// the loader now rejects.
func TestSchemaAgentModeHasNoAll(t *testing.T) {
	t.Parallel()

	var agent struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(reflectSchemaDefs(t)["Agent"], &agent))
	require.ElementsMatch(t, []string{"primary", "subagent", "branch"}, agent.Properties["mode"].Enum)
	require.NotContains(t, agent.Properties["mode"].Enum, "all")
}

// TestSchemaAllowedSetsAcceptLiterals pins the tri-state unions. A plain
// array type would make an editor flag the perfectly valid
// `"allowed_tools": "inherited"`.
func TestSchemaAllowedSetsAcceptLiterals(t *testing.T) {
	t.Parallel()

	defs := reflectSchemaDefs(t)

	for _, tc := range []struct {
		def       string
		firstType string
	}{
		{"AllowedToolSet", "array"},
		{"AllowedMCPSet", "object"},
	} {
		t.Run(tc.def, func(t *testing.T) {
			t.Parallel()
			var union struct {
				OneOf []struct {
					Type  string `json:"type"`
					Const string `json:"const"`
				} `json:"oneOf"`
			}
			require.NoError(t, json.Unmarshal(defs[tc.def], &union))
			require.Len(t, union.OneOf, 3)

			var types, consts []string
			for _, alt := range union.OneOf {
				if alt.Type != "" {
					types = append(types, alt.Type)
				}
				if alt.Const != "" {
					consts = append(consts, alt.Const)
				}
			}
			require.Equal(t, []string{tc.firstType}, types)
			require.ElementsMatch(t, []string{"all", "inherited"}, consts)
		})
	}
}
