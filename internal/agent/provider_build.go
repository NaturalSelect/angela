package agent

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/discover"
	"github.com/NaturalSelect/angela/internal/log"
	"github.com/NaturalSelect/angela/internal/oauth/copilot"

	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/azure"
	"charm.land/fantasy/providers/bedrock"
	"charm.land/fantasy/providers/google"
	"charm.land/fantasy/providers/openai"
	"charm.land/fantasy/providers/openaicompat"
	"charm.land/fantasy/providers/openrouter"
	"charm.land/fantasy/providers/vercel"
	openaisdk "github.com/openai/openai-go/v3/option"
)

// Copilot models that use the Responses API instead of Chat Completions.
var copilotResponsesModels = map[string]bool{
	"gpt-5.2":       true,
	"gpt-5.2-codex": true,
	"gpt-5.3-codex": true,
	"gpt-5.4":       true,
	"gpt-5.4-mini":  true,
	"gpt-5.5":       true,
	"gpt-5-mini":    true,
	"gpt-5.6-luna":  true,
	"gpt-5.6-terra": true,
	"gpt-5.6-sol":   true,
}

// openaiCompatResponsesAPIFunc returns the model filter used to select
// which models the given openai-compat provider dispatches through the
// Responses API instead of Chat Completions. The second return value is
// false when the provider never uses the Responses API. This is the
// single source of truth consulted both when constructing the provider
// (to opt into fantasy's Responses API routing) and when building
// per-call provider options (to know which options[Name] key the
// request will actually be read from).
func openaiCompatResponsesAPIFunc(providerID string) (fn func(modelID string) bool, ok bool) {
	switch providerID {
	case string(catwalk.InferenceProviderCopilot):
		return func(modelID string) bool { return copilotResponsesModels[modelID] }, true
	default:
		return nil, false
	}
}

// OpenCode models that user Anthropic Messages API instead of Chat Completions.
var opencodeMessagesModels = map[string]bool{
	"qwen3.7-max": true,
}

// openaiCompatUsesResponses reports whether an openai-compat provider
// dispatches this model to the Responses API. It mirrors the transport
// choice made in buildOpenaiCompatProvider, where an explicit
// use_responses setting replaces the per-provider table. Model-level
// use_responses wins over the provider-level setting.
func openaiCompatUsesResponses(providerCfg config.ProviderConfig, modelCfg config.ProviderModel, modelID string) bool {
	if modelCfg.UseResponses != nil {
		return *modelCfg.UseResponses
	}
	if providerCfg.UseResponses != nil {
		return *providerCfg.UseResponses
	}
	fn, ok := openaiCompatResponsesAPIFunc(providerCfg.ID)
	return ok && fn(modelID)
}

// responsesAPIEnabled reports whether a model on this provider talks the
// OpenAI Responses API. An explicit use_responses setting decides
// outright; left unset the choice falls back to recognizing the model ID,
// which only knows OpenAI's own names and misses gateway aliases.
// Model-level use_responses wins over the provider-level setting.
func responsesAPIEnabled(providerCfg config.ProviderConfig, modelCfg config.ProviderModel, modelID string) bool {
	if modelCfg.UseResponses != nil {
		return *modelCfg.UseResponses
	}
	if providerCfg.UseResponses != nil {
		return *providerCfg.UseResponses
	}
	return openai.IsResponsesModel(modelID)
}

// effectiveUseResponses returns the resolved use_responses flag for a
// model: model-level wins over provider-level, either may be unset.
func effectiveUseResponses(providerCfg config.ProviderConfig, modelCfg config.ProviderModel) *bool {
	if modelCfg.UseResponses != nil {
		return modelCfg.UseResponses
	}
	return providerCfg.UseResponses
}

// effectiveReasoningEffort returns the reasoning effort to apply for provider calls.
// An effort set explicitly in the model config wins outright: it is the user's own
// statement that the model reasons, and catalog metadata cannot know about hand-typed
// models or gateway aliases. Otherwise it takes the model default when valid, and
// finally falls back to the first configured reasoning level.
func effectiveReasoningEffort(model Model) string {
	if !model.CatwalkCfg.CanReason {
		return ""
	}

	if effort := model.CatwalkCfg.DefaultReasoningEffort; effort != "" && slices.Contains(model.CatwalkCfg.ReasoningLevels, effort) {
		return effort
	}
	if len(model.CatwalkCfg.ReasoningLevels) > 0 {
		return model.CatwalkCfg.ReasoningLevels[0]
	}
	return ""
}

// selectedModelDisplayName mirrors modelDisplayName for a model that
// was never built into a live agent.Model — such as a session's host
// agent, which the commit trailer credits without invoking it.
func (c *coordinator) selectedModelDisplayName(sel config.SelectedModel) string {
	if catalog := c.cfg.Config().GetModel(sel.Provider, sel.Model); catalog != nil {
		return cmp.Or(catalog.Name, sel.Model)
	}
	return sel.Model
}

// buildModel resolves the single model an ActiveAgent runs on. The
// model is already materialized on the instance, so this no longer
// consults the shared Config.Slots table — that lookup, and its
// fallback for a bad name, moved into config.InstantiateAgent.
func (c *coordinator) buildModel(ctx context.Context, active config.ActiveAgent, isSubAgent bool) (Model, error) {
	agent := active.Agent
	modelCfg := active.Model
	if modelCfg.Provider == "" || modelCfg.Model == "" {
		return Model{}, errModelNotSelected
	}

	providerCfg, ok := c.cfg.Config().Providers.Get(modelCfg.Provider)
	if !ok {
		return Model{}, errModelProviderNotConfigured
	}

	var catwalkModel *config.ProviderModel
	for _, m := range providerCfg.Models {
		if m.ID == modelCfg.Model {
			catwalkModel = &m
		}
	}
	if catwalkModel == nil {
		return Model{}, errModelNotFound
	}

	// The variant overlay lands before the provider is built so that
	// provider options a variant sets reach buildProvider too. The
	// agent's own preset always wins; the slot's is only a fallback
	// for whichever agent runs there and never names one of its own.
	variant := active.EffectiveVariant()
	resolvedCatwalk, variantApplied := catwalkModel.WithVariant(variant)
	if variant != "" && !variantApplied {
		slog.Warn("Unknown model variant; falling back to the model baseline",
			"agent", agent.ID, "model", active.Slot, "variant", variant)
	}

	think := active.Think

	provider, err := c.buildProvider(providerCfg, resolvedCatwalk, think, isSubAgent)
	if err != nil {
		return Model{}, err
	}

	modelID := modelCfg.Model
	if modelCfg.Provider == openrouter.Name && isExactoSupported(modelID) {
		modelID += ":exacto"
	}

	languageModel, err := provider.LanguageModel(ctx, modelID)
	if err != nil {
		return Model{}, err
	}

	resolved := Model{
		Model:      languageModel,
		CatwalkCfg: resolvedCatwalk,
		ModelCfg:   modelCfg,
		FlatRate:   providerCfg.FlatRate,
		Think:      think,
	}
	if variantApplied {
		resolved.Variant = variant
	}

	// Baking the agent's temperature into the model's catalog options lets
	// every downstream reader of Model().CatwalkCfg.Options.Temperature
	// (mergeCallOptions, runSubAgent) pick it up without knowing about the
	// agent at all.
	if agent.Temperature != nil {
		resolved.CatwalkCfg.Options.Temperature = agent.Temperature
	}

	return resolved, nil
}

func (c *coordinator) buildAnthropicProvider(baseURL, apiKey string, headers map[string]string, providerID string) (fantasy.Provider, error) {
	var opts []anthropic.Option

	switch {
	case strings.HasPrefix(apiKey, "Bearer "):
		// NOTE: Prevent the SDK from picking up the API key from env.
		os.Setenv("ANTHROPIC_API_KEY", "")
		headers["Authorization"] = apiKey
	case providerID == string(catwalk.InferenceProviderMiniMax) || providerID == string(catwalk.InferenceProviderMiniMaxChina):
		// NOTE: Prevent the SDK from picking up the API key from env.
		os.Setenv("ANTHROPIC_API_KEY", "")
		headers["Authorization"] = "Bearer " + apiKey
	case apiKey != "":
		// X-Api-Key header
		opts = append(opts, anthropic.WithAPIKey(apiKey))
	}

	if len(headers) > 0 {
		opts = append(opts, anthropic.WithHeaders(headers))
	}

	if baseURL := config.NormalizeBaseURL(baseURL, catwalk.TypeAnthropic); baseURL != "" {
		opts = append(opts, anthropic.WithBaseURL(baseURL))
	}

	httpClient := log.NewIdleTimeoutClient()
	if c.cfg.Config().Options.Debug {
		httpClient = log.NewHTTPClient()
	}
	opts = append(opts, anthropic.WithHTTPClient(httpClient))
	return anthropic.New(opts...)
}

func (c *coordinator) buildOpenaiProvider(baseURL, apiKey string, headers map[string]string, useResponses *bool) (fantasy.Provider, error) {
	opts := []openai.Option{
		openai.WithAPIKey(apiKey),
		openai.WithUseResponsesAPI(),
		// Gateways are routinely configured as a plain "openai" provider
		// (the type defaults to it) while answering in the
		// chat-completions shape, where reasoning arrives as
		// delta.reasoning_content. The openai provider parses reasoning
		// only on its Responses path, so without these hooks the text is
		// read into nothing and the turn renders with no thinking at all.
		openai.WithLanguageModelOptions(
			openai.WithLanguageModelStreamExtraFunc(openaicompat.StreamExtraFunc),
			openai.WithLanguageModelExtraContentFunc(openaicompat.ExtraContentFunc),
		),
	}
	if useResponses != nil {
		forced := *useResponses
		opts = append(opts, openai.WithResponsesAPIFunc(func(string) bool { return forced }))
	}
	httpClient := log.NewIdleTimeoutClient()
	if c.cfg.Config().Options.Debug {
		httpClient = log.NewHTTPClient()
	}
	opts = append(opts, openai.WithHTTPClient(httpClient))
	if len(headers) > 0 {
		opts = append(opts, openai.WithHeaders(headers))
	}
	if baseURL := config.NormalizeBaseURL(baseURL, catwalk.TypeOpenAI); baseURL != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}
	return openai.New(opts...)
}

func (c *coordinator) buildOpenrouterProvider(_, apiKey string, headers map[string]string) (fantasy.Provider, error) {
	opts := []openrouter.Option{
		openrouter.WithAPIKey(apiKey),
	}
	httpClient := log.NewIdleTimeoutClient()
	if c.cfg.Config().Options.Debug {
		httpClient = log.NewHTTPClient()
	}
	opts = append(opts, openrouter.WithHTTPClient(httpClient))
	if len(headers) > 0 {
		opts = append(opts, openrouter.WithHeaders(headers))
	}
	return openrouter.New(opts...)
}

func (c *coordinator) buildVercelProvider(_, apiKey string, headers map[string]string) (fantasy.Provider, error) {
	opts := []vercel.Option{
		vercel.WithAPIKey(apiKey),
	}
	httpClient := log.NewIdleTimeoutClient()
	if c.cfg.Config().Options.Debug {
		httpClient = log.NewHTTPClient()
	}
	opts = append(opts, vercel.WithHTTPClient(httpClient))
	if len(headers) > 0 {
		opts = append(opts, vercel.WithHeaders(headers))
	}
	return vercel.New(opts...)
}

func (c *coordinator) buildOpenaiCompatProvider(baseURL, apiKey string, headers map[string]string, extraBody map[string]any, providerID string, isSubAgent bool, useResponses *bool) (fantasy.Provider, error) {
	opts := []openaicompat.Option{
		openaicompat.WithBaseURL(config.NormalizeBaseURL(baseURL, catwalk.TypeOpenAICompat)),
		openaicompat.WithAPIKey(apiKey),
	}

	switch {
	case useResponses != nil:
		if *useResponses {
			opts = append(opts, openaicompat.WithUseResponsesAPI(),
				openaicompat.WithResponsesAPIFunc(func(string) bool { return true }))
		}
	default:
		if fn, ok := openaiCompatResponsesAPIFunc(providerID); ok {
			opts = append(opts, openaicompat.WithUseResponsesAPI(), openaicompat.WithResponsesAPIFunc(fn))
		}
	}

	// Set HTTP client based on provider and debug mode.
	var httpClient *http.Client
	switch providerID {
	case string(catwalk.InferenceProviderCopilot):
		httpClient = copilot.NewClient(isSubAgent, c.cfg.Config().Options.Debug)
	default:
		httpClient = log.NewIdleTimeoutClient()
		if c.cfg.Config().Options.Debug {
			httpClient = log.NewHTTPClient()
		}
	}
	opts = append(opts, openaicompat.WithHTTPClient(httpClient))

	if len(headers) > 0 {
		opts = append(opts, openaicompat.WithHeaders(headers))
	}

	for extraKey, extraValue := range extraBody {
		opts = append(opts, openaicompat.WithSDKOptions(openaisdk.WithJSONSet(extraKey, extraValue)))
	}

	return openaicompat.New(opts...)
}

func (c *coordinator) buildAzureProvider(baseURL, apiKey string, headers map[string]string, options map[string]string) (fantasy.Provider, error) {
	opts := []azure.Option{
		azure.WithBaseURL(baseURL),
		azure.WithAPIKey(apiKey),
		azure.WithUseResponsesAPI(),
	}
	httpClient := log.NewIdleTimeoutClient()
	if c.cfg.Config().Options.Debug {
		httpClient = log.NewHTTPClient()
	}
	opts = append(opts, azure.WithHTTPClient(httpClient))
	if options == nil {
		options = make(map[string]string)
	}
	if apiVersion, ok := options["apiVersion"]; ok {
		opts = append(opts, azure.WithAPIVersion(apiVersion))
	}
	if len(headers) > 0 {
		opts = append(opts, azure.WithHeaders(headers))
	}

	return azure.New(opts...)
}

func (c *coordinator) buildBedrockProvider(apiKey string, headers map[string]string, providerID string) (fantasy.Provider, error) {
	var opts []bedrock.Option
	httpClient := log.NewIdleTimeoutClient()
	if c.cfg.Config().Options.Debug {
		httpClient = log.NewHTTPClient()
	}
	opts = append(opts, bedrock.WithHTTPClient(httpClient))
	if len(headers) > 0 {
		opts = append(opts, bedrock.WithHeaders(headers))
	}

	switch {
	case apiKey != "":
		opts = append(opts, bedrock.WithAPIKey(apiKey))
	case os.Getenv("AWS_BEARER_TOKEN_BEDROCK") != "":
		opts = append(opts, bedrock.WithAPIKey(os.Getenv("AWS_BEARER_TOKEN_BEDROCK")))
	default:
		// Skip, let the SDK do authentication.
	}

	switch providerID {
	case string(catwalk.InferenceProviderBedrockEurope):
		opts = append(opts, bedrock.WithRegion("eu-west-1"))
	default:
		opts = append(opts, bedrock.WithRegion("us-east-1"))
	}

	return bedrock.New(opts...)
}

func (c *coordinator) buildGoogleProvider(baseURL, apiKey string, headers map[string]string) (fantasy.Provider, error) {
	opts := []google.Option{
		google.WithBaseURL(baseURL),
		google.WithGeminiAPIKey(apiKey),
	}
	httpClient := log.NewIdleTimeoutClient()
	if c.cfg.Config().Options.Debug {
		httpClient = log.NewHTTPClient()
	}
	opts = append(opts, google.WithHTTPClient(httpClient))
	if len(headers) > 0 {
		opts = append(opts, google.WithHeaders(headers))
	}
	return google.New(opts...)
}

func (c *coordinator) buildGoogleVertexProvider(headers map[string]string, options map[string]string) (fantasy.Provider, error) {
	opts := []google.Option{}
	httpClient := log.NewIdleTimeoutClient()
	if c.cfg.Config().Options.Debug {
		httpClient = log.NewHTTPClient()
	}
	opts = append(opts, google.WithHTTPClient(httpClient))
	if len(headers) > 0 {
		opts = append(opts, google.WithHeaders(headers))
	}

	project := options["project"]
	location := options["location"]

	opts = append(opts, google.WithVertex(project, location))

	return google.New(opts...)
}

func (c *coordinator) isAnthropicThinking(model config.ProviderModel, think bool) bool {
	if think {
		return true
	}
	opts, err := anthropic.ParseOptions(model.Options.ProviderOptions)
	return err == nil && opts.Thinking != nil
}

func (c *coordinator) buildProvider(providerCfg config.ProviderConfig, model config.ProviderModel, think bool, isSubAgent bool) (fantasy.Provider, error) {
	headers := maps.Clone(providerCfg.ExtraHeaders)
	if headers == nil {
		headers = make(map[string]string)
	}

	// handle special headers for anthropic
	if providerCfg.Type == anthropic.Name && c.isAnthropicThinking(model, think) {
		if v, ok := headers["anthropic-beta"]; ok {
			headers["anthropic-beta"] = v + ",interleaved-thinking-2025-05-14"
		} else {
			headers["anthropic-beta"] = "interleaved-thinking-2025-05-14"
		}
	}

	apiKey, _ := c.cfg.ProviderFieldResolver(providerCfg.ID, "api_key").ResolveValue(providerCfg.APIKey)
	baseURL, _ := c.cfg.ProviderFieldResolver(providerCfg.ID, "base_url").ResolveValue(providerCfg.BaseURL)

	switch providerCfg.ID {
	case string(catwalk.InferenceProviderOpenCodeGo), string(catwalk.InferenceProviderOpenCodeZen):
		if opencodeMessagesModels[model.ID] {
			return c.buildAnthropicProvider(baseURL, apiKey, headers, providerCfg.ID)
		}
	}

	switch providerCfg.Type {
	case openai.Name:
		return c.buildOpenaiProvider(baseURL, apiKey, headers, effectiveUseResponses(providerCfg, model))
	case anthropic.Name:
		return c.buildAnthropicProvider(baseURL, apiKey, headers, providerCfg.ID)
	case openrouter.Name:
		return c.buildOpenrouterProvider(baseURL, apiKey, headers)
	case vercel.Name:
		return c.buildVercelProvider(baseURL, apiKey, headers)
	case azure.Name:
		return c.buildAzureProvider(baseURL, apiKey, headers, providerCfg.ExtraParams)
	case bedrock.Name:
		return c.buildBedrockProvider(apiKey, headers, providerCfg.ID)
	case google.Name:
		return c.buildGoogleProvider(baseURL, apiKey, headers)
	case "google-vertex":
		return c.buildGoogleVertexProvider(headers, providerCfg.ExtraParams)
	case openaicompat.Name:
		switch providerCfg.ID {
		case string(catwalk.InferenceProviderZAI):
			// providerCfg is a value copy, but ExtraBody still aliases
			// the published config snapshot that other turns read.
			providerCfg.ExtraBody = maps.Clone(providerCfg.ExtraBody)
			if providerCfg.ExtraBody == nil {
				providerCfg.ExtraBody = map[string]any{}
			}
			providerCfg.ExtraBody["tool_stream"] = true
		}
		return c.buildOpenaiCompatProvider(baseURL, apiKey, headers, providerCfg.ExtraBody, providerCfg.ID, isSubAgent, effectiveUseResponses(providerCfg, model))
	default:
		// Known custom providers (litellm, ollama, omlx) are
		// openai-compat under the hood.
		if discover.IsKnownCustomProvider(string(providerCfg.Type)) {
			return c.buildOpenaiCompatProvider(baseURL, apiKey, headers, providerCfg.ExtraBody, providerCfg.ID, isSubAgent, effectiveUseResponses(providerCfg, model))
		}
		return nil, fmt.Errorf("provider type not supported: %q", providerCfg.Type)
	}
}

func isExactoSupported(modelID string) bool {
	supportedModels := []string{
		"moonshotai/kimi-k2-0905",
		"deepseek/deepseek-v3.1-terminus",
		"z-ai/glm-4.6",
		"openai/gpt-oss-120b",
		"qwen/qwen3-coder",
	}
	return slices.Contains(supportedModels, modelID)
}
