package config

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/invopop/jsonschema"
	"gopkg.in/yaml.v3"
)

// AllowedAgentSet is the value of Agent.AllowedAgents, the dispatch
// counterpart of AllowedToolSet. A nil *AllowedAgentSet means "this
// layer did not mention allowed_agents" — mergeAgent relies on that
// nil check the same way it does for AllowedTools and AllowedMCP.
//
// Unlike AllowedToolSet, dispatch starts open rather than closed: nil
// and ToolSetAll both mean every dispatchable agent is available,
// matching the behavior before this field existed. ToolSetScope grants
// only Agents, where an empty list denies every agent — equivalent to
// dropping the agent tool entirely. ToolSetInherited does not apply
// here, since there is no coder-anchored dispatch list to inherit, so
// it is rejected at parse time rather than silently accepted and left
// unhandled by Allows.
//
// On the wire it round-trips through the shapes users already write
// for allowed_tools: a bare array (["explore"]) decodes to
// ToolSetScope, and the string "all" decodes to ToolSetAll so a
// higher-priority layer can restore full dispatch after a lower one
// narrowed it.
type AllowedAgentSet struct {
	Kind   ToolSetKind
	Agents []string
}

// Allows reports whether id is dispatchable under s. A nil s means no
// layer ever restricted dispatch, so — unlike AllowedToolSet.Allows —
// it returns true: allowed_agents narrows an otherwise-unrestricted
// capability rather than granting one that starts closed.
func (s *AllowedAgentSet) Allows(id string) bool {
	if s == nil || s.Kind == ToolSetAll {
		return true
	}
	return slices.Contains(s.Agents, id)
}

// clone returns an agent whitelist that shares nothing with s.
func (s *AllowedAgentSet) clone() *AllowedAgentSet {
	if s == nil {
		return nil
	}
	out := *s
	out.Agents = cloneSlice(s.Agents)
	return &out
}

// agentSetKindFromLiteral parses the one wire literal allowed_agents
// accepts. It intentionally does not share kindFromLiteral: that
// helper also accepts "inherited", which would read as a real option
// here even though no coder-anchored dispatch list exists to inherit.
func agentSetKindFromLiteral(literal string) (ToolSetKind, error) {
	if literal == allowedSetAllLiteral {
		return ToolSetAll, nil
	}
	return 0, fmt.Errorf("allowed_agents: unsupported string value %q, only %q is valid",
		literal, allowedSetAllLiteral)
}

// MarshalJSON implements json.Marshaler.
func (s *AllowedAgentSet) MarshalJSON() ([]byte, error) {
	if s == nil {
		return []byte("null"), nil
	}
	if s.Kind != ToolSetScope {
		return json.Marshal(literalFromKind(s.Kind))
	}
	return json.Marshal(s.Agents)
}

// UnmarshalJSON implements json.Unmarshaler.
func (s *AllowedAgentSet) UnmarshalJSON(data []byte) error {
	var literal string
	if err := json.Unmarshal(data, &literal); err == nil {
		kind, err := agentSetKindFromLiteral(literal)
		if err != nil {
			return err
		}
		*s = AllowedAgentSet{Kind: kind}
		return nil
	}
	var agents []string
	if err := json.Unmarshal(data, &agents); err != nil {
		return fmt.Errorf("allowed_agents: expected an array of agent IDs or %q: %w",
			allowedSetAllLiteral, err)
	}
	*s = AllowedAgentSet{Kind: ToolSetScope, Agents: agents}
	return nil
}

// MarshalYAML implements yaml.Marshaler.
func (s *AllowedAgentSet) MarshalYAML() (any, error) {
	if s == nil {
		return nil, nil
	}
	if s.Kind != ToolSetScope {
		return literalFromKind(s.Kind), nil
	}
	return s.Agents, nil
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (s *AllowedAgentSet) UnmarshalYAML(value *yaml.Node) error {
	var literal string
	if err := value.Decode(&literal); err == nil {
		kind, err := agentSetKindFromLiteral(literal)
		if err != nil {
			return err
		}
		*s = AllowedAgentSet{Kind: kind}
		return nil
	}
	var agents []string
	if err := value.Decode(&agents); err != nil {
		return fmt.Errorf("allowed_agents: expected an array of agent IDs or %q: %w",
			allowedSetAllLiteral, err)
	}
	*s = AllowedAgentSet{Kind: ToolSetScope, Agents: agents}
	return nil
}

// JSONSchema reports the wire shape for JSON schema generation, for
// the same reason as AllowedToolSet.JSONSchema.
func (AllowedAgentSet) JSONSchema() *jsonschema.Schema {
	return unionSchema(&jsonschema.Schema{
		Type:  "array",
		Items: &jsonschema.Schema{Type: "string"},
	}, allowedSetAllLiteral)
}
