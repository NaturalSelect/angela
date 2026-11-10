package config

import (
	"slices"
	"strings"
)

// AnthropicBeta forces anthropic-beta header values on or off. The
// header is only sent to providers of type anthropic. Values Angela
// would add on its own are listed in Disable to switch them off.
type AnthropicBeta struct {
	Enable  []string `json:"enable,omitempty" jsonschema:"description=anthropic-beta values that are always sent"`
	Disable []string `json:"disable,omitempty" jsonschema:"description=anthropic-beta values that are never sent\\, including ones Angela enables automatically"`
}

// Overlay returns b with top layered over it. A value named by top
// moves to top's list: enabling it removes it from Disable and
// disabling it removes it from Enable. A value in both of top's lists
// ends up disabled. Values are trimmed, empty ones dropped and the
// first occurrence kept. Either side may be nil, and neither input is
// modified.
func (b *AnthropicBeta) Overlay(top *AnthropicBeta) *AnthropicBeta {
	var enable, disable []string
	if b != nil {
		enable, disable = normalizeBetas(b.Enable), normalizeBetas(b.Disable)
	}
	if top != nil {
		topEnable, topDisable := normalizeBetas(top.Enable), normalizeBetas(top.Disable)
		enable = slices.DeleteFunc(enable, func(v string) bool {
			return slices.Contains(topDisable, v)
		})
		disable = slices.DeleteFunc(disable, func(v string) bool {
			return slices.Contains(topEnable, v)
		})
		for _, v := range topEnable {
			if !slices.Contains(topDisable, v) && !slices.Contains(enable, v) {
				enable = append(enable, v)
			}
		}
		for _, v := range topDisable {
			if !slices.Contains(disable, v) {
				disable = append(disable, v)
			}
		}
	}
	if len(enable) == 0 && len(disable) == 0 {
		return nil
	}
	return &AnthropicBeta{Enable: enable, Disable: disable}
}

// GetEnable returns the enabled values, or nil when b is nil.
func (b *AnthropicBeta) GetEnable() []string {
	if b == nil {
		return nil
	}
	return b.Enable
}

// GetDisable returns the disabled values, or nil when b is nil.
func (b *AnthropicBeta) GetDisable() []string {
	if b == nil {
		return nil
	}
	return b.Disable
}

// clone returns a value that shares nothing with b.
func (b *AnthropicBeta) clone() *AnthropicBeta {
	if b == nil {
		return nil
	}
	return &AnthropicBeta{
		Enable:  cloneSlice(b.Enable),
		Disable: cloneSlice(b.Disable),
	}
}

// normalizeBetas trims values, drops empty ones and removes duplicates,
// returning a fresh slice.
func normalizeBetas(values []string) []string {
	var out []string
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || slices.Contains(out, v) {
			continue
		}
		out = append(out, v)
	}
	return out
}
