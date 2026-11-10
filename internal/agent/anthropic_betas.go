package agent

import (
	"slices"
	"strings"

	"github.com/NaturalSelect/angela/internal/config"
)

const anthropicBetaHeader = "anthropic-beta"

// anthropicBetaRule enables a beta when the request configuration uses the
// feature it gates. The API rejects beta values it does not know, so a rule
// must only match when the feature is actually requested.
type anthropicBetaRule struct {
	beta  string
	match func(merged map[string]any, think bool) bool
}

var anthropicBetaRules = []anthropicBetaRule{
	{"interleaved-thinking-2025-05-14", func(merged map[string]any, think bool) bool {
		return think || merged["thinking"] != nil
	}},
	{"context-management-2025-06-27", func(merged map[string]any, _ bool) bool {
		_, ok := extraBodyOf(merged)["context_management"]
		return ok
	}},
	{"compact-2026-01-12", func(merged map[string]any, _ bool) bool {
		management, _ := extraBodyOf(merged)["context_management"].(map[string]any)
		edits, _ := management["edits"].([]any)
		return slices.ContainsFunc(edits, func(edit any) bool {
			kind, _ := edit.(map[string]any)["type"].(string)
			return strings.HasPrefix(kind, "compact_")
		})
	}},
	{"fast-mode-2026-02-01", func(merged map[string]any, _ bool) bool {
		speed, _ := extraBodyOf(merged)["speed"].(string)
		return strings.EqualFold(speed, "fast")
	}},
	{"task-budgets-2026-03-13", func(merged map[string]any, _ bool) bool {
		extraBody := extraBodyOf(merged)
		if outputConfig, ok := extraBody["output_config"].(map[string]any); ok {
			if _, ok := outputConfig["task_budget"]; ok {
				return true
			}
		}
		for key := range extraBody {
			if strings.HasPrefix(key, "output_config.task_budget") {
				return true
			}
		}
		return false
	}},
	{"thinking-display-updates-2026-08-18", func(merged map[string]any, _ bool) bool {
		display, _ := merged["thinking_display"].(string)
		return display == "updates"
	}},
	{"mcp-client-2025-11-20", func(merged map[string]any, _ bool) bool {
		_, ok := extraBodyOf(merged)["mcp_servers"]
		return ok
	}},
}

func extraBodyOf(merged map[string]any) map[string]any {
	extraBody, _ := merged["extra_body"].(map[string]any)
	return extraBody
}

// autoAnthropicBetas returns the betas implied by the request configuration,
// in rule order.
func autoAnthropicBetas(merged map[string]any, think bool) []string {
	var out []string
	for _, rule := range anthropicBetaRules {
		if rule.match(merged, think) {
			out = append(out, rule.beta)
		}
	}
	return out
}

// applyAnthropicBetas rewrites the anthropic-beta entry of headers, under any
// key casing, to the resolved list, or removes it when the list is empty.
// The list is the header's own values, the betas the request implies and the
// configured enables, minus the configured disables. Provider, model and
// variant settings layer in that order, the one nearest the request winning.
func applyAnthropicBetas(headers map[string]string, providerCfg config.ProviderConfig, model config.ProviderModel, think bool) {
	var legacy []string
	for key, value := range headers {
		if !strings.EqualFold(key, anthropicBetaHeader) {
			continue
		}
		delete(headers, key)
		for part := range strings.SplitSeq(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				legacy = append(legacy, part)
			}
		}
	}

	var auto []string
	if merged, err := mergeProviderOptionMaps(model.Options.ProviderOptions, providerCfg.ProviderOptions); err == nil {
		auto = autoAnthropicBetas(merged, think)
	}

	manual := (&config.AnthropicBeta{Enable: legacy}).
		Overlay(providerCfg.AnthropicBeta).
		Overlay(model.AnthropicBeta)

	var list []string
	for _, group := range [][]string{legacy, auto, manual.GetEnable()} {
		for _, beta := range group {
			if !slices.Contains(list, beta) && !slices.Contains(manual.GetDisable(), beta) {
				list = append(list, beta)
			}
		}
	}
	if len(list) > 0 {
		headers[anthropicBetaHeader] = strings.Join(list, ",")
	}
}
