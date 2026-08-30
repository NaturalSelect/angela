package config

import (
	"strings"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/discover"
)

// NormalizeBaseURL rewrites a user-supplied provider base URL to the form
// the underlying SDK actually expects, regardless of whether the user
// included a trailing version segment.
//
// Anthropic's SDK hardcodes "v1/messages" as a relative request path, so
// its base URL must be the bare host; a user-supplied "/v1" suffix would
// otherwise be duplicated. OpenAI-style SDKs never add a version segment
// themselves, so their base URL must already contain whatever path the
// vendor's API requires; NormalizeBaseURL only strips an accidentally
// copied "/chat/completions" or "/responses" suffix there — it never
// appends a missing version segment, since doing so would corrupt real
// endpoints that intentionally sit at the domain root (e.g. Copilot) or
// under a non-"/v1" path (e.g. Zhipu).
func NormalizeBaseURL(baseURL string, providerType catwalk.Type) string {
	if baseURL == "" {
		return baseURL
	}
	switch {
	case providerType == catwalk.TypeAnthropic:
		return stripAnthropicSuffixes(baseURL)
	case isOpenAIStyle(providerType):
		return stripOpenAISuffixes(baseURL)
	default:
		return baseURL
	}
}

// isOpenAIStyle reports whether providerType routes through an
// OpenAI-compatible SDK, whose relative request paths never carry a
// version segment.
func isOpenAIStyle(providerType catwalk.Type) bool {
	switch providerType {
	case catwalk.TypeOpenAI, catwalk.TypeOpenAICompat, catwalk.TypeOpenRouter:
		return true
	default:
		return discover.IsKnownCustomProvider(string(providerType))
	}
}

// stripAnthropicSuffixes removes a trailing "/v1" or "/v1/messages" (with
// or without a trailing slash) that the Anthropic SDK would otherwise
// duplicate itself.
func stripAnthropicSuffixes(baseURL string) string {
	for {
		trimmed := strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1/messages")
		trimmed = strings.TrimSuffix(strings.TrimRight(trimmed, "/"), "/v1")
		if trimmed == baseURL {
			return baseURL
		}
		baseURL = trimmed
	}
}

// stripOpenAISuffixes removes a trailing "/chat/completions" or
// "/responses" segment that the OpenAI SDK would otherwise duplicate
// itself. It never adds a version segment: unlike Anthropic, OpenAI-style
// APIs have no fixed convention for where "/v1" sits, or whether it
// exists at all, so guessing would corrupt real endpoints.
func stripOpenAISuffixes(baseURL string) string {
	for {
		trimmed := strings.TrimRight(baseURL, "/")
		trimmed = strings.TrimSuffix(trimmed, "/chat/completions")
		trimmed = strings.TrimSuffix(trimmed, "/responses")
		if trimmed == baseURL {
			return baseURL
		}
		baseURL = trimmed
	}
}

// looksVersioned reports whether baseURL's last path segment looks like
// an API version marker (e.g. "v1", "v2", "v10"). It is used to decide
// whether a connection failure is worth hinting at a missing version
// segment; it is deliberately not used to append one automatically.
func looksVersioned(baseURL string) bool {
	trimmed := strings.TrimRight(baseURL, "/")
	segment := trimmed[strings.LastIndex(trimmed, "/")+1:]
	if len(segment) < 2 || segment[0] != 'v' {
		return false
	}
	for _, r := range segment[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
