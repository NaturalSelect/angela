package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestAllowedToolSet_JSONRoundTrip(t *testing.T) {
	type wrapper struct {
		AllowedTools *AllowedToolSet `json:"allowed_tools,omitempty"`
	}

	tests := []struct {
		name string
		json string
		want *AllowedToolSet
	}{
		{"whitelist array", `{"allowed_tools":["a","b"]}`, &AllowedToolSet{Kind: ToolSetScope, Tools: []string{"a", "b"}}},
		{"explicit empty array", `{"allowed_tools":[]}`, &AllowedToolSet{Kind: ToolSetScope, Tools: []string{}}},
		{"all literal", `{"allowed_tools":"all"}`, &AllowedToolSet{Kind: ToolSetAll}},
		{"field absent", `{}`, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w wrapper
			require.NoError(t, json.Unmarshal([]byte(tt.json), &w))
			require.Equal(t, tt.want, w.AllowedTools)

			out, err := json.Marshal(w)
			require.NoError(t, err)
			require.JSONEq(t, tt.json, string(out))
		})
	}
}

func TestAllowedToolSet_UnmarshalJSON_RejectsInvalidShapes(t *testing.T) {
	var s AllowedToolSet
	require.Error(t, json.Unmarshal([]byte(`"everything"`), &s), "only the literal \"all\" is a valid string value")
	require.Error(t, json.Unmarshal([]byte(`{"kind":0,"tools":["a"]}`), &s), "the internal struct shape must not be accepted from the wire")
	require.Error(t, json.Unmarshal([]byte(`42`), &s))
}

func TestAllowedToolSet_AllowsIsNilSafe(t *testing.T) {
	var nilSet *AllowedToolSet
	require.False(t, nilSet.Allows("bash"))

	all := &AllowedToolSet{Kind: ToolSetAll}
	require.True(t, all.Allows("bash"))
	require.True(t, all.Allows("anything"))

	empty := &AllowedToolSet{Kind: ToolSetScope}
	require.False(t, empty.Allows("bash"))

	scoped := &AllowedToolSet{Kind: ToolSetScope, Tools: []string{"view"}}
	require.True(t, scoped.Allows("view"))
	require.False(t, scoped.Allows("bash"))
}

func TestAllowedToolSet_Materialize(t *testing.T) {
	all := []string{"a", "b", "c"}

	t.Run("all with no deny expands to the full set", func(t *testing.T) {
		s := &AllowedToolSet{Kind: ToolSetAll}
		got := s.Materialize(all, nil, nil, nil)
		require.Equal(t, AllowedToolSet{Kind: ToolSetScope, Tools: all}, got)
	})

	t.Run("all with global deny", func(t *testing.T) {
		s := &AllowedToolSet{Kind: ToolSetAll}
		got := s.Materialize(all, nil, []string{"b"}, nil)
		require.Equal(t, ToolSetScope, got.Kind)
		require.Equal(t, []string{"a", "c"}, got.Tools)
	})

	t.Run("scope narrowed by agent deny", func(t *testing.T) {
		s := &AllowedToolSet{Kind: ToolSetScope, Tools: []string{"a", "b"}}
		got := s.Materialize(all, nil, nil, []string{"a"})
		require.Equal(t, []string{"b"}, got.Tools)
	})

	t.Run("global deny wins over an explicit whitelist", func(t *testing.T) {
		s := &AllowedToolSet{Kind: ToolSetScope, Tools: []string{"a", "b"}}
		got := s.Materialize(all, nil, []string{"a"}, nil)
		require.Equal(t, []string{"b"}, got.Tools, "global deny must not be re-enabled by a scoped whitelist")
	})

	t.Run("inherited takes the coder's resolved list", func(t *testing.T) {
		s := &AllowedToolSet{Kind: ToolSetInherited}
		got := s.Materialize(all, []string{"a", "b"}, nil, nil)
		require.Equal(t, []string{"a", "b"}, got.Tools, "inherited must not expand to the full set")
	})

	t.Run("inherited is still narrowed by the agent's own deny", func(t *testing.T) {
		s := &AllowedToolSet{Kind: ToolSetInherited}
		got := s.Materialize(all, []string{"a", "b"}, nil, []string{"a"})
		require.Equal(t, []string{"b"}, got.Tools)
	})

	t.Run("nil receiver resolves to no tools", func(t *testing.T) {
		var s *AllowedToolSet
		got := s.Materialize(all, all, nil, nil)
		require.Equal(t, ToolSetScope, got.Kind)
		require.Empty(t, got.Tools)
	})
}

func TestAllowedToolSet_InheritedRoundTrip(t *testing.T) {
	t.Parallel()

	type wrapper struct {
		AllowedTools *AllowedToolSet `json:"allowed_tools,omitempty"`
	}

	var w wrapper
	require.NoError(t, json.Unmarshal([]byte(`{"allowed_tools":"inherited"}`), &w))
	require.Equal(t, &AllowedToolSet{Kind: ToolSetInherited}, w.AllowedTools)

	out, err := json.Marshal(w)
	require.NoError(t, err)
	require.JSONEq(t, `{"allowed_tools":"inherited"}`, string(out))

	var fromYAML struct {
		AllowedTools *AllowedToolSet `yaml:"allowed_tools"`
	}
	require.NoError(t, yaml.Unmarshal([]byte("allowed_tools: inherited\n"), &fromYAML))
	require.Equal(t, &AllowedToolSet{Kind: ToolSetInherited}, fromYAML.AllowedTools)
}

func TestAllowedToolSet_UnresolvedInheritedIsFailClosed(t *testing.T) {
	t.Parallel()

	// Inherited only means something during resolution. A value that
	// reaches a permission check still carrying it has skipped
	// ResolveAgents, so it must not grant anything.
	inherited := &AllowedToolSet{Kind: ToolSetInherited}
	require.False(t, inherited.Allows("bash"))
	require.False(t, inherited.Allows("view"))
}

func TestAllowedMCPSet_Allows(t *testing.T) {
	t.Parallel()

	var nilSet *AllowedMCPSet
	require.False(t, nilSet.Allows("github", "create_issue"))

	all := &AllowedMCPSet{Kind: ToolSetAll}
	require.True(t, all.Allows("github", "create_issue"))

	inherited := &AllowedMCPSet{Kind: ToolSetInherited}
	require.False(t, inherited.Allows("github", "create_issue"),
		"an unresolved inherited set must be fail-closed")

	none := &AllowedMCPSet{Kind: ToolSetScope}
	require.False(t, none.Allows("github", "create_issue"),
		"an empty scope denies every MCP tool")

	wholeServer := &AllowedMCPSet{Kind: ToolSetScope, Servers: map[string][]string{"github": nil}}
	require.True(t, wholeServer.Allows("github", "create_issue"))
	require.False(t, wholeServer.Allows("gitlab", "create_issue"))

	scoped := &AllowedMCPSet{Kind: ToolSetScope, Servers: map[string][]string{"github": {"create_issue"}}}
	require.True(t, scoped.Allows("github", "create_issue"))
	require.False(t, scoped.Allows("github", "delete_repo"))
}

func TestAllowedMCPSet_Materialize(t *testing.T) {
	t.Parallel()

	coder := &AllowedMCPSet{Kind: ToolSetScope, Servers: map[string][]string{"github": {"create_issue"}}}

	t.Run("inherited copies the coder's set", func(t *testing.T) {
		t.Parallel()
		s := &AllowedMCPSet{Kind: ToolSetInherited}
		got := s.Materialize(coder)
		require.Equal(t, ToolSetScope, got.Kind)
		require.Equal(t, map[string][]string{"github": {"create_issue"}}, got.Servers)
	})

	t.Run("inherited does not alias the coder's map", func(t *testing.T) {
		t.Parallel()
		s := &AllowedMCPSet{Kind: ToolSetInherited}
		got := s.Materialize(coder)
		got.Servers["gitlab"] = nil
		require.NotContains(t, coder.Servers, "gitlab",
			"mutating a resolved agent must not widen the coder's grant")
	})

	t.Run("all stays all", func(t *testing.T) {
		t.Parallel()
		s := &AllowedMCPSet{Kind: ToolSetAll}
		require.Equal(t, AllowedMCPSet{Kind: ToolSetAll}, s.Materialize(coder))
	})

	t.Run("nil receiver resolves to an empty scope", func(t *testing.T) {
		t.Parallel()
		var s *AllowedMCPSet
		got := s.Materialize(coder)
		require.Equal(t, ToolSetScope, got.Kind)
		require.Empty(t, got.Servers)
	})
}

func TestAllowedMCPSet_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	type wrapper struct {
		AllowedMCP *AllowedMCPSet `json:"allowed_mcp,omitempty"`
	}

	tests := []struct {
		name string
		json string
		want *AllowedMCPSet
	}{
		{"server object", `{"allowed_mcp":{"github":["create_issue"]}}`, &AllowedMCPSet{Kind: ToolSetScope, Servers: map[string][]string{"github": {"create_issue"}}}},
		{"empty object denies all", `{"allowed_mcp":{}}`, &AllowedMCPSet{Kind: ToolSetScope, Servers: map[string][]string{}}},
		{"all literal", `{"allowed_mcp":"all"}`, &AllowedMCPSet{Kind: ToolSetAll}},
		{"inherited literal", `{"allowed_mcp":"inherited"}`, &AllowedMCPSet{Kind: ToolSetInherited}},
		{"field absent", `{}`, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var w wrapper
			require.NoError(t, json.Unmarshal([]byte(tt.json), &w))
			require.Equal(t, tt.want, w.AllowedMCP)

			out, err := json.Marshal(w)
			require.NoError(t, err)
			require.JSONEq(t, tt.json, string(out))
		})
	}
}

func TestAllowedMCPSet_YAMLRoundTrip(t *testing.T) {
	t.Parallel()

	var scoped struct {
		AllowedMCP *AllowedMCPSet `yaml:"allowed_mcp"`
	}
	require.NoError(t, yaml.Unmarshal([]byte("allowed_mcp:\n  github:\n    - create_issue\n"), &scoped))
	require.Equal(t, &AllowedMCPSet{Kind: ToolSetScope, Servers: map[string][]string{"github": {"create_issue"}}}, scoped.AllowedMCP)

	var literal struct {
		AllowedMCP *AllowedMCPSet `yaml:"allowed_mcp"`
	}
	require.NoError(t, yaml.Unmarshal([]byte("allowed_mcp: all\n"), &literal))
	require.Equal(t, &AllowedMCPSet{Kind: ToolSetAll}, literal.AllowedMCP)
}

func TestAllowedSets_RejectUnknownLiterals(t *testing.T) {
	t.Parallel()

	var tools AllowedToolSet
	require.Error(t, json.Unmarshal([]byte(`"everything"`), &tools))
	require.Error(t, yaml.Unmarshal([]byte("inherit\n"), &tools),
		"a near-miss of \"inherited\" must not be silently accepted")

	var mcp AllowedMCPSet
	require.Error(t, json.Unmarshal([]byte(`"everything"`), &mcp))
	require.Error(t, json.Unmarshal([]byte(`["github"]`), &mcp),
		"an array is not a valid allowed_mcp shape")
}
