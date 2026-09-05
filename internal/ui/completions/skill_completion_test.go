package completions

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

func skillCompletions(t *testing.T, names ...string) *Completions {
	t.Helper()

	c := New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
	values := make([]SkillCompletionValue, len(names))
	for i, name := range names {
		values[i] = SkillCompletionValue{Name: name, Description: "desc " + name}
	}
	c.SetSkills(values)
	return c
}

// TestSetSkillsFiltersByRelevance pins the contract skill mention relies
// on: typing part of a name narrows to it, matched on the name alone.
func TestSetSkillsFiltersByRelevance(t *testing.T) {
	t.Parallel()

	c := skillCompletions(t, "jq", "angela-config", "angela-setup")
	c.Filter("jq")

	require.NotEmpty(t, c.filtered)
	first, ok := c.filtered[0].(*CompletionItem)
	require.True(t, ok)
	require.Equal(t, "jq", first.Text())
	require.Equal(t, SkillCompletionValue{Name: "jq", Description: "desc jq"}, first.Value())
	require.NotEmpty(t, first.match.MatchedIndexes)
}

// TestSelectSkillEmitsSkillSelection covers the type dispatch in
// selectCurrent: an unrecognized value yields no message at all, so the
// skill case has to be wired for Enter to do anything.
func TestSelectSkillEmitsSkillSelection(t *testing.T) {
	t.Parallel()

	c := skillCompletions(t, "jq", "angela-hooks")
	c.Filter("hooks")

	msg := c.selectCurrent(false)
	selection, ok := msg.(SelectionMsg[SkillCompletionValue])
	require.True(t, ok, "selecting a skill must emit a skill selection, got %T", msg)
	require.Equal(t, "angela-hooks", selection.Value.Name)
	require.False(t, selection.KeepOpen)
	require.False(t, c.IsOpen(), "a committed selection closes the popup")
}

// TestSkillsSkipPathPriorityTiers guards the sort split the same way
// TestAgentsSkipPathPriorityTiers does for agents: a skill name is not a
// path, so it must not go through the file basename/extension tiers.
func TestSkillsSkipPathPriorityTiers(t *testing.T) {
	t.Parallel()

	c := skillCompletions(t, "deep.research", "deep")
	c.Filter("deep")

	require.NotEmpty(t, c.filtered)
	require.Equal(t, kindSkill, c.kind)
}

// TestSelectAtMatchesKeyboardSelection pins that a mouse click (SelectAt)
// and a keyboard Enter (selectCurrent) resolve the same item to the same
// message, since the mouse path exists precisely to share that outcome.
func TestSelectAtMatchesKeyboardSelection(t *testing.T) {
	t.Parallel()

	c := skillCompletions(t, "first", "second", "third")

	// Row 1 in insertion (un-reversed) order is "second", matching what
	// list.ItemIndexAtPosition would resolve regardless of how Render
	// displays the popup.
	msg := c.SelectAt(1)
	selection, ok := msg.(SelectionMsg[SkillCompletionValue])
	require.True(t, ok, "expected a skill selection, got %T", msg)
	require.Equal(t, "second", selection.Value.Name)

	c2 := skillCompletions(t, "first", "second", "third")
	c2.list.SetSelected(1)
	keyboardMsg := c2.selectCurrent(false)
	require.Equal(t, keyboardMsg, msg)
}

// TestSelectAtOutOfRangeReturnsNil guards the click-outside-any-row case:
// a stale or mis-flipped coordinate must not panic or select something
// arbitrary.
func TestSelectAtOutOfRangeReturnsNil(t *testing.T) {
	t.Parallel()

	c := skillCompletions(t, "only")
	require.Nil(t, c.SelectAt(-1))
	require.Nil(t, c.SelectAt(99))
}
