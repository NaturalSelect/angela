package completions

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/ui/list"
	"github.com/stretchr/testify/require"
)

func threeFileCompletions(t *testing.T) *Completions {
	t.Helper()

	c := New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
	c.SetItems([]FileCompletionValue{{Path: "a.go"}, {Path: "b.go"}, {Path: "c.go"}}, nil)
	return c
}

func TestUpdate_NotOpenReturnsFalse(t *testing.T) {
	t.Parallel()

	c := New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
	msg, handled := c.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	require.False(t, handled)
	require.Nil(t, msg)
}

// The completions list renders in reverse (SetReverse(true)), which
// inverts SelectNext/SelectPrev: "up" moves to a higher index and
// "down" moves to a lower one, wrapping at the index-0/highest-index
// boundary instead of the usual first/last.

func TestUpdate_DownWrapsToHighestIndex(t *testing.T) {
	t.Parallel()

	c := threeFileCompletions(t)
	require.Equal(t, 0, c.list.Selected())

	msg, handled := c.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	require.True(t, handled)
	require.Nil(t, msg)
	require.Equal(t, 2, c.list.Selected(), "index 0 has no lower index to move down to, so it wraps to the highest")
}

func TestUpdate_UpMovesToHigherIndex(t *testing.T) {
	t.Parallel()

	c := threeFileCompletions(t)
	_, handled := c.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	require.True(t, handled)
	require.Equal(t, 1, c.list.Selected())
}

func TestUpdate_UpWrapsFromHighestIndex(t *testing.T) {
	t.Parallel()

	c := threeFileCompletions(t)
	_, handled := c.Update(tea.KeyPressMsg{Code: tea.KeyDown}) // wraps 0 -> 2
	require.True(t, handled)
	require.Equal(t, 2, c.list.Selected())

	_, handled = c.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	require.True(t, handled)
	require.Equal(t, 0, c.list.Selected(), "already at the highest index, up wraps back to 0")
}

func TestUpdate_SelectCommitsAndCloses(t *testing.T) {
	t.Parallel()

	c := threeFileCompletions(t)
	msg, handled := c.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, handled)
	sel, ok := msg.(SelectionMsg[FileCompletionValue])
	require.True(t, ok, "expected a file selection, got %T", msg)
	require.Equal(t, "a.go", sel.Value.Path)
	require.False(t, sel.KeepOpen)
	require.False(t, c.IsOpen(), "a committed selection closes the popup")
}

func TestUpdate_UpInsertKeepsOpen(t *testing.T) {
	t.Parallel()

	c := threeFileCompletions(t)
	msg, handled := c.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	require.True(t, handled)
	sel, ok := msg.(SelectionMsg[FileCompletionValue])
	require.True(t, ok)
	require.True(t, sel.KeepOpen)
	require.True(t, c.IsOpen(), "insert selections keep the popup open")
	require.Equal(t, "b.go", sel.Value.Path, "ctrl+p moves to the next higher index before inserting")
}

func TestUpdate_DownInsertKeepsOpen(t *testing.T) {
	t.Parallel()

	c := threeFileCompletions(t)
	msg, handled := c.Update(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	require.True(t, handled)
	sel, ok := msg.(SelectionMsg[FileCompletionValue])
	require.True(t, ok)
	require.True(t, sel.KeepOpen)
	require.Equal(t, "c.go", sel.Value.Path, "ctrl+n wraps from index 0 to the highest index before inserting")
}

func TestUpdate_CancelClosesAndEmitsClosedMsg(t *testing.T) {
	t.Parallel()

	c := threeFileCompletions(t)
	msg, handled := c.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.True(t, handled)
	require.Equal(t, ClosedMsg{}, msg)
	require.False(t, c.IsOpen())
}

func TestUpdate_UnmatchedKeyReturnsFalse(t *testing.T) {
	t.Parallel()

	c := threeFileCompletions(t)
	msg, handled := c.Update(tea.KeyPressMsg{Code: 'z', Text: "z"})
	require.False(t, handled)
	require.Nil(t, msg)
}

func TestUpdate_SelectWithNoItemsReturnsNilMsg(t *testing.T) {
	t.Parallel()

	c := New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
	c.SetItems(nil, nil)
	msg, handled := c.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, handled)
	require.Nil(t, msg)
}

func TestUpdate_NavigationWithNoItemsIsNoop(t *testing.T) {
	t.Parallel()

	c := New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
	c.SetItems(nil, nil)

	_, handled := c.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	require.True(t, handled)
	_, handled = c.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	require.True(t, handled)
}

func TestUpdate_SelectResourceEmitsResourceSelection(t *testing.T) {
	t.Parallel()

	c := New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
	c.SetItems(nil, []ResourceCompletionValue{{MCPName: "srv", Title: "doc", URI: "file:///x"}})

	msg, handled := c.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, handled)
	sel, ok := msg.(SelectionMsg[ResourceCompletionValue])
	require.True(t, ok, "expected a resource selection, got %T", msg)
	require.Equal(t, "srv", sel.Value.MCPName)
}

// TestUpdate_SelectUnknownValueTypeReturnsNil exercises selectCurrent's
// default case directly: SetItems/SetAgents only ever produce the three
// known value types, so an unrecognized value has to be injected by hand.
func TestUpdate_SelectUnknownValueTypeReturnsNil(t *testing.T) {
	t.Parallel()

	c := New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
	item := NewCompletionItem("weird", 42, lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
	c.open = true
	c.allItems = []list.FilterableItem{item}
	c.filtered = []list.FilterableItem{item}
	c.list.SetItems(c.filtered...)
	c.list.SelectFirst()

	msg, handled := c.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, handled)
	require.Nil(t, msg)
}

func TestClose(t *testing.T) {
	t.Parallel()

	c := threeFileCompletions(t)
	require.True(t, c.IsOpen())
	c.Close()
	require.False(t, c.IsOpen())
}

func TestHasItems(t *testing.T) {
	t.Parallel()

	c := New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
	require.False(t, c.HasItems())

	c.SetItems([]FileCompletionValue{{Path: "a.go"}}, nil)
	require.True(t, c.HasItems())
}

func TestSize(t *testing.T) {
	t.Parallel()

	c := threeFileCompletions(t)
	w, h := c.Size()
	require.Equal(t, c.width, w)
	require.Equal(t, 3, h, "3 visible items, well under maxHeight")
}

func TestKeyMapAccessor(t *testing.T) {
	t.Parallel()

	c := New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
	require.Equal(t, DefaultKeyMap(), c.KeyMap())
}

func TestSetStyles(t *testing.T) {
	t.Parallel()

	c := New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
	normal := lipgloss.NewStyle().Bold(true)
	focused := lipgloss.NewStyle().Italic(true)
	match := lipgloss.NewStyle().Underline(true)

	c.SetStyles(normal, focused, match)
	require.Equal(t, normal, c.normalStyle)
	require.Equal(t, focused, c.focusedStyle)
	require.Equal(t, match, c.matchStyle)
}

func TestRender_ClosedReturnsEmpty(t *testing.T) {
	t.Parallel()

	c := threeFileCompletions(t)
	c.Close()
	require.Empty(t, c.Render())
}

func TestRender_NoItemsReturnsEmpty(t *testing.T) {
	t.Parallel()

	c := New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
	c.SetItems(nil, nil)
	require.Empty(t, c.Render())
}

func TestRender_WithItemsProducesOutput(t *testing.T) {
	t.Parallel()

	c := threeFileCompletions(t)
	require.NotEmpty(t, c.Render())
}

func TestSetItems_IncludesResources(t *testing.T) {
	t.Parallel()

	c := New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
	c.SetItems(
		[]FileCompletionValue{{Path: "a.go"}},
		[]ResourceCompletionValue{{MCPName: "srv", Title: "doc", URI: "file:///x"}},
	)
	require.Len(t, c.filtered, 2)

	var texts []string
	for _, it := range c.filtered {
		texts = append(texts, it.(*CompletionItem).Text())
	}
	require.Contains(t, texts, "a.go")
	require.Contains(t, texts, "srv/doc")
}

func TestSetItems_ResourceFallsBackToURIWhenTitleEmpty(t *testing.T) {
	t.Parallel()

	c := New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
	c.SetItems(nil, []ResourceCompletionValue{{MCPName: "srv", URI: "file:///x"}})
	require.Equal(t, "srv/file:///x", c.filtered[0].(*CompletionItem).Text())
}

func TestFilter_NoOpWhenClosed(t *testing.T) {
	t.Parallel()

	c := New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
	c.Filter("anything")
	require.Empty(t, c.Query())
}

func TestFilter_NoOpWhenQueryUnchanged(t *testing.T) {
	t.Parallel()

	c := threeFileCompletions(t)
	c.Filter("a")
	first := c.filtered
	c.Filter("a")
	require.Equal(t, first, c.filtered)
}

func TestFilter_EmptyQueryResetsToAllItems(t *testing.T) {
	t.Parallel()

	c := threeFileCompletions(t)
	c.Filter("a")
	require.Less(t, len(c.filtered), 3)

	c.Filter("")
	require.Len(t, c.filtered, 3)
}

func TestNamePriorityTier_EmptyQueryIsFallback(t *testing.T) {
	t.Parallel()

	require.Equal(t, tierFallback, namePriorityTier("internal/ui/chat/user.go", ""))
}

// TestLoadFiles is not parallel: it calls t.Chdir, which Go forbids in a
// parallel test.
func TestLoadFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644))
	t.Chdir(dir)

	files := loadFiles(1, 10)
	require.Len(t, files, 2)
	require.Equal(t, "a.txt", files[0].Path)
	require.Equal(t, "b.txt", files[1].Path)
}

func TestLoadMCPResourcesEmptyByDefault(t *testing.T) {
	t.Parallel()

	require.Empty(t, loadMCPResources())
}

// TestOpen is not parallel: it calls t.Chdir, which Go forbids in a
// parallel test.
func TestOpen(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644))
	t.Chdir(dir)

	c := New(lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
	cmd := c.Open(1, 10)
	require.NotNil(t, cmd)

	msg := cmd()
	loaded, ok := msg.(CompletionItemsLoadedMsg)
	require.True(t, ok, "expected CompletionItemsLoadedMsg, got %T", msg)
	require.Len(t, loaded.Files, 1)
	require.Equal(t, "f.txt", loaded.Files[0].Path)
	require.Empty(t, loaded.Resources)
}
