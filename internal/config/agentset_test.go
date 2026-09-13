package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestAllowedAgentSet_JSONRoundTrip(t *testing.T) {
	type wrapper struct {
		AllowedAgents *AllowedAgentSet `json:"allowed_agents,omitempty"`
	}

	tests := []struct {
		name string
		json string
		want *AllowedAgentSet
	}{
		{"whitelist array", `{"allowed_agents":["explore","general"]}`, &AllowedAgentSet{Kind: ToolSetScope, Agents: []string{"explore", "general"}}},
		{"explicit empty array", `{"allowed_agents":[]}`, &AllowedAgentSet{Kind: ToolSetScope, Agents: []string{}}},
		{"all literal", `{"allowed_agents":"all"}`, &AllowedAgentSet{Kind: ToolSetAll}},
		{"field absent", `{}`, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w wrapper
			require.NoError(t, json.Unmarshal([]byte(tt.json), &w))
			require.Equal(t, tt.want, w.AllowedAgents)

			out, err := json.Marshal(w)
			require.NoError(t, err)
			require.JSONEq(t, tt.json, string(out))
		})
	}
}

func TestAllowedAgentSet_UnmarshalJSON_RejectsInvalidShapes(t *testing.T) {
	var s AllowedAgentSet
	require.Error(t, json.Unmarshal([]byte(`"everything"`), &s), "only the literal \"all\" is a valid string value")
	require.Error(t, json.Unmarshal([]byte(`"inherited"`), &s),
		"inherited implies a coder-anchored dispatch list, which does not exist for agent dispatch")
	require.Error(t, json.Unmarshal([]byte(`{"kind":0,"agents":["explore"]}`), &s), "the internal struct shape must not be accepted from the wire")
	require.Error(t, json.Unmarshal([]byte(`42`), &s))
}

func TestAllowedAgentSet_AllowsIsNilSafeAndFailsOpen(t *testing.T) {
	// Unlike AllowedToolSet, dispatch starts open: a nil set means no
	// layer ever restricted it, so every agent is allowed.
	var nilSet *AllowedAgentSet
	require.True(t, nilSet.Allows("explore"))
	require.True(t, nilSet.Allows("anything"))

	all := &AllowedAgentSet{Kind: ToolSetAll}
	require.True(t, all.Allows("explore"))

	empty := &AllowedAgentSet{Kind: ToolSetScope}
	require.False(t, empty.Allows("explore"), "an explicit empty scope must deny everything")

	scoped := &AllowedAgentSet{Kind: ToolSetScope, Agents: []string{"explore"}}
	require.True(t, scoped.Allows("explore"))
	require.False(t, scoped.Allows("general"))
}

func TestAllowedAgentSet_YAMLRoundTrip(t *testing.T) {
	t.Parallel()

	type wrapper struct {
		AllowedAgents *AllowedAgentSet `yaml:"allowed_agents,omitempty"`
	}

	var fromArray wrapper
	require.NoError(t, yaml.Unmarshal([]byte("allowed_agents: [explore]\n"), &fromArray))
	require.Equal(t, &AllowedAgentSet{Kind: ToolSetScope, Agents: []string{"explore"}}, fromArray.AllowedAgents)

	var fromAll wrapper
	require.NoError(t, yaml.Unmarshal([]byte("allowed_agents: all\n"), &fromAll))
	require.Equal(t, &AllowedAgentSet{Kind: ToolSetAll}, fromAll.AllowedAgents)

	require.Error(t, yaml.Unmarshal([]byte("allowed_agents: inherited\n"), new(wrapper)))
}

func TestAllowedAgentSet_Clone(t *testing.T) {
	t.Parallel()

	var nilSet *AllowedAgentSet
	require.Nil(t, nilSet.clone())

	original := &AllowedAgentSet{Kind: ToolSetScope, Agents: []string{"explore"}}
	cloned := original.clone()
	require.Equal(t, original, cloned)

	cloned.Agents[0] = "general"
	require.Equal(t, "explore", original.Agents[0], "the clone must not share the backing array")
}

// TestAllowedAgentSet_MarshalJSON_NilReceiver pins the one branch
// encoding/json can never reach through a struct field: the
// marshalerEncoder special-cases a nil pointer implementing
// json.Marshaler and writes "null" directly without calling
// MarshalJSON, so the nil check inside the method is only exercised
// by calling it directly.
func TestAllowedAgentSet_MarshalJSON_NilReceiver(t *testing.T) {
	var s *AllowedAgentSet
	b, err := s.MarshalJSON()
	require.NoError(t, err)
	require.Equal(t, "null", string(b))
}

// TestAllowedAgentSet_MarshalYAML exercises all three MarshalYAML
// branches directly: none of them are reached by
// TestAllowedAgentSet_YAMLRoundTrip, which only calls
// yaml.Unmarshal.
func TestAllowedAgentSet_MarshalYAML(t *testing.T) {
	var nilSet *AllowedAgentSet
	out, err := nilSet.MarshalYAML()
	require.NoError(t, err)
	require.Nil(t, out)

	all := &AllowedAgentSet{Kind: ToolSetAll}
	out, err = all.MarshalYAML()
	require.NoError(t, err)
	require.Equal(t, "all", out)

	scoped := &AllowedAgentSet{Kind: ToolSetScope, Agents: []string{"explore"}}
	out, err = scoped.MarshalYAML()
	require.NoError(t, err)
	require.Equal(t, []string{"explore"}, out)
}

func TestAllowedAgentSet_UnmarshalYAML_RejectsInvalidShape(t *testing.T) {
	type wrapper struct {
		AllowedAgents *AllowedAgentSet `yaml:"allowed_agents"`
	}
	var w wrapper
	err := yaml.Unmarshal([]byte("allowed_agents: 42\n"), &w)
	require.Error(t, err)

	// "42" decodes cleanly as a string literal (just not one of the
	// accepted keywords), so it never reaches the array-decode
	// branch below. Only a shape that fails both decodes, like a
	// mapping, does.
	var w2 wrapper
	err = yaml.Unmarshal([]byte("allowed_agents:\n  foo: bar\n"), &w2)
	require.Error(t, err)
	require.Contains(t, err.Error(), "expected an array of agent IDs")
}

func TestAllowedAgentSet_JSONSchema(t *testing.T) {
	schema := AllowedAgentSet{}.JSONSchema()
	require.NotNil(t, schema)
	require.Len(t, schema.OneOf, 2, "the scoped array shape plus the \"all\" literal")
}
