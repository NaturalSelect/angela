package completions

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

func agentCompletions(t *testing.T, ids ...string) *Completions {
	t.Helper()

	c := New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
	agents := make([]AgentCompletionValue, len(ids))
	for i, id := range ids {
		agents[i] = AgentCompletionValue{ID: id}
	}
	c.SetAgents(agents)
	return c
}

// TestSetAgentsFiltersByRelevance pins the contract agent mention relies on:
// typing part of an id narrows to it, and the match is recorded so the popup
// can highlight the typed run.
func TestSetAgentsFiltersByRelevance(t *testing.T) {
	t.Parallel()

	c := agentCompletions(t, "explore", "general", "deep-research")
	c.Filter("gen")

	require.NotEmpty(t, c.filtered)
	first, ok := c.filtered[0].(*CompletionItem)
	require.True(t, ok)
	require.Equal(t, "general", first.Text())
	require.Equal(t, AgentCompletionValue{ID: "general"}, first.Value())
	require.NotEmpty(t, first.match.MatchedIndexes)
}

// TestSelectAgentEmitsAgentSelection covers the type dispatch in
// selectCurrent. The popup carries an untyped value, so an unrecognized one
// silently yields no message at all — the agent case has to be wired for
// Enter to do anything.
func TestSelectAgentEmitsAgentSelection(t *testing.T) {
	t.Parallel()

	c := agentCompletions(t, "plan", "deep-research")
	c.Filter("deep")

	msg := c.selectCurrent(false)
	selection, ok := msg.(SelectionMsg[AgentCompletionValue])
	require.True(t, ok, "selecting an agent must emit an agent selection, got %T", msg)
	require.Equal(t, "deep-research", selection.Value.ID)
	require.False(t, selection.KeepOpen)
	require.False(t, c.IsOpen(), "a committed selection closes the popup")
}

// TestAgentsSkipPathPriorityTiers guards the sort split. The file tiers rank
// on basename and extension, so an id containing a dot would be read as one:
// "deep.research" has stem "deep", which the exact-stem tier would hoist
// above a better fuzzy match. Agents must not go through that.
func TestAgentsSkipPathPriorityTiers(t *testing.T) {
	t.Parallel()

	c := agentCompletions(t, "deep.research", "deep")
	c.Filter("deep")

	require.NotEmpty(t, c.filtered)
	require.Equal(t, kindAgent, c.kind)

	// Same ids as file paths do get reordered by the stem tier, which is
	// what the agent list is being kept away from.
	f := New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
	f.SetItems([]FileCompletionValue{{Path: "deep.research"}, {Path: "deep"}}, nil)
	f.Filter("deep")
	require.Equal(t, kindFile, f.kind)
}

// TestSetAgentsReplacesFileItems pins that the two sources do not bleed into
// each other: reopening on a different trigger rebuilds the list rather than
// appending to whatever was there.
func TestSetAgentsReplacesFileItems(t *testing.T) {
	t.Parallel()

	c := New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
	c.SetItems([]FileCompletionValue{{Path: "main.go"}}, nil)
	require.Len(t, c.filtered, 1)

	c.SetAgents([]AgentCompletionValue{{ID: "explore"}, {ID: "plan"}})
	require.Len(t, c.filtered, 2)
	require.Equal(t, kindAgent, c.kind)
	require.Empty(t, c.Query(), "rebuilding clears the previous query")
}
