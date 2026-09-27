package dialog

import (
	"image"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/images"
	"github.com/NaturalSelect/angela/internal/ui/common"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func twoTestImages() []images.Image {
	return []images.Image{
		{ID: "img_aaaaaaaaaaaa", Prompt: "a cat wearing a hat", CreatedAt: 1000},
		{ID: "img_bbbbbbbbbbbb", Prompt: "a dog in the rain", CreatedAt: 2000},
	}
}

func newTestImages(t *testing.T, imgs []images.Image, workingDir string) *Images {
	t.Helper()
	com := &common.Common{Styles: testStyles()}
	return NewImages(com, imgs, workingDir)
}

func TestImages_ID(t *testing.T) {
	t.Parallel()

	d := newTestImages(t, twoTestImages(), "/work")
	require.Equal(t, ImagesID, d.ID())
}

// TestImages_ListsEveryImage verifies every image passed to NewImages
// shows up in the list, in the order given (the caller, i.e. the
// backend query, is responsible for newest-first ordering).
func TestImages_ListsEveryImage(t *testing.T) {
	t.Parallel()

	d := newTestImages(t, twoTestImages(), "/work")
	require.Len(t, d.list.FilteredItems(), 2)

	item, ok := d.list.FilteredItems()[0].(*ImageItem)
	require.True(t, ok)
	require.Equal(t, "img_aaaaaaaaaaaa", item.ID())
}

func TestImages_HandleMsg_Close(t *testing.T) {
	t.Parallel()

	d := newTestImages(t, twoTestImages(), "/work")
	require.Equal(t, ActionClose{}, d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape}))
}

// TestImages_HandleMsg_Navigation verifies up/down wrap around the
// ends of the list.
func TestImages_HandleMsg_Navigation(t *testing.T) {
	t.Parallel()

	d := newTestImages(t, twoTestImages(), "/work")
	require.True(t, d.list.IsSelectedFirst())

	d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyUp})
	require.True(t, d.list.IsSelectedLast(), "up from the first item must wrap to the last")

	d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	require.True(t, d.list.IsSelectedFirst(), "down from the last item must wrap to the first")
}

// TestImages_HandleMsg_Select verifies enter emits the highlighted
// image together with the working directory the dialog was opened
// with.
func TestImages_HandleMsg_Select(t *testing.T) {
	t.Parallel()

	d := newTestImages(t, twoTestImages(), "/work")
	d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})

	action := d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	resp, ok := action.(ActionSelectExportImage)
	require.True(t, ok)
	require.Equal(t, "img_bbbbbbbbbbbb", resp.Image.ID)
	require.Equal(t, "/work", resp.WorkingDir)
}

// TestImages_HandleMsg_TypingFiltersList verifies free text narrows
// the list through the shared fuzzy filter, matching on the prompt.
func TestImages_HandleMsg_TypingFiltersList(t *testing.T) {
	t.Parallel()

	d := newTestImages(t, twoTestImages(), "/work")
	var lastAction Action
	for _, r := range "dog" {
		lastAction = d.HandleMsg(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	_, ok := lastAction.(ActionCmd)
	require.True(t, ok)
	require.Len(t, d.list.FilteredItems(), 1)
	item, ok := d.list.SelectedItem().(*ImageItem)
	require.True(t, ok)
	require.Equal(t, "img_bbbbbbbbbbbb", item.ID())
}

// TestImages_Draw verifies every image's prompt renders along with a
// relative timestamp.
func TestImages_Draw(t *testing.T) {
	t.Parallel()

	d := newTestImages(t, twoTestImages(), "/work")
	const w, h = 60, 20
	scr := uv.NewScreenBuffer(w, h)
	d.Draw(scr, image.Rect(0, 0, w, h))

	view := ansi.Strip(scr.Render())
	require.Contains(t, view, "Export Image")
	require.Contains(t, view, "a cat wearing a hat")
	require.Contains(t, view, "a dog in the rain")
	require.Contains(t, view, "ago")
}

func TestImages_ShortAndFullHelp(t *testing.T) {
	t.Parallel()

	d := newTestImages(t, twoTestImages(), "/work")
	require.Len(t, d.ShortHelp(), 3)

	full := d.FullHelp()
	var total int
	for _, row := range full {
		total += len(row)
	}
	require.Equal(t, 4, total)
}

// TestImageItem_FilterAndID verify the small list.Item /
// list.FilterableItem accessor methods used by the filterable list.
func TestImageItem_FilterAndID(t *testing.T) {
	t.Parallel()

	d := newTestImages(t, twoTestImages(), "/work")
	item, ok := d.list.SelectedItem().(*ImageItem)
	require.True(t, ok)

	require.Contains(t, item.Filter(), "a cat wearing a hat")
	require.Contains(t, item.Filter(), "img_aaaaaaaaaaaa")
	require.Equal(t, "img_aaaaaaaaaaaa", item.ID())
	require.True(t, item.Finished())
}

// TestImageItem_TitleFallsBackToIDWhenPromptIsEmpty verifies the
// picker never renders a blank row for an image with no prompt.
func TestImageItem_TitleFallsBackToIDWhenPromptIsEmpty(t *testing.T) {
	t.Parallel()

	d := newTestImages(t, []images.Image{{ID: "img_cccccccccccc", CreatedAt: 3000}}, "/work")
	item, ok := d.list.SelectedItem().(*ImageItem)
	require.True(t, ok)
	require.Equal(t, "img_cccccccccccc", imageTitle(item.image))
}
