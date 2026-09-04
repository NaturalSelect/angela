package agent

import (
	"testing"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/openrouter"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/stretchr/testify/require"
)

// TestOpenrouterCost pins the three ways provider metadata can fail to
// yield a cost (no entry, wrong provider, wrong type) alongside the
// success path that reads OpenRouter's own accounted cost.
func TestOpenrouterCost(t *testing.T) {
	t.Parallel()

	t.Run("no metadata at all", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, openrouterCost(nil))
	})

	t.Run("no openrouter entry", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, openrouterCost(fantasy.ProviderMetadata{}))
	})

	t.Run("entry present under the wrong type", func(t *testing.T) {
		t.Parallel()
		metadata := fantasy.ProviderMetadata{openrouter.Name: &anthropic.ProviderOptions{}}
		require.Nil(t, openrouterCost(metadata))
	})

	t.Run("reads the accounted cost", func(t *testing.T) {
		t.Parallel()
		metadata := fantasy.ProviderMetadata{
			openrouter.Name: &openrouter.ProviderMetadata{Usage: openrouter.UsageAccounting{Cost: 0.0042}},
		}
		cost := openrouterCost(metadata)
		require.NotNil(t, cost)
		require.InDelta(t, 0.0042, *cost, 1e-9)
	})
}

// TestSanitizeToolInput pins that only genuinely malformed JSON gets
// replaced, and that valid input (including a bare empty object) is
// passed through untouched.
func TestSanitizeToolInput(t *testing.T) {
	t.Parallel()

	t.Run("valid JSON passes through unchanged", func(t *testing.T) {
		t.Parallel()
		out, sanitized := sanitizeToolInput("bash", "call-1", `{"command":"ls"}`)
		require.Equal(t, `{"command":"ls"}`, out)
		require.False(t, sanitized)
	})

	t.Run("malformed JSON is replaced with an empty object", func(t *testing.T) {
		t.Parallel()
		out, sanitized := sanitizeToolInput("bash", "call-1", `{"command":`)
		require.Equal(t, "{}", out)
		require.True(t, sanitized)
	})

	t.Run("empty string is malformed", func(t *testing.T) {
		t.Parallel()
		out, sanitized := sanitizeToolInput("bash", "call-1", "")
		require.Equal(t, "{}", out)
		require.True(t, sanitized)
	})
}

// TestBuildSummaryPrompt pins that the base summary instruction is
// always present, and that a todo list is only appended (with each
// entry's status and content) when there is one to report.
func TestBuildSummaryPrompt(t *testing.T) {
	t.Parallel()

	t.Run("no todos", func(t *testing.T) {
		t.Parallel()
		prompt := buildSummaryPrompt(nil)
		require.Equal(t, "Provide a detailed summary of our conversation above.", prompt)
	})

	t.Run("includes each todo's status and content", func(t *testing.T) {
		t.Parallel()
		todos := []session.Todo{
			{Content: "Fix the bug", Status: session.TodoStatusCompleted},
			{Content: "Write tests", Status: session.TodoStatusInProgress},
		}
		prompt := buildSummaryPrompt(todos)
		require.Contains(t, prompt, "Provide a detailed summary of our conversation above.")
		require.Contains(t, prompt, "## Current Todo List")
		require.Contains(t, prompt, "- [completed] Fix the bug")
		require.Contains(t, prompt, "- [in_progress] Write tests")
		require.Contains(t, prompt, "`todos` tool")
	})
}
