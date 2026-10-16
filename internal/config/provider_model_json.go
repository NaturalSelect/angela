package config

import (
	"cmp"
	"encoding/json"
	"fmt"

	"github.com/invopop/jsonschema"
)

// NOTE: Method-free copy of ProviderModel, so encoding it cannot recurse into MarshalJSON.
type providerModelBody ProviderModel

// NOTE: The depth-0 default_* fields shadow the embedded catalog keys, so they never reach the wire and flag old configs on read.
type providerModelWire struct {
	providerModelBody

	MaxTokens       int64  `json:"max_tokens"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`

	OldMaxTokens       *int64  `json:"default_max_tokens,omitempty"`
	OldReasoningEffort *string `json:"default_reasoning_effort,omitempty"`
}

func (m ProviderModel) MarshalJSON() ([]byte, error) {
	return json.Marshal(providerModelWire{
		providerModelBody: providerModelBody(m),
		MaxTokens:         m.DefaultMaxTokens,
		ReasoningEffort:   m.DefaultReasoningEffort,
	})
}

func (m *ProviderModel) UnmarshalJSON(data []byte) error {
	wire := providerModelWire{
		providerModelBody: providerModelBody(*m),
		MaxTokens:         m.DefaultMaxTokens,
		ReasoningEffort:   m.DefaultReasoningEffort,
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.OldMaxTokens != nil {
		return fmt.Errorf("model %q: default_max_tokens was renamed to max_tokens", wire.ID)
	}
	if wire.OldReasoningEffort != nil {
		return fmt.Errorf("model %q: default_reasoning_effort was renamed to reasoning_effort", wire.ID)
	}

	*m = ProviderModel(wire.providerModelBody)
	m.DefaultMaxTokens = wire.MaxTokens
	m.DefaultReasoningEffort = wire.ReasoningEffort
	return nil
}

var providerModelSchemaRenames = map[string]string{
	"default_max_tokens":       "max_tokens",
	"default_reasoning_effort": "reasoning_effort",
}

func (ProviderModel) JSONSchemaExtend(schema *jsonschema.Schema) {
	if schema.Properties != nil {
		renamed := jsonschema.NewProperties()
		for pair := schema.Properties.Oldest(); pair != nil; pair = pair.Next() {
			renamed.Set(cmp.Or(providerModelSchemaRenames[pair.Key], pair.Key), pair.Value)
		}
		schema.Properties = renamed
	}
	for i, name := range schema.Required {
		if wireName, ok := providerModelSchemaRenames[name]; ok {
			schema.Required[i] = wireName
		}
	}
}
