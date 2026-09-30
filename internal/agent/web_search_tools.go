package agent

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/env"
	"github.com/NaturalSelect/angela/internal/websearch"
	"github.com/NaturalSelect/angela/internal/websearch/engines"
)

// webSearchEnvKeyFallback maps an engine id to the environment
// variable it falls back to when Tools.WebSearch.APIKeys has no entry
// for that engine, matching the lookup order dsh-free-search itself
// uses for the same engines.
var webSearchEnvKeyFallback = map[string]string{
	"exa":       "EXA_API_KEY",
	"tavily":    "TAVILY_API_KEY",
	"firecrawl": "FIRECRAWL_API_KEY",
	"anysearch": "ANYSEARCH_API_KEY",
}

// newWebSearchRouter builds the websearch.Router shared by the
// WebSearch and MultiSearch tools. It performs no network requests
// itself, so buildTools can call it on every invocation and pick up a
// reloaded config for free.
func (c *coordinator) newWebSearchRouter() (*websearch.Router, error) {
	cfg := c.cfg.Config().Tools.WebSearch
	e := env.New()
	resolver := config.NewEnvOnlyVariableResolver(e)

	apiKeys := make(map[string]string, len(cfg.APIKeys))
	for id, raw := range cfg.APIKeys {
		resolved, err := resolver.ResolveValue(raw)
		if err != nil {
			return nil, fmt.Errorf("resolve web search api key for engine %q: %w", id, err)
		}
		if strings.Contains(resolved, "$(") {
			// NOTE: Only $VAR and ${VAR} are expanded here; the value is never logged as it is a credential.
			slog.Warn("Web search api key still contains \"$(\" after expansion; command substitution is not supported",
				"engine", id)
		}
		if resolved != "" {
			apiKeys[id] = resolved
		}
	}
	for id, envVar := range webSearchEnvKeyFallback {
		if apiKeys[id] != "" {
			continue
		}
		if v := e.Get(envVar); v != "" {
			apiKeys[id] = v
		}
	}

	all := engines.Default(engines.Options{
		APIKeys:          apiKeys,
		BingMarket:       cfg.BingMarket,
		SearxngInstances: cfg.SearxngInstances,
	})

	preferred := cfg.GetEngine()
	if !webSearchHasEngine(all, preferred) {
		slog.Warn("Configured web search engine not found; falling back to bing", "engine", preferred)
		preferred = "bing"
	}

	return websearch.NewRouter(all, preferred, cfg.GetTimeout())
}

// webSearchHasEngine reports whether id names one of the engines in all.
func webSearchHasEngine(all []websearch.Engine, id string) bool {
	for _, e := range all {
		if e.ID() == id {
			return true
		}
	}
	return false
}
