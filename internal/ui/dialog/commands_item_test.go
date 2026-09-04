package dialog

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// TestCommandItem_FilterIncludesAliasesAndDescription pins that fuzzy
// filtering can match on any of the title, its aliases, or its
// description, not just the visible title text.
func TestCommandItem_FilterIncludesAliasesAndDescription(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()

	t.Run("title only", func(t *testing.T) {
		t.Parallel()
		c := NewCommandItem(&s, "id", "New Session", "ctrl+n", ActionNewSession{})
		require.Equal(t, "New Session", c.Filter())
	})

	t.Run("title with aliases", func(t *testing.T) {
		t.Parallel()
		c := NewCommandItem(&s, "id", "New Session", "ctrl+n", ActionNewSession{}).WithAliases("clear", "reset")
		require.Equal(t, "New Session clear reset", c.Filter())
	})

	t.Run("title with description", func(t *testing.T) {
		t.Parallel()
		c := NewCommandItem(&s, "id", "New Session", "ctrl+n", ActionNewSession{}).WithDescription("Starts fresh")
		require.Equal(t, "New Session Starts fresh", c.Filter())
	})

	t.Run("title, aliases, and description together", func(t *testing.T) {
		t.Parallel()
		c := NewCommandItem(&s, "id", "New Session", "ctrl+n", ActionNewSession{}).
			WithAliases("clear").
			WithDescription("Starts fresh")
		require.Equal(t, "New Session clear Starts fresh", c.Filter())
	})
}

// TestCommandItem_Accessors covers the plain getters: identity, the
// action a selection dispatches, and the shortcut/info-text surface the
// list renders.
func TestCommandItem_Accessors(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	action := ActionNewSession{}
	c := NewCommandItem(&s, "new_session", "New Session", "ctrl+n", action)

	require.True(t, c.Finished())
	require.Equal(t, "new_session", c.ID())
	require.Equal(t, action, c.Action())
	require.Equal(t, "ctrl+n", c.Shortcut())
	require.Equal(t, "ctrl+n", c.InfoText())
}

// TestCommandItem_SetHideInfo verifies the shortcut hint column can be
// toggled off (used when it would crowd the command names) and that the
// change bumps the render cache only when the value actually flips.
func TestCommandItem_SetHideInfo(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	c := NewCommandItem(&s, "new_session", "New Session", "ctrl+n", ActionNewSession{})

	shown := ansi.Strip(c.Render(40))
	require.Contains(t, shown, "ctrl+n")

	before := c.Version()
	c.SetHideInfo(false) // already false: no-op
	require.Equal(t, before, c.Version())

	c.SetHideInfo(true)
	require.Greater(t, c.Version(), before)
	hidden := ansi.Strip(c.Render(40))
	require.NotContains(t, hidden, "ctrl+n")

	afterHide := c.Version()
	c.SetHideInfo(true) // already true: no-op
	require.Equal(t, afterHide, c.Version())
}

// TestCommandItem_RenderIncludesDescription verifies a description, when
// present, is rendered as a second line beneath the title.
func TestCommandItem_RenderIncludesDescription(t *testing.T) {
	t.Parallel()

	s := styles.CharmtonePantera()
	c := NewCommandItem(&s, "id", "Attach Skill", "", ActionNewSession{}).WithDescription("Runs the plan skill")

	blurred := ansi.Strip(c.Render(40))
	require.Contains(t, blurred, "Attach Skill")
	require.Contains(t, blurred, "Runs the plan skill")

	c.SetFocused(true)
	focused := ansi.Strip(c.Render(40))
	require.Contains(t, focused, "Attach Skill")
	require.Contains(t, focused, "Runs the plan skill")
}
