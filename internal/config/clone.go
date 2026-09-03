package config

// clonePtr copies the value behind p, so a caller mutating one side
// cannot be seen through the other.
func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// cloneJSONValue copies a value decoded from JSON. Only maps and
// slices need copying; everything else a JSON decoder produces is a
// scalar, and scalars cannot be mutated in place.
func cloneJSONValue(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		return cloneJSONMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneJSONValue(item)
		}
		return out
	default:
		return v
	}
}

// cloneJSONMap copies a provider-options map all the way down. A
// shallow copy is not enough: values are `any`, and a provider option
// like "extra_body" is itself a map that two sessions would otherwise
// share.
func cloneJSONMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneJSONValue(v)
	}
	return out
}

// clone returns a preset that shares nothing with o.
func (o SelectedModelOverride) clone() SelectedModelOverride {
	o.ReasoningEffort = clonePtr(o.ReasoningEffort)
	o.Think = clonePtr(o.Think)
	o.MaxTokens = clonePtr(o.MaxTokens)
	o.Temperature = clonePtr(o.Temperature)
	o.TopP = clonePtr(o.TopP)
	o.TopK = clonePtr(o.TopK)
	o.FrequencyPenalty = clonePtr(o.FrequencyPenalty)
	o.PresencePenalty = clonePtr(o.PresencePenalty)
	o.ProviderOptions = cloneJSONMap(o.ProviderOptions)
	return o
}

// clone returns a model config that shares nothing with m. SelectedModel
// is a plain identity (model, provider), so there is nothing mutable to
// copy; the method exists so callers do not need to special-case it.
func (m SelectedModel) clone() SelectedModel {
	return m
}

// clone returns a catalog model entry that shares nothing with m.
func (m ProviderModel) clone() ProviderModel {
	m.Options.Temperature = clonePtr(m.Options.Temperature)
	m.Options.TopP = clonePtr(m.Options.TopP)
	m.Options.TopK = clonePtr(m.Options.TopK)
	m.Options.FrequencyPenalty = clonePtr(m.Options.FrequencyPenalty)
	m.Options.PresencePenalty = clonePtr(m.Options.PresencePenalty)
	m.Options.ProviderOptions = cloneJSONMap(m.Options.ProviderOptions)
	m.ReasoningLevels = cloneSlice(m.ReasoningLevels)
	if m.Variants != nil {
		variants := make(map[string]SelectedModelOverride, len(m.Variants))
		for name, override := range m.Variants {
			variants[name] = override.clone()
		}
		m.Variants = variants
	}
	return m
}

// clone returns a tool whitelist that shares nothing with s.
func (s *AllowedToolSet) clone() *AllowedToolSet {
	if s == nil {
		return nil
	}
	out := *s
	out.Tools = cloneSlice(s.Tools)
	return &out
}

// clone returns an MCP whitelist that shares nothing with s, including
// the per-server tool lists.
func (s *AllowedMCPSet) clone() *AllowedMCPSet {
	if s == nil {
		return nil
	}
	out := *s
	if s.Servers != nil {
		servers := make(map[string][]string, len(s.Servers))
		for server, allowed := range s.Servers {
			servers[server] = cloneSlice(allowed)
		}
		out.Servers = servers
	}
	return &out
}

// cloneSlice copies a slice, preserving the nil/empty distinction that
// the tool sets rely on: a nil list means "not mentioned", an empty
// one means "mentioned and denies everything".
func cloneSlice[T any](s []T) []T {
	if s == nil {
		return nil
	}
	return append(make([]T, 0, len(s)), s...)
}

// clone returns an agent definition that shares nothing with a.
func (a Agent) clone() Agent {
	a.Disabled = clonePtr(a.Disabled)
	a.Hidden = clonePtr(a.Hidden)
	a.MaxTokens = clonePtr(a.MaxTokens)
	a.Temperature = clonePtr(a.Temperature)
	a.DisabledTools = cloneSlice(a.DisabledTools)
	a.ContextPaths = cloneSlice(a.ContextPaths)
	a.AllowedTools = a.AllowedTools.clone()
	a.AllowedMCP = a.AllowedMCP.clone()
	return a
}
