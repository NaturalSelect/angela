package agent

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"strings"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/discover"

	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/azure"
	"charm.land/fantasy/providers/bedrock"
	"charm.land/fantasy/providers/google"
	"charm.land/fantasy/providers/openai"
	"charm.land/fantasy/providers/openaicompat"
	"charm.land/fantasy/providers/openrouter"
	"charm.land/fantasy/providers/vercel"
	"github.com/qjebbs/go-jsons"
)

func getProviderOptions(model Model, providerCfg config.ProviderConfig, promptCacheKey string) fantasy.ProviderOptions {
	options := fantasy.ProviderOptions{}

	providerCfgOpts := []byte("{}")
	catwalkOpts := []byte("{}")

	if providerCfg.ProviderOptions != nil {
		data, err := json.Marshal(providerCfg.ProviderOptions)
		if err == nil {
			providerCfgOpts = data
		}
	}

	if model.CatwalkCfg.Options.ProviderOptions != nil {
		data, err := json.Marshal(model.CatwalkCfg.Options.ProviderOptions)
		if err == nil {
			catwalkOpts = data
		}
	}

	readers := []io.Reader{
		bytes.NewReader(catwalkOpts),
		bytes.NewReader(providerCfgOpts),
	}

	got, err := jsons.Merge(readers)
	if err != nil {
		slog.Error("Could not merge call config", "err", err)
		return options
	}

	mergedOptions := make(map[string]any)

	err = json.Unmarshal([]byte(got), &mergedOptions)
	if err != nil {
		slog.Error("Could not create config for call", "err", err)
		return options
	}

	reasoningEffort := effectiveReasoningEffort(model)
	// effectiveReasoningEffort already applied the catalog rules, so a
	// non-empty result is by itself the decision to send an effort.
	shouldSetEffort := reasoningEffort != ""

	switch providerCfg.Type {
	case openai.Name, azure.Name:
		_, hasReasoningEffort := mergedOptions["reasoning_effort"]
		if !hasReasoningEffort && shouldSetEffort {
			mergedOptions["reasoning_effort"] = reasoningEffort
		}
		if _, hasPromptCacheKey := mergedOptions["prompt_cache_key"]; !hasPromptCacheKey && promptCacheKey != "" {
			mergedOptions["prompt_cache_key"] = promptCacheKey
		}
		if responsesAPIEnabled(providerCfg, model.CatwalkCfg, model.CatwalkCfg.ID) {
			if openai.IsResponsesReasoningModel(model.CatwalkCfg.ID) || shouldSetEffort {
				mergedOptions["reasoning_summary"] = "auto"
				mergedOptions["include"] = []openai.IncludeType{openai.IncludeReasoningEncryptedContent}
			}
			parsed, err := openai.ParseResponsesOptions(mergedOptions)
			if err == nil {
				options[openai.Name] = parsed
			}
		} else {
			parsed, err := openai.ParseOptions(mergedOptions)
			if err == nil {
				options[openai.Name] = parsed
			}
		}

	case anthropic.Name, bedrock.Name:
		var (
			_, hasEffort = mergedOptions["effort"]
			_, hasThink  = mergedOptions["thinking"]
			extraBody    = make(map[string]any)
		)

		switch providerCfg.ID {
		case string(catwalk.InferenceProviderAlibabaSingapore), string(catwalk.InferenceProviderAlibabaUS):
			switch {
			case !hasEffort && shouldSetEffort:
				extraBody["reasoning_effort"] = reasoningEffort
			case !hasThink && model.CatwalkCfg.CanReason:
				if model.Think {
					extraBody["thinking"] = map[string]any{"type": "enabled"}
				} else {
					extraBody["thinking"] = map[string]any{"type": "disabled"}
				}
			}
			mergedOptions["extra_body"] = extraBody

		default:
			switch {
			case !hasEffort && shouldSetEffort:
				mergedOptions["effort"] = reasoningEffort
			case !hasThink && model.Think:
				mergedOptions["thinking"] = map[string]any{"budget_tokens": 2000}
			}

			// metadata.user_id is an Anthropic-API-specific anti-abuse field.
			// Bedrock (a different providerCfg.Type sharing this case) has its own fixed request schema and does not
			// support it.
			if providerCfg.Type == anthropic.Name {
				existingExtraBody, _ := mergedOptions["extra_body"].(map[string]any)
				mergedOptions["extra_body"] = withAnthropicUserID(existingExtraBody, promptCacheKey)
			}
		}

		requestVisibleThinking(mergedOptions)

		parsed, err := anthropic.ParseOptions(mergedOptions)
		if err == nil {
			options[anthropic.Name] = parsed
		}

	case openrouter.Name:
		_, hasReasoning := mergedOptions["reasoning"]
		if !hasReasoning && shouldSetEffort {
			mergedOptions["reasoning"] = map[string]any{
				"enabled": true,
				"effort":  reasoningEffort,
			}
		}
		parsed, err := openrouter.ParseOptions(mergedOptions)
		if err == nil {
			options[openrouter.Name] = parsed
		}

	case vercel.Name:
		_, hasReasoning := mergedOptions["reasoning"]
		if !hasReasoning && shouldSetEffort {
			mergedOptions["reasoning"] = map[string]any{
				"enabled": true,
				"effort":  reasoningEffort,
			}
		}
		parsed, err := vercel.ParseOptions(mergedOptions)
		if err == nil {
			options[vercel.Name] = parsed
		}

	case google.Name:
		_, hasReasoning := mergedOptions["thinking_config"]
		if !hasReasoning {
			if strings.HasPrefix(model.CatwalkCfg.ID, "gemini-2") {
				mergedOptions["thinking_config"] = map[string]any{
					"thinking_budget":  2000,
					"include_thoughts": true,
				}
			} else {
				mergedOptions["thinking_config"] = map[string]any{
					"thinking_level":   reasoningEffort,
					"include_thoughts": true,
				}
			}
		}
		parsed, err := google.ParseOptions(mergedOptions)
		if err == nil {
			options[google.Name] = parsed
		}

	case openaicompat.Name:
		extraBody, _ := mergedOptions["extra_body"].(map[string]any)
		if extraBody == nil {
			extraBody = make(map[string]any)
		}

		_, hasReasoningEffort := mergedOptions["reasoning_effort"]
		if !hasReasoningEffort && shouldSetEffort {
			switch providerCfg.ID {
			case string(catwalk.InferenceProviderIoNet):
				extraBody["reasoning"] = map[string]string{"effort": reasoningEffort}
			case string(catwalk.InferenceProviderOpenCodeGo), string(catwalk.InferenceProviderOpenCodeZen):
				// MiniMax models use the "thinking" parameter instead of
				// "reasoning_effort". Other models on these providers still
				// use the standard field.
				if !strings.HasPrefix(strings.ToLower(model.CatwalkCfg.ID), "minimax") {
					mergedOptions["reasoning_effort"] = reasoningEffort
				}
			default:
				mergedOptions["reasoning_effort"] = reasoningEffort
			}
		}

		// "reasoning effort" is a standard OpenAI field, but "thinking" is not.
		// Setting it in the right way for each provider.
		// TODO: Abstract this in Fantasy somehow?
		// TODO: Allow custom providers to specify how to set this?
		switch providerCfg.ID {
		case string(catwalk.InferenceProviderIoNet):
			if _, ok := extraBody["reasoning"]; !ok && model.CatwalkCfg.CanReason {
				if model.Think {
					extraBody["reasoning"] = map[string]string{"effort": "medium"}
				} else {
					extraBody["reasoning"] = map[string]string{"effort": "none"}
				}
			}

		case string(catwalk.InferenceProviderZAI), string(catwalk.InferenceProviderDeepSeek):
			if model.Think || reasoningEffort != "" {
				extraBody["thinking"] = map[string]any{"type": "enabled"}
			} else {
				extraBody["thinking"] = map[string]any{"type": "disabled"}
			}

		case string(catwalk.InferenceProviderFireworks):
			// NOTE: Fireworks break if we set both `reasoning_effort` and `thinking`.
			if reasoningEffort == "" {
				if model.Think {
					extraBody["thinking"] = map[string]any{"type": "enabled"}
				} else {
					extraBody["thinking"] = map[string]any{"type": "disabled"}
				}
			}

		case string(catwalk.InferenceProviderBaseten):
			extraBody["chat_template_args"] = map[string]any{
				"enable_thinking": model.Think || reasoningEffort != "" && reasoningEffort != "none",
			}

		case string(catwalk.InferenceProviderOpenCodeGo), string(catwalk.InferenceProviderOpenCodeZen):
			// MiniMax M3 uses the "thinking" parameter to control reasoning.
			// "reasoning_split" must be true so thinking content is returned
			// in the "reasoning_content" field instead of inline in "content".
			if strings.HasPrefix(strings.ToLower(model.CatwalkCfg.ID), "minimax") {
				if model.CatwalkCfg.CanReason && (model.Think || reasoningEffort != "") {
					extraBody["thinking"] = map[string]any{"type": "adaptive"}
					extraBody["reasoning_split"] = true
				} else {
					extraBody["thinking"] = map[string]any{"type": "disabled"}
				}
			}

		case string(catwalk.InferenceProviderAlibabaSingapore), string(catwalk.InferenceProviderAlibabaUS):
			if model.CatwalkCfg.CanReason {
				extraBody["enable_thinking"] = model.Think || reasoningEffort != ""
			}
		}

		mergedOptions["extra_body"] = withPromptCacheKey(extraBody, promptCacheKey)

		parsed, err := openaicompat.ParseOptions(mergedOptions)
		if err == nil {
			options[openaicompat.Name] = parsed
		}

		// Some openai-compat providers silently dispatch specific models
		// to the Responses API even though the provider is configured as
		// openai-compat, and that path reads options[openai.Name]
		// instead of options[openaicompat.Name]. Mirror the full
		// extra_body there too (not just prompt_cache_key), so nothing
		// configured through it silently disappears from the request.
		// Fields with no Responses equivalent are dropped by
		// ParseResponsesOptions like any other unrecognized JSON key.
		if openaiCompatUsesResponses(providerCfg, model.CatwalkCfg, model.CatwalkCfg.ID) {
			if respParsed, err := openai.ParseResponsesOptions(extraBody); err == nil {
				options[openai.Name] = respParsed
			}
		}

	default:
		// Known custom providers (litellm, ollama, omlx) are
		// openai-compat under the hood.
		if discover.IsKnownCustomProvider(string(providerCfg.Type)) {
			extraBody, _ := mergedOptions["extra_body"].(map[string]any)
			mergedOptions["extra_body"] = withPromptCacheKey(extraBody, promptCacheKey)
			parsed, err := openaicompat.ParseOptions(mergedOptions)
			if err == nil {
				options[openaicompat.Name] = parsed
			}
		}
	}

	return options
}

// withPromptCacheKey sets extraBody["prompt_cache_key"] to promptCacheKey,
// unless the caller already configured one explicitly or promptCacheKey
// is empty. Used by the openai-compat family, which has no typed
// PromptCacheKey field and instead relies on the extra_body escape hatch.
func withPromptCacheKey(extraBody map[string]any, promptCacheKey string) map[string]any {
	if extraBody == nil {
		extraBody = make(map[string]any)
	}
	if _, ok := extraBody["prompt_cache_key"]; !ok && promptCacheKey != "" {
		extraBody["prompt_cache_key"] = promptCacheKey
	}
	return extraBody
}

// requestVisibleThinking asks Anthropic to stream its thinking summary.
// Adaptive thinking — which the effort option always selects — omits the
// trace unless display says otherwise, and the default is decided per
// model ID, so a gateway-served or renamed model thinks with nothing to
// show. Setting display here short-circuits that lookup for every model;
// an explicit user setting still wins.
func requestVisibleThinking(mergedOptions map[string]any) {
	if _, ok := mergedOptions["thinking_display"]; ok {
		return
	}
	mergedOptions["thinking_display"] = string(anthropic.ThinkingDisplaySummarized)
}

// withAnthropicUserID sets extraBody["metadata"]["user_id"] to a value
// derived from promptCacheKey, unless the caller already configured a
// metadata.user_id explicitly or promptCacheKey is empty. Anthropic's
// ProviderOptions has no typed Metadata field, so this relies on the
// same extra_body escape hatch as withPromptCacheKey.
func withAnthropicUserID(extraBody map[string]any, promptCacheKey string) map[string]any {
	if promptCacheKey == "" {
		return extraBody
	}
	if extraBody == nil {
		extraBody = make(map[string]any)
	}
	metadata, _ := extraBody["metadata"].(map[string]any)
	if metadata == nil {
		metadata = make(map[string]any)
	}
	if _, ok := metadata["user_id"]; !ok {
		metadata["user_id"] = buildAnthropicUserID(promptCacheKey)
	}
	extraBody["metadata"] = metadata
	return extraBody
}

func mergeCallOptions(model Model, cfg config.ProviderConfig, promptCacheKey string) (fantasy.ProviderOptions, *float64, *float64, *int64, *float64, *float64) {
	modelOptions := getProviderOptions(model, cfg, promptCacheKey)
	temp := model.CatwalkCfg.Options.Temperature
	topP := model.CatwalkCfg.Options.TopP
	topK := model.CatwalkCfg.Options.TopK
	freqPenalty := model.CatwalkCfg.Options.FrequencyPenalty
	presPenalty := model.CatwalkCfg.Options.PresencePenalty
	return modelOptions, temp, topP, topK, freqPenalty, presPenalty
}
