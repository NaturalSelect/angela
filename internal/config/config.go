package config

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/oauth"
	"github.com/NaturalSelect/angela/internal/oauth/copilot"
	"github.com/invopop/jsonschema"
)

const (
	appName              = "angela"
	defaultDataDirectory = ".angela"
	defaultInitializeAs  = "AGENTS.md"
)

var defaultContextPaths = []string{
	".github/copilot-instructions.md",
	".cursorrules",
	".cursor/rules/",
	"CLAUDE.md",
	"CLAUDE.local.md",
	"GEMINI.md",
	"gemini.md",
	"angela.md",
	"angela.local.md",
	"Angela.md",
	"Angela.local.md",
	"ANGELA.md",
	"ANGELA.local.md",
	"AGENTS.md",
	"agents.md",
	"Agents.md",
}

// ModelConfigName names a model configuration slot. The two seeds below
// ship with Angela; users may define any other name and point an agent
// at it via the agent's Model field.
type ModelConfigName string

// String returns the string representation of the [ModelConfigName].
func (s ModelConfigName) String() string {
	return string(s)
}

const (
	// ModelMain is the workhorse model configuration.
	ModelMain ModelConfigName = "main"
	// ModelChore is the cheap model configuration used for auxiliary
	// work such as titles and summaries.
	ModelChore ModelConfigName = "chore"
)

const (
	AgentCoder   string = "coder"
	AgentExplore string = "explore"
	AgentGeneral string = "general"

	// The agents below back Angela's own auxiliary LLM calls. They are
	// hidden — resolvable by ID, but never offered for dispatch or
	// completion — so a user can retune their model and prompt without
	// them showing up as things to delegate to.
	AgentTitle         string = "title"
	AgentCompact       string = "compact"
	AgentAgenticFetch  string = "agentic-fetch"
	AgentGenerateAgent string = "generate-agent"
	AgentInitialize    string = "initialize"
)

func ptr[T any](v T) *T { return &v }

// AgentMode determines how an agent can be used.
type AgentMode string

const (
	// AgentModePrimary means the agent drives a session directly: it is
	// what a session can be switched to, and it is never dispatched via
	// the agent tool.
	AgentModePrimary AgentMode = "primary"
	// AgentModeSubagent means the agent can only be launched via the
	// agent tool.
	AgentModeSubagent AgentMode = "subagent"
)

type SelectedModel struct {
	// The model id as used by the provider API.
	// Required.
	Model string `json:"model" jsonschema:"required,description=The model ID as used by the provider API,example=gpt-4o"`
	// The model provider, same as the key/id used in the providers config.
	// Required.
	Provider string `json:"provider" jsonschema:"required,description=The model provider ID that matches a key in the providers config,example=openai"`

	// Only used by models that use the openai provider and need this set.
	ReasoningEffort string `json:"reasoning_effort,omitempty" jsonschema:"description=Reasoning effort level for OpenAI models that support it,enum=low,enum=medium,enum=high"`

	// Used by anthropic models that can reason to indicate if the model should think.
	Think bool `json:"think,omitempty" jsonschema:"description=Enable thinking mode for Anthropic models that support reasoning"`

	// Overrides the default model configuration.
	MaxTokens        int64    `json:"max_tokens,omitempty" jsonschema:"description=Maximum number of tokens for model responses,maximum=200000,example=4096"`
	Temperature      *float64 `json:"temperature,omitempty" jsonschema:"description=Sampling temperature,minimum=0,maximum=1,example=0.7"`
	TopP             *float64 `json:"top_p,omitempty" jsonschema:"description=Top-p (nucleus) sampling parameter,minimum=0,maximum=1,example=0.9"`
	TopK             *int64   `json:"top_k,omitempty" jsonschema:"description=Top-k sampling parameter"`
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty" jsonschema:"description=Frequency penalty to reduce repetition"`
	PresencePenalty  *float64 `json:"presence_penalty,omitempty" jsonschema:"description=Presence penalty to increase topic diversity"`

	// Override provider specific options.
	ProviderOptions map[string]any `json:"provider_options,omitempty" jsonschema:"description=Additional provider-specific options for the model"`

	// Variants are named parameter presets over the fields above. They
	// keep the model identity and override only the keys they name, so
	// N models by M presets stays N+M configs instead of N*M.
	Variants map[string]SelectedModelOverride `json:"variants,omitempty" jsonschema:"description=Named parameter presets layered over this model config"`
}

type ProviderConfig struct {
	// The provider's id.
	ID string `json:"id,omitempty" jsonschema:"description=Unique identifier for the provider,example=openai"`
	// The provider's name, used for display purposes.
	Name string `json:"name,omitempty" jsonschema:"description=Human-readable name for the provider,example=OpenAI"`
	// The provider's API endpoint.
	BaseURL string `json:"base_url,omitempty" jsonschema:"description=Base URL for the provider's API,format=uri,example=https://api.openai.com/v1"`
	// The provider type, e.g. "openai", "anthropic", etc. if empty it defaults to openai.
	Type catwalk.Type `json:"type,omitempty" jsonschema:"description=Provider type that determines the API format,default=openai"`
	// The provider's API key.
	APIKey string `json:"api_key,omitempty" jsonschema:"description=API key for authentication with the provider,example=$OPENAI_API_KEY"`
	// The original API key template before resolution (for re-resolution on auth errors).
	APIKeyTemplate string `json:"-"`
	// OAuthToken for providers that use OAuth2 authentication.
	OAuthToken *oauth.Token `json:"oauth,omitempty" jsonschema:"description=OAuth2 token for authentication with the provider"`
	// Marks the provider as disabled.
	Disable bool `json:"disable,omitempty" jsonschema:"description=Whether this provider is disabled,default=false"`

	// Custom system prompt prefix.
	SystemPromptPrefix string `json:"system_prompt_prefix,omitempty" jsonschema:"description=Custom prefix to add to system prompts for this provider"`

	// Extra headers to send with each request to the provider. Values
	// run through shell expansion at config-load time, so $VAR and
	// $(cmd) work the same way they do in MCP headers. A header whose
	// value resolves to the empty string (unset bare $VAR under
	// lenient nounset, $(echo), or literal "") is omitted from the
	// outgoing request rather than sent as "Header:".
	ExtraHeaders map[string]string `json:"extra_headers,omitempty" jsonschema:"description=Additional HTTP headers to send with requests"`
	// ExtraBody is merged verbatim into OpenAI-compatible request
	// bodies. String values are NOT shell-expanded: this is a plain
	// JSON passthrough so that arbitrary provider-extension fields
	// (numbers, nested objects, booleans) round-trip without a
	// recursive walker guessing at intent. If you need an env-var-
	// driven value at request time, put it in extra_headers, or in
	// the provider's top-level api_key / base_url, all of which do
	// expand.
	ExtraBody map[string]any `json:"extra_body,omitempty" jsonschema:"description=Additional fields to include in request bodies\\, only works with openai-compatible providers"`

	ProviderOptions map[string]any `json:"provider_options,omitempty" jsonschema:"description=Additional provider-specific options for this provider"`

	// Used to pass extra parameters to the provider.
	ExtraParams map[string]string `json:"-"`

	// AWSAuthRefresh is a shell command run when Bedrock returns a
	// credential error. Output is discarded to avoid corrupting the TUI.
	AWSAuthRefresh string `json:"aws_auth_refresh,omitempty" jsonschema:"description=Shell command to run when AWS credentials expire (Bedrock only)."`

	// Skip cost accumulation for this provider when using subscription or flat rate billing.
	FlatRate bool `json:"flat_rate,omitempty" jsonschema:"description=Flat-rate mode for this provider"`

	// AutoDiscoverModels controls model discovery via /v1/models endpoint.
	// When Models is empty and this is nil or true, Angela auto-discovers
	// models. When true and Models is non-empty, discovered models are
	// merged in (user-specified models take precedence). When false,
	// only explicitly listed models are used.
	AutoDiscoverModels *bool `json:"discover_models,omitempty" jsonschema:"description=Auto-discover models from /v1/models endpoint. When true with existing models they are merged (yours win),default=true"`

	// The provider models
	Models []catwalk.Model `json:"models,omitempty" jsonschema:"description=List of models available from this provider"`
}

// ToProvider converts the [ProviderConfig] to a [catwalk.Provider].
func (c *ProviderConfig) ToProvider() catwalk.Provider {
	// Convert config provider to provider.Provider format
	provider := catwalk.Provider{
		Name:   c.Name,
		ID:     catwalk.InferenceProvider(c.ID),
		Models: make([]catwalk.Model, len(c.Models)),
	}

	// Convert models
	for i, model := range c.Models {
		provider.Models[i] = catwalk.Model{
			ID:                     model.ID,
			Name:                   model.Name,
			CostPer1MIn:            model.CostPer1MIn,
			CostPer1MOut:           model.CostPer1MOut,
			CostPer1MInCached:      model.CostPer1MInCached,
			CostPer1MOutCached:     model.CostPer1MOutCached,
			ContextWindow:          model.ContextWindow,
			DefaultMaxTokens:       model.DefaultMaxTokens,
			CanReason:              model.CanReason,
			ReasoningLevels:        model.ReasoningLevels,
			DefaultReasoningEffort: model.DefaultReasoningEffort,
			SupportsImages:         model.SupportsImages,
		}
	}

	return provider
}

func (c *ProviderConfig) SetupGitHubCopilot() {
	maps.Copy(c.ExtraHeaders, copilot.Headers())
}

type MCPType string

const (
	MCPStdio MCPType = "stdio"
	MCPSSE   MCPType = "sse"
	MCPHttp  MCPType = "http"
)

type MCPConfig struct {
	Command       string            `json:"command,omitempty" jsonschema:"description=Command to execute for stdio MCP servers,example=npx"`
	Env           map[string]string `json:"env,omitempty" jsonschema:"description=Environment variables to set for the MCP server"`
	Args          []string          `json:"args,omitempty" jsonschema:"description=Arguments to pass to the MCP server command"`
	Type          MCPType           `json:"type" jsonschema:"required,description=Type of MCP connection,enum=stdio,enum=sse,enum=http,default=stdio"`
	URL           string            `json:"url,omitempty" jsonschema:"description=URL for HTTP or SSE MCP servers,format=uri,example=http://localhost:3000/mcp"`
	Disabled      bool              `json:"disabled,omitempty" jsonschema:"description=Whether this MCP server is disabled,default=false"`
	DisabledTools []string          `json:"disabled_tools,omitempty" jsonschema:"description=List of tools from this MCP server to disable,example=get-library-doc"`
	EnabledTools  []string          `json:"enabled_tools,omitempty" jsonschema:"description=Allow list of tools from this MCP server,example=get-library-doc"`
	Timeout       int               `json:"timeout,omitempty" jsonschema:"description=Timeout in seconds for MCP server connections,default=10,example=30,example=60,example=120"`

	// Headers are HTTP headers for HTTP/SSE MCP servers. Values run
	// through shell expansion at MCP startup, so $VAR and $(cmd)
	// work. A header whose value resolves to the empty string (unset
	// bare $VAR under lenient nounset, $(echo), or literal "") is
	// omitted from the outgoing request rather than sent as
	// "Header:".
	Headers map[string]string `json:"headers,omitempty" jsonschema:"description=HTTP headers for HTTP/SSE MCP servers"`

	// OAuth enables the MCP OAuth 2.1 authorization flow for HTTP
	// transport servers. When true, the client uses dynamic client
	// registration and opens a browser for the user to authorize.
	// Tokens are persisted automatically. Only supported for type=http.
	OAuth bool `json:"oauth,omitempty" jsonschema:"description=Enable OAuth 2.1 authorization flow for this MCP server (HTTP transport only),default=false"`

	// OAuthClientID is an optional pre-registered OAuth client ID. Set
	// it for servers that do not support dynamic client registration
	// (e.g. GitHub, Slack) and instead issue client credentials when you
	// register an OAuth app. Values run through shell expansion, so
	// $VAR and $(cmd) work.
	OAuthClientID string `json:"oauth_client_id,omitempty" jsonschema:"description=Pre-registered OAuth client ID for servers without dynamic client registration"`

	// OAuthClientSecret is the optional secret paired with
	// OAuthClientID for confidential clients. Values run through shell
	// expansion, so $VAR and $(cmd) work.
	OAuthClientSecret string `json:"oauth_client_secret,omitempty" jsonschema:"description=Pre-registered OAuth client secret paired with oauth_client_id"`

	// OAuthCallbackPort pins the localhost port used for the OAuth
	// redirect listener. Set this when the OAuth provider requires an
	// exact-match callback URL (e.g. GitHub OAuth Apps). When omitted,
	// Angela picks the first free port from its default range.
	OAuthCallbackPort int `json:"oauth_callback_port,omitempty" jsonschema:"description=Fixed localhost port for the OAuth callback, required by providers that enforce exact-match redirect URIs"`

	// OAuthToken is the persisted OAuth token for this server. It is
	// managed internally and stored in the global data config.
	OAuthToken *oauth.Token `json:"oauth_token,omitempty" jsonschema:"-"`
}

// isOrphanedToken reports whether this entry is a leftover OAuth token
// with no real server config.
func (m MCPConfig) isOrphanedToken() bool {
	return m.Type == "" && m.Command == "" && m.URL == "" && m.OAuthToken != nil
}

type LSPConfig struct {
	Disabled    bool              `json:"disabled,omitempty" jsonschema:"description=Whether this LSP server is disabled,default=false"`
	Command     string            `json:"command,omitempty" jsonschema:"description=Command to execute for the LSP server,example=gopls"`
	Args        []string          `json:"args,omitempty" jsonschema:"description=Arguments to pass to the LSP server command"`
	Env         map[string]string `json:"env,omitempty" jsonschema:"description=Environment variables to set to the LSP server command"`
	FileTypes   []string          `json:"filetypes,omitempty" jsonschema:"description=File types this LSP server handles,example=go,example=mod,example=rs,example=c,example=js,example=ts"`
	RootMarkers []string          `json:"root_markers,omitempty" jsonschema:"description=Files or directories that indicate the project root,example=go.mod,example=package.json,example=Cargo.toml"`
	InitOptions map[string]any    `json:"init_options,omitempty" jsonschema:"description=Initialization options passed to the LSP server during initialize request"`
	Options     map[string]any    `json:"options,omitempty" jsonschema:"description=LSP server-specific settings passed during initialization"`
	Timeout     int               `json:"timeout,omitempty" jsonschema:"description=Timeout in seconds for LSP server initialization,default=30,example=60,example=120"`
}

type TUIOptions struct {
	CompactMode bool   `json:"compact_mode,omitempty" jsonschema:"description=Enable compact mode for the TUI interface,default=false"`
	DiffMode    string `json:"diff_mode,omitempty" jsonschema:"description=Diff mode for the TUI interface,enum=unified,enum=split"`
	// Here we can add themes later or any TUI related options
	//

	Completions Completions `json:"completions,omitzero" jsonschema:"description=Completions UI options"`
	Transparent *bool       `json:"transparent,omitempty" jsonschema:"description=Enable transparent background for the TUI interface,default=false"`
	Scrollbar   string      `json:"scrollbar,omitempty" jsonschema:"description=Chat scrollbar visibility,enum=default,enum=always,enum=never,default=default"`
}

// Completions defines options for the completions UI.
type Completions struct {
	MaxDepth *int `json:"max_depth,omitempty" jsonschema:"description=Maximum depth for the ls tool,default=0,example=10"`
	MaxItems *int `json:"max_items,omitempty" jsonschema:"description=Maximum number of items to return for the ls tool,default=1000,example=100"`
}

// Compaction defaults. A context window past
// CompactionLargeContextThreshold gets a fixed headroom reserve; a
// smaller one reserves a proportion of itself instead, because a fixed
// 20k reserve would swallow most of a 32k window.
const (
	CompactionLargeContextThreshold int64   = 200_000
	CompactionReserved              int64   = 20_000
	CompactionSmallContextRatio     float64 = 0.2
)

// CompactionOptions tunes when a conversation is automatically
// summarized to free context.
type CompactionOptions struct {
	Auto                  *bool    `json:"auto,omitempty" jsonschema:"description=Automatically compact when the context fills up,default=true"`
	LargeContextThreshold *int64   `json:"large_context_threshold,omitempty" jsonschema:"description=Context window size above which Reserved is used instead of SmallContextRatio,default=200000"`
	Reserved              *int64   `json:"reserved,omitempty" jsonschema:"description=Tokens kept free for the next turn on a large context window,default=20000"`
	SmallContextRatio     *float64 `json:"small_context_ratio,omitempty" jsonschema:"description=Proportion of a small context window kept free for the next turn,default=0.2"`
}

// AutoCompact reports whether automatic compaction is on. A nil
// receiver means "not configured", which is the default: on.
func (c *CompactionOptions) AutoCompact() bool {
	if c == nil || c.Auto == nil {
		return true
	}
	return *c.Auto
}

// ReserveFor returns how many tokens of a context window to keep free
// for the next turn.
func (c *CompactionOptions) ReserveFor(contextWindow int64) int64 {
	threshold := CompactionLargeContextThreshold
	reserved := CompactionReserved
	ratio := CompactionSmallContextRatio
	if c != nil {
		threshold = ptrValOr(c.LargeContextThreshold, threshold)
		reserved = ptrValOr(c.Reserved, reserved)
		ratio = ptrValOr(c.SmallContextRatio, ratio)
	}
	if contextWindow > threshold {
		return reserved
	}
	return int64(float64(contextWindow) * ratio)
}

func (c Completions) Limits() (depth, items int) {
	return ptrValOr(c.MaxDepth, 0), ptrValOr(c.MaxItems, 0)
}

// Scrollbar visibility options.
const (
	ScrollbarDefault = "default" // Auto-hide after 2 seconds
	ScrollbarAlways  = "always"  // Always show when content exceeds viewport
	ScrollbarNever   = "never"   // Never show scrollbar
)

type Permissions struct {
	AllowedTools []string `json:"allowed_tools,omitempty" jsonschema:"description=List of tools that don't require permission prompts,example=bash,example=view"`
}

type TrailerStyle string

const (
	TrailerStyleNone         TrailerStyle = "none"
	TrailerStyleCoAuthoredBy TrailerStyle = "co-authored-by"
	TrailerStyleAssistedBy   TrailerStyle = "assisted-by"
)

type Attribution struct {
	TrailerStyle  TrailerStyle `json:"trailer_style,omitempty" jsonschema:"description=Style of attribution trailer to add to commits,enum=none,enum=co-authored-by,enum=assisted-by,default=assisted-by"`
	CoAuthoredBy  *bool        `json:"co_authored_by,omitempty" jsonschema:"description=Deprecated: use trailer_style instead"`
	GeneratedWith bool         `json:"generated_with,omitempty" jsonschema:"description=Add Generated with Angela line to commit messages and issues and PRs,default=true"`
}

// JSONSchemaExtend marks the co_authored_by field as deprecated in the schema.
func (Attribution) JSONSchemaExtend(schema *jsonschema.Schema) {
	if schema.Properties != nil {
		if prop, ok := schema.Properties.Get("co_authored_by"); ok {
			prop.Deprecated = true
		}
	}
}

type Options struct {
	ContextPaths       []string           `json:"context_paths,omitempty" jsonschema:"description=Paths to files containing context information for the AI,example=.cursorrules,example=ANGELA.md"`
	GlobalContextPaths []string           `json:"global_context_paths,omitempty" jsonschema:"description=Paths to files containing global context information for the AI,default=~/.config/angela/ANGELA.md,default=~/.config/AGENTS.md"`
	SkillsPaths        []string           `json:"skills_paths,omitempty" jsonschema:"description=Paths to directories containing Agent Skills (folders with SKILL.md files),example=~/.config/angela/skills,example=./skills"`
	TUI                *TUIOptions        `json:"tui,omitempty" jsonschema:"description=Terminal user interface options"`
	Compaction         *CompactionOptions `json:"compaction,omitempty" jsonschema:"description=Conversation compaction options"`
	Debug              bool               `json:"debug,omitempty" jsonschema:"description=Enable debug logging,default=false"`
	DebugLSP           bool               `json:"debug_lsp,omitempty" jsonschema:"description=Enable debug logging for LSP servers,default=false"`
	// DataDirectory is where Angela keeps per-project state such as
	// the SQLite database and workspace overrides. Relative paths are
	// resolved against the working directory; absolute paths are used
	// verbatim. After defaulting the stored value is always absolute.
	DataDirectory             string       `json:"data_directory,omitempty" jsonschema:"description=Directory for storing application data. Relative paths are resolved against the working directory; absolute paths are used as-is.,default=.angela,example=.angela"`
	DisabledTools             []string     `json:"disabled_tools,omitempty" jsonschema:"description=List of built-in tools to disable and hide from the agent,example=bash,example=sourcegraph"`
	DisableProviderAutoUpdate bool         `json:"disable_provider_auto_update,omitempty" jsonschema:"description=Disable providers auto-update,default=false"`
	DisableDefaultProviders   bool         `json:"disable_default_providers,omitempty" jsonschema:"description=Ignore all default/embedded providers. When enabled\\, providers must be fully specified in the config file with base_url\\, models\\, and api_key - no merging with defaults occurs,default=false"`
	Attribution               *Attribution `json:"attribution,omitempty" jsonschema:"description=Attribution settings for generated content"`
	DisableMetrics            bool         `json:"disable_metrics,omitempty" jsonschema:"description=Disable sending metrics,default=false"`
	InitializeAs              string       `json:"initialize_as,omitempty" jsonschema:"description=Name of the context file to create/update during project initialization,default=AGENTS.md,example=AGENTS.md,example=ANGELA.md,example=CLAUDE.md,example=docs/LLMs.md"`
	AutoLSP                   *bool        `json:"auto_lsp,omitempty" jsonschema:"description=Automatically setup LSPs based on root markers,default=true"`
	Progress                  *bool        `json:"progress,omitempty" jsonschema:"description=Show indeterminate progress updates during long operations,default=true"`
	Notifications             string       `json:"notifications,omitempty" jsonschema:"description=Notification style to use. Options: auto (default)\\, native\\, osc\\, bell\\, disabled. Auto selects based on environment: native for local sessions\\, osc for SSH (with automatic OSC 99/777 detection).,enum=auto,enum=native,enum=osc,enum=bell,enum=disabled,default=auto"`
	DisabledSkills            []string     `json:"disabled_skills,omitempty" jsonschema:"description=List of skill names to disable and hide from the agent,example=angela-config"`
	AgentPaths                []string     `json:"agent_paths,omitempty" jsonschema:"description=Paths to directories containing agent markdown files,example=~/.config/angela/agents,example=./agents"`
}

type MCPs map[string]MCPConfig

type MCP struct {
	Name string    `json:"name"`
	MCP  MCPConfig `json:"mcp"`
}

func (m MCPs) Sorted() []MCP {
	sorted := make([]MCP, 0, len(m))
	for k, v := range m {
		sorted = append(sorted, MCP{
			Name: k,
			MCP:  v,
		})
	}
	slices.SortFunc(sorted, func(a, b MCP) int {
		return strings.Compare(a.Name, b.Name)
	})
	return sorted
}

type LSPs map[string]LSPConfig

type LSP struct {
	Name string    `json:"name"`
	LSP  LSPConfig `json:"lsp"`
}

func (l LSPs) Sorted() []LSP {
	sorted := make([]LSP, 0, len(l))
	for k, v := range l {
		sorted = append(sorted, LSP{
			Name: k,
			LSP:  v,
		})
	}
	slices.SortFunc(sorted, func(a, b LSP) int {
		return strings.Compare(a.Name, b.Name)
	})
	return sorted
}

// ResolvedEnv returns m.Env with every value expanded through the
// given resolver. The returned slice is of the form "KEY=value" sorted
// by key so callers get deterministic output; the receiver's Env map is
// not mutated. On the first resolution failure it returns nil and an
// error that identifies the offending key; the inner resolver error is
// already sanitized by ResolveValue and is wrapped with %w so
// errors.Is/As continues to work. Callers are expected to surface it
// (for MCP, via StateError on the status card) rather than silently
// spawn the server with an empty credential.
//
// The resolver choice matters: in server mode pass the shell resolver
// so $VAR / $(cmd) expand; in client mode pass IdentityResolver so the
// template is forwarded verbatim and expansion happens on the server.
func (m MCPConfig) ResolvedEnv(r VariableResolver) ([]string, error) {
	return resolveEnvs(m.Env, r)
}

// ResolvedArgs returns m.Args with every element expanded through the
// given resolver. A fresh slice is allocated; m.Args is never mutated.
// On the first resolution failure it returns nil and an error
// identifying the offending positional index; the inner resolver error
// is already sanitized by ResolveValue and is wrapped with %w so
// errors.Is/As continues to work.
//
// See ResolvedEnv for guidance on picking a resolver.
func (m MCPConfig) ResolvedArgs(r VariableResolver) ([]string, error) {
	if len(m.Args) == 0 {
		return nil, nil
	}
	out := make([]string, len(m.Args))
	for i, a := range m.Args {
		v, err := r.ResolveValue(a)
		if err != nil {
			return nil, fmt.Errorf("arg %d: %w", i, err)
		}
		out[i] = v
	}
	return out, nil
}

// ResolvedURL returns m.URL expanded through the given resolver. The
// receiver is not mutated. Errors from the resolver are already
// sanitized by ResolveValue and are wrapped with %w for errors.Is/As.
//
// URLs run through the same shell-expansion pipeline as the other
// fields, so a literal '$' (e.g. OData query strings containing
// $filter/$select) must be escaped as '\$' or '${DOLLAR:-$}' to avoid
// being interpreted as a variable reference. Same constraint already
// applies to command, args, env, and headers.
//
// See ResolvedEnv for guidance on picking a resolver.
func (m MCPConfig) ResolvedURL(r VariableResolver) (string, error) {
	if m.URL == "" {
		return "", nil
	}
	v, err := r.ResolveValue(m.URL)
	if err != nil {
		return "", fmt.Errorf("url: %w", err)
	}
	return v, nil
}

// ResolvedHeaders returns m.Headers with every value expanded through
// the given resolver. A fresh map is allocated; m.Headers is never
// mutated. On the first resolution failure it returns nil and an error
// identifying the offending header name; the inner resolver error is
// already sanitized by ResolveValue and is wrapped with %w so
// errors.Is/As continues to work.
//
// A header whose value resolves to the empty string (unset bare $VAR
// under lenient nounset, $(echo), or literal "") is omitted from the
// returned map — sending "X-Auth:" with an empty value is rejected by
// some providers and the user's intent in "optional, env-gated
// header" is clearly "absent when the var isn't set."
//
// See ResolvedEnv for guidance on picking a resolver.
func (m MCPConfig) ResolvedHeaders(r VariableResolver) (map[string]string, error) {
	if len(m.Headers) == 0 {
		return map[string]string{}, nil
	}
	out := make(map[string]string, len(m.Headers))
	// Sort keys so failures are reported deterministically when more
	// than one header would fail.
	keys := make([]string, 0, len(m.Headers))
	for k := range m.Headers {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		v, err := r.ResolveValue(m.Headers[k])
		if err != nil {
			return nil, fmt.Errorf("header %s: %w", k, err)
		}
		if v == "" {
			continue
		}
		out[k] = v
	}
	return out, nil
}

// ResolvedArgs returns l.Args with every element expanded through the
// given resolver. A fresh slice is allocated; l.Args is never mutated.
// On the first resolution failure it returns nil and an error
// identifying the offending positional index; the inner resolver error
// is already sanitized by ResolveValue and is wrapped with %w so
// errors.Is/As continues to work.
//
// Empty resolved values are kept (a deliberate "empty positional arg"
// like --flag "" is sometimes valid), matching MCPConfig.ResolvedArgs.
//
// The resolver choice matters: in server mode pass the shell resolver
// so $VAR / $(cmd) expand; in client mode pass IdentityResolver so the
// template is forwarded verbatim.
func (l LSPConfig) ResolvedArgs(r VariableResolver) ([]string, error) {
	if len(l.Args) == 0 {
		return nil, nil
	}
	out := make([]string, len(l.Args))
	for i, a := range l.Args {
		v, err := r.ResolveValue(a)
		if err != nil {
			return nil, fmt.Errorf("arg %d: %w", i, err)
		}
		out[i] = v
	}
	return out, nil
}

// ResolvedEnv returns l.Env with every value expanded through the
// given resolver. A fresh map is allocated; l.Env is never mutated.
// On the first resolution failure it returns nil and an error that
// identifies the offending key; the inner resolver error is already
// sanitized by ResolveValue and is wrapped with %w so errors.Is/As
// continues to work.
//
// Empty resolved values are kept ("FOO=" is a legitimate request;
// opt out via ${VAR:+...}), matching MCPConfig.ResolvedEnv.
//
// Shape note: this returns map[string]string rather than the []string
// shape MCPConfig.ResolvedEnv uses because the consumer
// (powernap.ClientConfig.Environment in internal/lsp/client.go) takes
// a map directly — returning a []string here would only force a
// round-trip back to a map at the call site.
//
// See ResolvedArgs for guidance on picking a resolver.
func (l LSPConfig) ResolvedEnv(r VariableResolver) (map[string]string, error) {
	if len(l.Env) == 0 {
		return map[string]string{}, nil
	}
	out := make(map[string]string, len(l.Env))
	// Sort keys so failures are reported deterministically when more
	// than one value would fail.
	keys := make([]string, 0, len(l.Env))
	for k := range l.Env {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		v, err := r.ResolveValue(l.Env[k])
		if err != nil {
			return nil, fmt.Errorf("env %q: %w", k, err)
		}
		out[k] = v
	}
	return out, nil
}

type Agent struct {
	ID          string `json:"id,omitempty" jsonschema:"description=Unique identifier for the agent"`
	Name        string `json:"name,omitempty" jsonschema:"description=Human-readable display name"`
	Description string `json:"description,omitempty" jsonschema:"description=What this agent does"`

	// Disabled uses a pointer so a markdown-layer "disabled: true" can
	// be explicitly re-enabled by a higher-priority layer's
	// "disabled: false", instead of false being indistinguishable
	// from unset.
	Disabled *bool `json:"disabled,omitempty" jsonschema:"description=Whether this agent is disabled"`

	// Hidden keeps an agent out of the agent tool's dispatch list and
	// out of UI completion, while leaving it resolvable by ID. It is a
	// pointer for the same reason as Disabled: so a user can un-hide a
	// built-in hidden agent with an explicit "hidden: false".
	//
	// Hidden is orthogonal to Mode: whether an agent is internal has
	// nothing to do with whether it is primary or a subagent.
	Hidden *bool `json:"hidden,omitempty" jsonschema:"description=Keep this agent out of the agent tool's dispatch list and UI completion"`

	// Mode controls how the agent can be used. Primary agents are
	// top-level; subagents are launched via the agent tool.
	Mode AgentMode `json:"mode,omitempty" jsonschema:"description=Agent mode: primary or subagent,enum=primary,enum=subagent"`

	Model ModelConfigName `json:"model,omitempty" jsonschema:"description=Name of the model config to use,default=main"`

	// Variant names a parameter preset on the model config above.
	// Unknown names degrade to the model's baseline parameters.
	Variant string `json:"variant,omitempty" jsonschema:"description=Name of a variant on the model config"`

	// MaxTokens caps the agent's output tokens. Zero means the model
	// default applies.
	MaxTokens *int64 `json:"max_tokens,omitempty" jsonschema:"description=Cap on the agent's output tokens; zero means the model default"`

	// Prompt is the system prompt text. When set it replaces the
	// built-in template for this agent. The text is parsed as a Go
	// template with the same data as built-in templates.
	Prompt string `json:"prompt,omitempty" jsonschema:"description=Custom system prompt text (Go template)"`

	// Temperature overrides the model's default sampling temperature.
	Temperature *float64 `json:"temperature,omitempty" jsonschema:"description=Sampling temperature override,minimum=0,maximum=1"`

	// AllowedTools controls which tools this layer grants. A nil
	// value means this layer did not mention allowed_tools (the
	// merge keeps whatever a lower-priority layer set); a non-nil
	// value is self-describing via its Kind: ToolSetAll grants every
	// tool, ToolSetInherited takes the coder's resolved set, and
	// ToolSetScope grants only Tools. ResolveAgents' output is always
	// non-nil with Kind == ToolSetScope: a fully materialized
	// whitelist with every deny list already applied.
	AllowedTools *AllowedToolSet `json:"allowed_tools,omitempty" jsonschema:"description=Tools available to this agent: an array of names\\, \"all\"\\, or \"inherited\""`

	// DisabledTools removes tools from the resolved whitelist.
	DisabledTools []string `json:"disabled_tools,omitempty" jsonschema:"description=Tools to remove from the allowed set"`

	// AllowedMCP controls which MCP servers and tools are available,
	// with the same tri-state semantics as AllowedTools. nil means
	// this layer did not mention allowed_mcp.
	AllowedMCP *AllowedMCPSet `json:"allowed_mcp,omitempty" jsonschema:"description=MCP servers available to this agent: an object of server names\\, \"all\"\\, or \"inherited\""`

	// ContextPaths overrides the context paths for this agent.
	ContextPaths []string `json:"context_paths,omitempty" jsonschema:"description=Context file paths for this agent"`
}

// IsHidden reports whether the agent should stay out of dispatch lists
// and UI completion. Unset means visible.
func (a Agent) IsHidden() bool {
	return a.Hidden != nil && *a.Hidden
}

type Tools struct {
	Ls   ToolLs   `json:"ls,omitzero"`
	Grep ToolGrep `json:"grep,omitzero"`
	Glob ToolGlob `json:"glob,omitzero"`
}

type ToolLs struct {
	MaxDepth *int `json:"max_depth,omitempty" jsonschema:"description=Maximum depth for the ls tool,default=0,example=10"`
	MaxItems *int `json:"max_items,omitempty" jsonschema:"description=Maximum number of items to return for the ls tool,default=1000,example=100"`
}

// Limits returns the user-defined max-depth and max-items, or their defaults.
func (t ToolLs) Limits() (depth, items int) {
	return ptrValOr(t.MaxDepth, 0), ptrValOr(t.MaxItems, 0)
}

type ToolGrep struct {
	Timeout *time.Duration `json:"timeout,omitempty" jsonschema:"description=Timeout for the grep tool call,default=5s,example=10s"`
}

// GetTimeout returns the user-defined timeout or the default.
func (t ToolGrep) GetTimeout() time.Duration {
	return ptrValOr(t.Timeout, 5*time.Second)
}

type ToolGlob struct {
	Timeout *time.Duration `json:"timeout,omitempty" jsonschema:"description=Timeout for the glob tool call,default=30s,example=10s"`
}

// GetTimeout returns the user-defined timeout or the default.
func (t ToolGlob) GetTimeout() time.Duration {
	return ptrValOr(t.Timeout, 30*time.Second)
}

// HookConfig defines a user-configured shell command that fires on a hook
// event (e.g. PreToolUse). This is a pure-data struct: matcher compilation
// is owned by hooks.Runner so a JSON round-trip, merge, or reload can't
// silently drop compiled state.
type HookConfig struct {
	// Friendly display name shown in the TUI. Falls back to Command when empty.
	Name string `json:"name,omitempty" jsonschema:"description=Friendly display name shown in the TUI for this hook"`
	// Regex pattern tested against the tool name. Empty means match all.
	Matcher string `json:"matcher,omitempty" jsonschema:"description=Regex pattern tested against the tool name. Empty means match all tools."`
	// Shell command to execute.
	Command string `json:"command" jsonschema:"required,description=Shell command to execute when the hook fires"`
	// Timeout in seconds. Default 30.
	Timeout int `json:"timeout,omitempty" jsonschema:"description=Timeout in seconds for the hook command,default=30"`
}

// DisplayName returns the hook name for display purposes. It returns Name
// when set, otherwise falls back to Command.
func (h *HookConfig) DisplayName() string {
	if h.Name != "" {
		return h.Name
	}
	return h.Command
}

// TimeoutDuration returns the hook timeout as a time.Duration, defaulting
// to 30s.
func (h *HookConfig) TimeoutDuration() time.Duration {
	if h.Timeout <= 0 {
		return 30 * time.Second
	}
	return time.Duration(h.Timeout) * time.Second
}

// Config holds the configuration for angela.
type Config struct {
	Schema string `json:"$schema,omitempty"`

	// Named model configurations. "main" and "chore" ship as seeds;
	// any other name may be defined and referenced by an agent.
	Models map[ModelConfigName]SelectedModel `json:"models,omitempty" jsonschema:"description=Named model configurations,example={\"main\":{\"model\":\"gpt-4o\",\"provider\":\"openai\"}}"`

	// Recently used models stored in the data directory config.
	RecentModels map[ModelConfigName][]SelectedModel `json:"recent_models,omitempty" jsonschema:"-"`

	// The providers that are configured
	Providers *csync.Map[string, ProviderConfig] `json:"providers,omitempty" jsonschema:"description=AI provider configurations"`

	MCP MCPs `json:"mcp,omitempty" jsonschema:"description=Model Context Protocol server configurations"`

	LSP LSPs `json:"lsp,omitempty" jsonschema:"description=Language Server Protocol configurations"`

	Options *Options `json:"options,omitempty" jsonschema:"description=General application options"`

	Permissions *Permissions `json:"permissions,omitempty" jsonschema:"description=Permission settings for tool usage"`

	Tools Tools `json:"tools,omitzero" jsonschema:"description=Tool configurations"`

	Hooks map[string][]HookConfig `json:"hooks,omitempty" jsonschema:"description=User-defined shell commands that fire on hook events (e.g. PreToolUse)"`

	// Env is a map of environment variables set on startup.
	Env map[string]string `json:"env,omitempty" jsonschema:"description=Environment variables to set on startup"`

	// AgentConfigs holds user-defined agent overrides and custom agents.
	// These are merged over built-in defaults during SetupAgents().
	AgentConfigs map[string]Agent `json:"agents,omitempty" jsonschema:"description=Agent configurations and overrides"`

	// Agents is the resolved agent map (built-in + markdown + config).
	// Not serialized; rebuilt by SetupAgents() on every load.
	Agents map[string]Agent `json:"-"`
}

// cloneForWrite returns a copy of c that the store's typed field mutators
// may modify without racing readers of the currently published Config.
//
// Reads of a published Config take no lock beyond the pointer load, so a
// mutator must never write through the live pointer. Instead it clones,
// mutates the clone, and atomically swaps it in. The clone gives fresh
// copies of every field a typed mutator touches in place — Models,
// RecentModels, MCP, and Options (with its nested TUI pointer). Providers
// is a *csync.Map (internally synchronized) and is shared by reference;
// the remaining fields are immutable after load from the mutators'
// standpoint and are likewise shared.
func (c *Config) cloneForWrite() *Config {
	nc := *c
	// Deep: prepareResolvedConfig edits each model's Variants map in
	// place (dropInvalidVariants deletes from it), so a shallow clone
	// would write straight through into the published snapshot.
	if c.Models != nil {
		models := make(map[ModelConfigName]SelectedModel, len(c.Models))
		for name, model := range c.Models {
			models[name] = model.clone()
		}
		nc.Models = models
	}
	nc.RecentModels = maps.Clone(c.RecentModels)
	nc.MCP = maps.Clone(c.MCP)
	if c.Options != nil {
		opts := *c.Options
		if c.Options.TUI != nil {
			tui := *c.Options.TUI
			opts.TUI = &tui
		}
		nc.Options = &opts
	}
	return &nc
}

// ensureTUI returns c.Options.TUI, allocating Options and TUI as needed so
// callers can assign TUI fields without nil checks.
func (c *Config) ensureTUI() *TUIOptions {
	if c.Options == nil {
		c.Options = &Options{}
	}
	if c.Options.TUI == nil {
		c.Options.TUI = &TUIOptions{}
	}
	return c.Options.TUI
}

func (c *Config) EnabledProviders() []ProviderConfig {
	var enabled []ProviderConfig
	for p := range c.Providers.Seq() {
		if !p.Disable {
			enabled = append(enabled, p)
		}
	}
	return enabled
}

// IsConfigured  return true if at least one provider is configured
func (c *Config) IsConfigured() bool {
	return len(c.EnabledProviders()) > 0
}

func (c *Config) GetModel(provider, model string) *catwalk.Model {
	if providerConfig, ok := c.Providers.Get(provider); ok {
		for _, m := range providerConfig.Models {
			if m.ID == model {
				return &m
			}
		}
	}
	return nil
}

// IsModelAvailable returns true if the provider is enabled and the model
// exists in its catalog. Unlike GetModel, it rejects disabled providers.
func (c *Config) IsModelAvailable(provider, model string) bool {
	providerConfig, ok := c.Providers.Get(provider)
	if !ok || providerConfig.Disable {
		return false
	}
	for _, m := range providerConfig.Models {
		if m.ID == model {
			return true
		}
	}
	return false
}

// ModelForName returns the model configuration registered under name.
func (c *Config) ModelForName(name ModelConfigName) (SelectedModel, bool) {
	model, ok := c.Models[name]
	return model, ok
}

func (c *Config) GetProviderForModelName(name ModelConfigName) *ProviderConfig {
	model, ok := c.Models[name]
	if !ok {
		return nil
	}
	if providerConfig, ok := c.Providers.Get(model.Provider); ok {
		return &providerConfig
	}
	return nil
}

func (c *Config) GetModelByName(name ModelConfigName) *catwalk.Model {
	model, ok := c.Models[name]
	if !ok {
		return nil
	}
	return c.GetModel(model.Provider, model.Model)
}

const maxRecentModelsPerType = 5

func allToolNames() []string {
	return []string{
		"agent",
		"bash",
		"angela_info",
		"angela_logs",
		"job_output",
		"job_kill",
		"download",
		"edit",
		"multiedit",
		"lsp_diagnostics",
		"lsp_references",
		"lsp_restart",
		"lsp_symbols",
		"lsp_definition",
		"lsp_call_hierarchy",
		"lsp_rename",
		"lsp_replace_symbol",
		"fetch",
		"agentic_fetch",
		"glob",
		"grep",
		"ls",
		"question",
		"sourcegraph",
		"todos",
		"view",
		"write",
		"list_mcp_resources",
		"read_mcp_resource",
	}
}

func resolveAllowedTools(allTools []string, disabledTools []string) []string {
	if disabledTools == nil {
		return allTools
	}
	// filter out disabled tools (exclude mode)
	return filterSlice(allTools, disabledTools, false)
}

func filterSlice(data []string, mask []string, include bool) []string {
	var filtered []string
	for _, s := range data {
		// if include is true, we include items that ARE in the mask
		// if include is false, we include items that are NOT in the mask
		if include == slices.Contains(mask, s) {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

func exploreToolNames() []string {
	return []string{
		"fetch", "agentic_fetch", "angela_info",
		"glob", "grep", "ls",
		"lsp_call_hierarchy", "lsp_definition", "lsp_symbols",
		"sourcegraph", "view",
	}
}

// warnUnknownTools logs a warning for any name in names that isn't a
// known built-in tool, catching typos in an agent's allowed_tools or
// disabled_tools without rejecting the config: an unrecognized name
// is inert, it just never matches a real tool in buildTools.
func warnUnknownTools(agentID, field string, names []string) {
	all := allToolNames()
	for _, name := range names {
		if !slices.Contains(all, name) {
			slog.Warn("Agent references an unknown tool name", "agent", agentID, "field", field, "tool", name)
		}
	}
}

// builtinAgents returns the default agent definitions. The base tool set
// has already had the global DisabledTools removed.
func builtinAgents(base []string, contextPaths []string) map[string]Agent {
	return map[string]Agent{
		AgentCoder: {
			ID:   AgentCoder,
			Name: "Coder",
			// Coder is the inheritance root, so both of its sets must
			// be explicit rather than inherited.
			Description:  "An agent that helps with executing coding tasks.",
			Mode:         AgentModePrimary,
			Model:        ModelMain,
			ContextPaths: contextPaths,
			AllowedTools: &AllowedToolSet{Kind: ToolSetAll},
			AllowedMCP:   &AllowedMCPSet{Kind: ToolSetAll},
		},
		AgentExplore: {
			ID:           AgentExplore,
			Name:         "Explore",
			Description:  "Fast agent specialized for exploring codebases. Use for file searches, code keyword searches, or questions about the codebase structure.",
			Mode:         AgentModeSubagent,
			Model:        ModelMain,
			ContextPaths: contextPaths,
			AllowedTools: &AllowedToolSet{Kind: ToolSetScope, Tools: filterSlice(base, exploreToolNames(), true)},
			AllowedMCP:   &AllowedMCPSet{Kind: ToolSetScope},
		},
		AgentGeneral: {
			ID:           AgentGeneral,
			Name:         "General",
			Description:  "General-purpose agent for researching complex questions and executing multi-step tasks in parallel.",
			Mode:         AgentModeSubagent,
			Model:        ModelMain,
			ContextPaths: contextPaths,
			// General mirrors whatever the coder may use, so tightening
			// the coder tightens it too.
			AllowedTools:  &AllowedToolSet{Kind: ToolSetInherited},
			AllowedMCP:    &AllowedMCPSet{Kind: ToolSetInherited},
			DisabledTools: []string{"todos"},
		},

		// Internal agents. Each backs one of Angela's own auxiliary LLM
		// calls; all are hidden and toolless.
		AgentTitle: {
			ID:           AgentTitle,
			Name:         "Title",
			Description:  "Names a session from its first user prompt.",
			Mode:         AgentModeSubagent,
			Hidden:       ptr(true),
			Model:        ModelChore,
			MaxTokens:    ptr(int64(40)),
			ContextPaths: contextPaths,
			AllowedTools: &AllowedToolSet{Kind: ToolSetScope},
			AllowedMCP:   &AllowedMCPSet{Kind: ToolSetScope},
		},
		AgentCompact: {
			ID:          AgentCompact,
			Name:        "Compact",
			Description: "Summarizes a conversation so work can continue in a fresh context.",
			Mode:        AgentModeSubagent,
			Hidden:      ptr(true),
			// Compaction borrows the workhorse model on purpose:
			// summarizing on the cheap model silently degrades the only
			// context a resumed session gets.
			Model:        ModelMain,
			ContextPaths: contextPaths,
			AllowedTools: &AllowedToolSet{Kind: ToolSetScope},
			AllowedMCP:   &AllowedMCPSet{Kind: ToolSetScope},
		},
		AgentAgenticFetch: {
			ID:           AgentAgenticFetch,
			Name:         "Agentic Fetch",
			Description:  "Fetches a URL and answers a question about its content.",
			Mode:         AgentModeSubagent,
			Hidden:       ptr(true),
			Model:        ModelChore,
			ContextPaths: contextPaths,
			AllowedTools: &AllowedToolSet{Kind: ToolSetScope},
			AllowedMCP:   &AllowedMCPSet{Kind: ToolSetScope},
		},
		AgentGenerateAgent: {
			ID:           AgentGenerateAgent,
			Name:         "Generate Agent",
			Description:  "Writes a new agent definition from a description.",
			Mode:         AgentModeSubagent,
			Hidden:       ptr(true),
			Model:        ModelMain,
			ContextPaths: contextPaths,
			AllowedTools: &AllowedToolSet{Kind: ToolSetScope},
			AllowedMCP:   &AllowedMCPSet{Kind: ToolSetScope},
		},
		AgentInitialize: {
			ID:          AgentInitialize,
			Name:        "Initialize",
			Description: "Writes the project's initial context file.",
			Mode:        AgentModeSubagent,
			Hidden:      ptr(true),
			// Model is deliberately unset: initialize never makes an LLM
			// call of its own. Its rendered prompt is injected into an
			// ordinary session and runs on whatever agent is primary.
			ContextPaths: contextPaths,
			AllowedTools: &AllowedToolSet{Kind: ToolSetScope},
			AllowedMCP:   &AllowedMCPSet{Kind: ToolSetScope},
		},
	}
}

// newCustomAgent returns the default Agent used as the merge base for
// an ID with no lower-priority definition yet (a brand-new markdown
// or JSON/angelarc agent). Both permission sets default to
// ToolSetInherited so an agent that never mentions them mirrors the
// coder instead of silently getting a broader grant than the coder
// itself has; ResolveAgents materializes that into a concrete,
// deny-filtered list.
func newCustomAgent(contextPaths []string) Agent {
	return Agent{
		Model:        ModelMain,
		Mode:         AgentModeSubagent,
		ContextPaths: contextPaths,
		AllowedTools: &AllowedToolSet{Kind: ToolSetInherited},
		AllowedMCP:   &AllowedMCPSet{Kind: ToolSetInherited},
	}
}

// mergeAgent overlays non-zero fields from override onto base. Each of
// the three permission fields (AllowedTools, DisabledTools,
// AllowedMCP) replaces the lower layer's value wholesale rather than
// being unioned with it. The ID is always forced to the map key (set
// by the caller after merge).
func mergeAgent(base, override Agent) Agent {
	if override.Name != "" {
		base.Name = override.Name
	}
	if override.Description != "" {
		base.Description = override.Description
	}
	if override.Mode != "" {
		base.Mode = override.Mode
	}
	if override.Model != "" {
		base.Model = override.Model
	}
	if override.Variant != "" {
		base.Variant = override.Variant
	}
	if override.Prompt != "" {
		base.Prompt = override.Prompt
	}
	if override.Temperature != nil {
		base.Temperature = override.Temperature
	}
	if override.AllowedTools != nil {
		base.AllowedTools = override.AllowedTools
	}
	if override.DisabledTools != nil {
		base.DisabledTools = override.DisabledTools
	}
	if override.AllowedMCP != nil {
		base.AllowedMCP = override.AllowedMCP
	}
	if override.ContextPaths != nil {
		base.ContextPaths = override.ContextPaths
	}
	if override.Disabled != nil {
		base.Disabled = override.Disabled
	}
	if override.Hidden != nil {
		base.Hidden = override.Hidden
	}
	if override.MaxTokens != nil {
		base.MaxTokens = override.MaxTokens
	}
	return base
}

// ResolveAgents computes the resolved agent map from built-in
// defaults, markdown agent files, and user AgentConfigs without
// mutating c. The coder agent is resolved first because every other
// agent's ToolSetInherited expands to the coder's final sets.
// Callers that own an unpublished Config (initial load, tests, client
// snapshot refresh) may assign the result via SetupAgents; a running
// ConfigStore must go through ConfigStore.SetupAgents instead, which
// clones before swapping so concurrent readers never observe a
// partially-built map.
func (c *Config) ResolveAgents() map[string]Agent {
	base := resolveAllowedTools(allToolNames(), c.Options.DisabledTools)
	agents := builtinAgents(base, c.Options.ContextPaths)

	// Layer 2: markdown agent files.
	mdAgents := DiscoverAgentFiles(c.Options.AgentPaths)
	for key, override := range mdAgents {
		existing, ok := agents[key]
		if !ok {
			existing = newCustomAgent(c.Options.ContextPaths)
		}
		merged := mergeAgent(existing, override)
		merged.ID = key
		agents[key] = merged
	}

	// Layer 3: user JSON/angelarc overrides. Unlike markdown files,
	// which are validated while being parsed, these arrive straight
	// from JSON decoding, so they are validated here instead of being
	// trusted.
	for key, override := range c.AgentConfigs {
		if err := ValidateAgent(key, override); err != nil {
			slog.Warn("Skipping invalid agent config", "agent", key, "error", err)
			continue
		}
		existing, ok := agents[key]
		if !ok {
			existing = newCustomAgent(c.Options.ContextPaths)
		}
		merged := mergeAgent(existing, override)
		merged.ID = key
		agents[key] = merged
	}

	coderTools, coderMCP := resolveCoderAgent(agents, c.Options.DisabledTools)

	// Materialize every other agent against the coder's resolved sets.
	// Materialize expands ToolSetAll to the full tool list and
	// ToolSetInherited to the coder's list, then applies
	// Options.DisabledTools followed by the agent's own DisabledTools,
	// so no higher-priority layer's allowed_tools can re-enable a
	// globally disabled tool. The result is always non-nil with
	// Kind == ToolSetScope: a self-contained, already-filtered
	// whitelist that buildTools can query with Allows without knowing
	// anything about how it was assembled.
	for key, a := range agents {
		if key == AgentCoder {
			continue
		}
		resolvedTools := a.AllowedTools.Materialize(allToolNames(), coderTools.Tools, c.Options.DisabledTools, a.DisabledTools)
		warnUnknownTools(key, "allowed_tools", resolvedTools.Tools)
		warnUnknownTools(key, "disabled_tools", a.DisabledTools)
		resolvedMCP := a.AllowedMCP.Materialize(&coderMCP)
		a.AllowedTools = &resolvedTools
		a.AllowedMCP = &resolvedMCP
		agents[key] = a
	}

	// Remove disabled agents, but never disable coder.
	for key, a := range agents {
		if a.Disabled != nil && *a.Disabled && key != AgentCoder {
			delete(agents, key)
		}
	}

	return agents
}

// resolveCoderAgent materializes the coder agent in place and returns
// its resolved sets, which every other agent's ToolSetInherited
// expands to. Coder is the inheritance root, so it cannot itself
// inherit: an explicit "inherited" from any layer is downgraded to
// "all" with a warning rather than failing the load.
func resolveCoderAgent(agents map[string]Agent, globalDisabled []string) (AllowedToolSet, AllowedMCPSet) {
	coder := agents[AgentCoder]

	if coder.AllowedTools == nil || coder.AllowedTools.Kind == ToolSetInherited {
		if coder.AllowedTools != nil {
			slog.Warn("The coder agent cannot inherit allowed_tools; granting every tool instead")
		}
		coder.AllowedTools = &AllowedToolSet{Kind: ToolSetAll}
	}
	if coder.AllowedMCP == nil || coder.AllowedMCP.Kind == ToolSetInherited {
		if coder.AllowedMCP != nil {
			slog.Warn("The coder agent cannot inherit allowed_mcp; granting every MCP server instead")
		}
		coder.AllowedMCP = &AllowedMCPSet{Kind: ToolSetAll}
	}

	tools := coder.AllowedTools.Materialize(allToolNames(), nil, globalDisabled, coder.DisabledTools)
	warnUnknownTools(AgentCoder, "allowed_tools", tools.Tools)
	warnUnknownTools(AgentCoder, "disabled_tools", coder.DisabledTools)
	mcp := coder.AllowedMCP.Materialize(nil)

	coder.AllowedTools = &tools
	coder.AllowedMCP = &mcp
	agents[AgentCoder] = coder

	return tools, mcp
}

// prepareResolvedConfig fills in the fields derived from a Config's
// own contents. It must be called on a Config that is not yet visible
// to concurrent readers, and before the single setConfig that
// publishes it: mutating an already-published Config is exactly the
// half-built state the copy-on-write publish exists to prevent.
func prepareResolvedConfig(cfg *Config) {
	cfg.Agents = cfg.ResolveAgents()
	dropInvalidVariants(cfg)
	warnUnreadModelConfigs(cfg)
}

// warnUnreadModelConfigs reports model configs nothing will ever read.
// Only main and chore are resolved implicitly; any other key has to be
// named by an agent's model field. A typo, or a name left behind by a
// rename, otherwise parses cleanly and is silently ignored.
func warnUnreadModelConfigs(cfg *Config) {
	referenced := make(map[ModelConfigName]bool, len(cfg.Agents))
	for _, agent := range cfg.Agents {
		if agent.Model != "" {
			referenced[agent.Model] = true
		}
	}
	for name := range cfg.Models {
		if name == ModelMain || name == ModelChore || referenced[name] {
			continue
		}
		slog.Warn("Model config is never used; no agent names it",
			"model", name, "hint", "set an agent's \"model\" to this name, or remove it")
	}
}

// SetupAgents rebuilds the resolved Agents map in place. Only safe on
// a Config not yet published to concurrent readers (initial load,
// tests, client snapshot refresh); a running ConfigStore must call
// ConfigStore.SetupAgents, which clones before swapping.
func (c *Config) SetupAgents() {
	prepareResolvedConfig(c)
}

func (c *ProviderConfig) TestConnection(resolver VariableResolver) error {
	var (
		providerID = catwalk.InferenceProvider(c.ID)
		testURL    = ""
		headers    = make(map[string]string)
		apiKey, _  = resolver.ResolveValue(c.APIKey)
	)

	switch providerID {
	case catwalk.InferenceProviderMiniMax, catwalk.InferenceProviderMiniMaxChina:
		// NOTE: MiniMax has no good endpoint we can use to validate the API key.
		return nil
	case catwalk.InferenceProviderAlibabaSingapore:
		// NOTE: Alibaba has no good endpoint we can use to validate the API key.
		// Let's at least check the pattern.
		if !strings.HasPrefix(apiKey, "sk-") {
			return fmt.Errorf("invalid API key format for provider %s", c.ID)
		}
		return nil
	}

	switch c.Type {
	case catwalk.TypeOpenAI, catwalk.TypeOpenAICompat, catwalk.TypeOpenRouter:
		baseURL, _ := resolver.ResolveValue(c.BaseURL)
		baseURL = cmp.Or(baseURL, "https://api.openai.com/v1")

		switch providerID {
		case catwalk.InferenceProviderOpenRouter:
			testURL = baseURL + "/credits"
		case catwalk.InferenceProviderOpenCodeGo:
			testURL = strings.Replace(baseURL, "/go", "", 1) + "/models"
		default:
			testURL = baseURL + "/models"
		}

		headers["Authorization"] = "Bearer " + apiKey
	case catwalk.TypeAnthropic:
		baseURL, _ := resolver.ResolveValue(c.BaseURL)
		baseURL = cmp.Or(baseURL, "https://api.anthropic.com/v1")

		switch providerID {
		case catwalk.InferenceKimiCoding:
			testURL = baseURL + "/v1/models"
		default:
			testURL = baseURL + "/models"
		}

		headers["x-api-key"] = apiKey
		headers["anthropic-version"] = "2023-06-01"
	case catwalk.TypeGoogle:
		baseURL, _ := resolver.ResolveValue(c.BaseURL)
		baseURL = cmp.Or(baseURL, "https://generativelanguage.googleapis.com")
		testURL = baseURL + "/v1beta/models?key=" + url.QueryEscape(apiKey)
	case catwalk.TypeBedrock:
		// NOTE: Bedrock has a `/foundation-models` endpoint that we could in
		// theory use, but apparently the authorization is region-specific,
		// so it's not so trivial.
		if strings.HasPrefix(apiKey, "ABSK") { // Bedrock API keys
			return nil
		}
		return errors.New("not a valid bedrock api key")
	case catwalk.TypeVercel:
		// NOTE: Vercel does not validate API keys on the `/models` endpoint.
		if strings.HasPrefix(apiKey, "vck_") { // Vercel API keys
			return nil
		}
		return errors.New("not a valid vercel api key")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := &http.Client{}
	req, err := http.NewRequestWithContext(ctx, "GET", testURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request for provider %s: %w", c.ID, err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for k, v := range c.ExtraHeaders {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to create request for provider %s: %w", c.ID, err)
	}
	defer resp.Body.Close()

	switch providerID {
	case catwalk.InferenceProviderZAI:
		if resp.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("failed to connect to provider %s: %s", c.ID, resp.Status)
		}
	default:
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("failed to connect to provider %s: %s", c.ID, resp.Status)
		}
	}
	return nil
}

// resolveEnvs expands every value in envs through the given resolver
// and returns a fresh "KEY=value" slice sorted by key. The input map is
// not mutated. On the first resolution failure it returns nil and an
// error identifying the offending variable; the inner resolver error is
// already sanitized by ResolveValue and is wrapped with %w.
func resolveEnvs(envs map[string]string, r VariableResolver) ([]string, error) {
	if len(envs) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(envs))
	for k := range envs {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	res := make([]string, 0, len(envs))
	for _, k := range keys {
		v, err := r.ResolveValue(envs[k])
		if err != nil {
			return nil, fmt.Errorf("env %s: %w", k, err)
		}
		res = append(res, fmt.Sprintf("%s=%s", k, v))
	}
	return res, nil
}

func ptrValOr[T any](t *T, el T) T {
	if t == nil {
		return el
	}
	return *t
}
