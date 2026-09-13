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
