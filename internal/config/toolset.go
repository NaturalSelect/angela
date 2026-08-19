package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/invopop/jsonschema"
	"gopkg.in/yaml.v3"
)

// ToolSetKind tags the three ways an agent's tool or MCP access can be
// described by a config layer.
type ToolSetKind uint8

const (
	// ToolSetScope is the zero value: the explicit set is the
	// whitelist. An empty set means nothing is allowed.
	ToolSetScope ToolSetKind = iota
	// ToolSetAll means everything is allowed.
	ToolSetAll
	// ToolSetInherited means the set is taken from the coder agent's
	// resolved set. It never survives resolution: ResolveAgents
	// expands it into a concrete ToolSetScope.
	ToolSetInherited
)

// Wire-format string values shared by AllowedToolSet and AllowedMCPSet.
const (
	allowedSetAllLiteral       = "all"
	allowedSetInheritedLiteral = "inherited"
)

// kindFromLiteral maps a wire string to its kind. It rejects anything
// that is neither "all" nor "inherited" so a typo can never be read as
// a broader grant than intended.
func kindFromLiteral(field, literal string) (ToolSetKind, error) {
	switch literal {
	case allowedSetAllLiteral:
		return ToolSetAll, nil
	case allowedSetInheritedLiteral:
		return ToolSetInherited, nil
	default:
		return 0, fmt.Errorf("%s: unsupported string value %q, only %q and %q are valid",
			field, literal, allowedSetAllLiteral, allowedSetInheritedLiteral)
	}
}

// literalFromKind is the inverse of kindFromLiteral for the two kinds
// that serialize as a string.
func literalFromKind(k ToolSetKind) string {
	if k == ToolSetAll {
		return allowedSetAllLiteral
	}
	return allowedSetInheritedLiteral
}

// unionSchema builds the JSON schema for a tri-state set whose scoped
// form has the given shape.
func unionSchema(scoped *jsonschema.Schema) *jsonschema.Schema {
	return &jsonschema.Schema{
		OneOf: []*jsonschema.Schema{
			scoped,
			{Const: allowedSetAllLiteral},
			{Const: allowedSetInheritedLiteral},
		},
	}
}

// AllowedToolSet is the value of Agent.AllowedTools. A nil
// *AllowedToolSet means "this layer did not mention allowed_tools" —
// mergeAgent relies on that nil check the same way it does for
// Disabled and Temperature. A non-nil value is self-describing: Kind
// says whether it grants every tool, inherits the coder's set, or
// grants just Tools.
//
// On the wire (JSON/YAML) it round-trips through the shapes users
// already write: a bare array (["bash","view"]) decodes to
// ToolSetScope, the strings "all" and "inherited" decode to their
// kinds, and an absent field decodes to a nil pointer. ResolveAgents
// is the only place allowed to produce a resolved value: its output
// always has Kind == ToolSetScope, with All and Inherited already
// expanded into a concrete list, so a global deny can never be
// re-enabled by a higher-priority layer's allowed_tools.
type AllowedToolSet struct {
	Kind  ToolSetKind
	Tools []string
}

// Allows reports whether name is permitted by s. A nil s, and an
// unresolved ToolSetInherited, both return false: a config.Agent
// reaching this call without having gone through ResolveAgents is not
// a resolved whitelist and must be treated as fail-closed.
func (s *AllowedToolSet) Allows(name string) bool {
	if s == nil {
		return false
	}
	if s.Kind == ToolSetAll {
		return true
	}
	if s.Kind == ToolSetInherited {
		return false
	}
	return slices.Contains(s.Tools, name)
}

// Materialize expands s into a concrete whitelist: ToolSetAll becomes
// every name in all, ToolSetInherited becomes inherited (the coder's
// already-resolved list), then globalDisabled and agentDisabled are
// removed in that order, so neither can be re-enabled by a
// higher-priority layer's allowed_tools. A nil s resolves to no tools,
// matching Allows' fail-closed default.
func (s *AllowedToolSet) Materialize(all, inherited, globalDisabled, agentDisabled []string) AllowedToolSet {
	var tools []string
	switch {
	case s == nil:
		tools = nil
	case s.Kind == ToolSetAll:
		tools = all
	case s.Kind == ToolSetInherited:
		tools = inherited
	default:
		tools = s.Tools
	}
	tools = filterSlice(tools, globalDisabled, false)
	tools = filterSlice(tools, agentDisabled, false)
	return AllowedToolSet{Kind: ToolSetScope, Tools: tools}
}

// MarshalJSON implements json.Marshaler.
func (s *AllowedToolSet) MarshalJSON() ([]byte, error) {
	if s == nil {
		return []byte("null"), nil
	}
	if s.Kind != ToolSetScope {
		return json.Marshal(literalFromKind(s.Kind))
	}
	return json.Marshal(s.Tools)
}

// UnmarshalJSON implements json.Unmarshaler.
func (s *AllowedToolSet) UnmarshalJSON(data []byte) error {
	var literal string
	if err := json.Unmarshal(data, &literal); err == nil {
		kind, err := kindFromLiteral("allowed_tools", literal)
		if err != nil {
			return err
		}
		*s = AllowedToolSet{Kind: kind}
		return nil
	}
	var tools []string
	if err := json.Unmarshal(data, &tools); err != nil {
		return fmt.Errorf("allowed_tools: expected an array of tool names, %q or %q: %w",
			allowedSetAllLiteral, allowedSetInheritedLiteral, err)
	}
	*s = AllowedToolSet{Kind: ToolSetScope, Tools: tools}
	return nil
}

// MarshalYAML implements yaml.Marshaler.
func (s *AllowedToolSet) MarshalYAML() (any, error) {
	if s == nil {
		return nil, nil
	}
	if s.Kind != ToolSetScope {
		return literalFromKind(s.Kind), nil
	}
	return s.Tools, nil
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (s *AllowedToolSet) UnmarshalYAML(value *yaml.Node) error {
	var literal string
	if err := value.Decode(&literal); err == nil {
		kind, err := kindFromLiteral("allowed_tools", literal)
		if err != nil {
			return err
		}
		*s = AllowedToolSet{Kind: kind}
		return nil
	}
	var tools []string
	if err := value.Decode(&tools); err != nil {
		return fmt.Errorf("allowed_tools: expected an array of tool names, %q or %q: %w",
			allowedSetAllLiteral, allowedSetInheritedLiteral, err)
	}
	*s = AllowedToolSet{Kind: ToolSetScope, Tools: tools}
	return nil
}

// JSONSchema reports the wire shape for JSON schema generation.
// AllowedToolSet's exported fields (Kind, Tools) are an internal
// representation, not what's read from or written to config files;
// the schema describes the three accepted shapes instead.
func (AllowedToolSet) JSONSchema() *jsonschema.Schema {
	return unionSchema(&jsonschema.Schema{
		Type:  "array",
		Items: &jsonschema.Schema{Type: "string"},
	})
}

// AllowedMCPSet is the value of Agent.AllowedMCP, the MCP counterpart
// of AllowedToolSet. ToolSetScope uses Servers as the whitelist: a key
// grants that server, and its value narrows the grant to the listed
// tool names (an empty or nil value grants every tool on that server).
// An empty Servers map therefore denies every MCP tool, which is what
// the read-only built-in agents want.
//
// On the wire it is an object ({"github": ["create_issue"]}) or one of
// the strings "all" / "inherited". Like AllowedToolSet, a resolved
// value never keeps ToolSetInherited.
type AllowedMCPSet struct {
	Kind    ToolSetKind
	Servers map[string][]string
}

// Allows reports whether the given MCP server's tool is permitted. A
// nil receiver and an unresolved ToolSetInherited are fail-closed, for
// the same reason as AllowedToolSet.Allows.
func (s *AllowedMCPSet) Allows(server, tool string) bool {
	if s == nil {
		return false
	}
	if s.Kind == ToolSetAll {
		return true
	}
	if s.Kind == ToolSetInherited {
		return false
	}
	allowedTools, ok := s.Servers[server]
	if !ok {
		return false
	}
	return len(allowedTools) == 0 || slices.Contains(allowedTools, tool)
}

// Materialize expands s into a concrete grant: ToolSetInherited
// becomes the coder's already-resolved set, everything else is kept as
// is. There is no MCP deny list, so the result is either ToolSetAll or
// a ToolSetScope carrying an explicit server map. A nil s resolves to
// an empty scope, matching Allows' fail-closed default.
func (s *AllowedMCPSet) Materialize(inherited *AllowedMCPSet) AllowedMCPSet {
	switch {
	case s == nil:
		return AllowedMCPSet{Kind: ToolSetScope}
	case s.Kind == ToolSetInherited:
		if inherited == nil {
			return AllowedMCPSet{Kind: ToolSetScope}
		}
		return AllowedMCPSet{Kind: inherited.Kind, Servers: maps.Clone(inherited.Servers)}
	case s.Kind == ToolSetAll:
		return AllowedMCPSet{Kind: ToolSetAll}
	default:
		return AllowedMCPSet{Kind: ToolSetScope, Servers: maps.Clone(s.Servers)}
	}
}

// MarshalJSON implements json.Marshaler.
func (s *AllowedMCPSet) MarshalJSON() ([]byte, error) {
	if s == nil {
		return []byte("null"), nil
	}
	if s.Kind != ToolSetScope {
		return json.Marshal(literalFromKind(s.Kind))
	}
	return json.Marshal(s.Servers)
}

// UnmarshalJSON implements json.Unmarshaler.
func (s *AllowedMCPSet) UnmarshalJSON(data []byte) error {
	var literal string
	if err := json.Unmarshal(data, &literal); err == nil {
		kind, err := kindFromLiteral("allowed_mcp", literal)
		if err != nil {
			return err
		}
		*s = AllowedMCPSet{Kind: kind}
		return nil
	}
	var servers map[string][]string
	if err := json.Unmarshal(data, &servers); err != nil {
		return fmt.Errorf("allowed_mcp: expected an object of server names, %q or %q: %w",
			allowedSetAllLiteral, allowedSetInheritedLiteral, err)
	}
	*s = AllowedMCPSet{Kind: ToolSetScope, Servers: servers}
	return nil
}

// MarshalYAML implements yaml.Marshaler.
func (s *AllowedMCPSet) MarshalYAML() (any, error) {
	if s == nil {
		return nil, nil
	}
	if s.Kind != ToolSetScope {
		return literalFromKind(s.Kind), nil
	}
	return s.Servers, nil
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (s *AllowedMCPSet) UnmarshalYAML(value *yaml.Node) error {
	var literal string
	if err := value.Decode(&literal); err == nil {
		kind, err := kindFromLiteral("allowed_mcp", literal)
		if err != nil {
			return err
		}
		*s = AllowedMCPSet{Kind: kind}
		return nil
	}
	var servers map[string][]string
	if err := value.Decode(&servers); err != nil {
		return fmt.Errorf("allowed_mcp: expected an object of server names, %q or %q: %w",
			allowedSetAllLiteral, allowedSetInheritedLiteral, err)
	}
	*s = AllowedMCPSet{Kind: ToolSetScope, Servers: servers}
	return nil
}

// JSONSchema reports the wire shape for JSON schema generation, for
// the same reason as AllowedToolSet.JSONSchema.
func (AllowedMCPSet) JSONSchema() *jsonschema.Schema {
	return unionSchema(&jsonschema.Schema{
		Type: "object",
		AdditionalProperties: &jsonschema.Schema{
			Type:  "array",
			Items: &jsonschema.Schema{Type: "string"},
		},
	})
}
