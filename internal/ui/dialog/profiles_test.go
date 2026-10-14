package dialog

import (
	"image"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/profiling"
	"github.com/NaturalSelect/angela/internal/ui/common"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func newTestProfiles(t *testing.T, workingDir string) *Profiles {
	t.Helper()
	com := &common.Common{Styles: testStyles()}
	return NewProfiles(com, profiling.Profiles, workingDir)
}

func TestProfiles_ID(t *testing.T) {
	t.Parallel()

	require.Equal(t, ProfilesID, newTestProfiles(t, "/work").ID())
}

func TestProfiles_ListsEveryProfile(t *testing.T) {
	t.Parallel()

	d := newTestProfiles(t, "/work")
	require.Len(t, d.list.FilteredItems(), len(profiling.Profiles))

	item, ok := d.list.FilteredItems()[0].(*ProfileItem)
	require.True(t, ok)
	require.Equal(t, profiling.Profiles[0].Name, item.ID())
}

func TestProfiles_HandleMsg_Close(t *testing.T) {
	t.Parallel()

	d := newTestProfiles(t, "/work")
	require.Equal(t, ActionClose{}, d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape}))
}

func TestProfiles_HandleMsg_Navigation(t *testing.T) {
	t.Parallel()

	d := newTestProfiles(t, "/work")
	require.True(t, d.list.IsSelectedFirst())

	d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyUp})
	require.True(t, d.list.IsSelectedLast(), "up from the first item must wrap to the last")

	d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	require.True(t, d.list.IsSelectedFirst(), "down from the last item must wrap to the first")
}

// TestProfiles_HandleMsg_Select verifies enter emits the highlighted profile
// together with the working directory the dialog was opened with.
func TestProfiles_HandleMsg_Select(t *testing.T) {
	t.Parallel()

	d := newTestProfiles(t, "/work")
	d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})

	resp, ok := d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter}).(ActionSelectExportProfile)
	require.True(t, ok)
	require.Equal(t, profiling.Profiles[1], resp.Profile)
	require.Equal(t, "/work", resp.WorkingDir)
}

// TestProfiles_HandleMsg_TypingFiltersList verifies free text narrows the
// list, matching on the profile name.
func TestProfiles_HandleMsg_TypingFiltersList(t *testing.T) {
	t.Parallel()

	d := newTestProfiles(t, "/work")
	for _, r := range "goroutine" {
		d.HandleMsg(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	require.Len(t, d.list.FilteredItems(), 1)
	item, ok := d.list.SelectedItem().(*ProfileItem)
	require.True(t, ok)
	require.Equal(t, "goroutine", item.ID())
}

func TestProfiles_Draw(t *testing.T) {
	t.Parallel()

	d := newTestProfiles(t, "/work")
	const w, h = 80, 24
	scr := uv.NewScreenBuffer(w, h)
	d.Draw(scr, image.Rect(0, 0, w, h))

	view := ansi.Strip(scr.Render())
	require.Contains(t, view, "Export Profile")
	for _, p := range profiling.Profiles {
		require.Contains(t, view, p.Name)
	}
}

func TestProfiles_ShortAndFullHelp(t *testing.T) {
	t.Parallel()

	d := newTestProfiles(t, "/work")
	require.Len(t, d.ShortHelp(), 3)

	var total int
	for _, row := range d.FullHelp() {
		total += len(row)
	}
	require.Equal(t, 4, total)
}
