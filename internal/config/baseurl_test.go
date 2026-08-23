package config

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/stretchr/testify/require"
)

// TestNormalizeBaseURLAnthropic pins that an Anthropic-type base URL
// always ends up as the bare host the SDK expects, regardless of
// whether the user copied a "/v1" or "/v1/messages" suffix from the
// vendor's REST docs. The SDK hardcodes "v1/messages" as a relative
// path, so a surviving "/v1" would be duplicated into "/v1/v1/messages".
func TestNormalizeBaseURLAnthropic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare host unchanged", "https://api.anthropic.com", "https://api.anthropic.com"},
		{"strips trailing v1", "https://api.anthropic.com/v1", "https://api.anthropic.com"},
		{"strips trailing v1 with slash", "https://api.anthropic.com/v1/", "https://api.anthropic.com"},
		{"strips v1/messages", "https://api.anthropic.com/v1/messages", "https://api.anthropic.com"},
		{"strips v1/messages with slash", "https://api.anthropic.com/v1/messages/", "https://api.anthropic.com"},
		{"kimi coding path has no v1 to strip", "https://api.kimi.com/coding", "https://api.kimi.com/coding"},
		{"minimax path has no v1 to strip", "https://api.minimax.io/anthropic", "https://api.minimax.io/anthropic"},
		{"empty stays empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, NormalizeBaseURL(tt.in, catwalk.TypeAnthropic))
		})
	}
}

// TestNormalizeBaseURLOpenAIStyle pins that OpenAI-style base URLs only
// have an accidentally copied "/chat/completions" or "/responses" tail
// stripped — never a missing version segment appended. Auto-appending
// would corrupt real endpoints that intentionally sit at the domain root
// (Copilot) or under a non-"/v1" path (Zhipu, Z.ai).
func TestNormalizeBaseURLOpenAIStyle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		providerType catwalk.Type
		in           string
		want         string
	}{
		{"openai v1 unchanged", catwalk.TypeOpenAI, "https://api.openai.com/v1", "https://api.openai.com/v1"},
		{"openai strips chat/completions", catwalk.TypeOpenAI, "https://api.openai.com/v1/chat/completions", "https://api.openai.com/v1"},
		{"openai strips responses", catwalk.TypeOpenAI, "https://api.openai.com/v1/responses", "https://api.openai.com/v1"},
		{"openai-compat strips chat/completions with slash", catwalk.TypeOpenAICompat, "https://api.deepseek.com/v1/chat/completions/", "https://api.deepseek.com/v1"},
		{"openrouter unchanged", catwalk.TypeOpenRouter, "https://openrouter.ai/api/v1", "https://openrouter.ai/api/v1"},
		{"copilot bare domain unchanged", catwalk.TypeOpenAICompat, "https://api.githubcopilot.com", "https://api.githubcopilot.com"},
		{"zai custom path unchanged", catwalk.TypeOpenAICompat, "https://api.z.ai/api/coding/paas/v4", "https://api.z.ai/api/coding/paas/v4"},
		{"zhipu custom path unchanged", catwalk.TypeOpenAICompat, "https://open.bigmodel.cn/api/paas/v4", "https://open.bigmodel.cn/api/paas/v4"},
		{"custom discover provider strips chat/completions", catwalk.Type("ollama"), "http://localhost:11434/v1/chat/completions", "http://localhost:11434/v1"},
		{"unrelated provider type left untouched", catwalk.TypeGoogle, "https://example.com/v1/chat/completions", "https://example.com/v1/chat/completions"},
		{"empty stays empty", catwalk.TypeOpenAI, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, NormalizeBaseURL(tt.in, tt.providerType))
		})
	}
}

func TestLooksVersioned(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"v1 suffix", "https://api.openai.com/v1", true},
		{"v1 suffix with trailing slash", "https://api.openai.com/v1/", true},
		{"v10 suffix", "https://example.com/v10", true},
		{"bare domain", "https://api.githubcopilot.com", false},
		{"non-version last segment", "https://api.z.ai/api/coding/paas/v4x", false},
		{"vendor path without version", "https://open.bigmodel.cn/api/paas", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, looksVersioned(tt.in))
		})
	}
}
