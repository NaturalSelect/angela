package dialog

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/images"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/list"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/dustin/go-humanize"
	"github.com/sahilm/fuzzy"
)

// ImagesID is the identifier for the "Export Image" picker dialog.
const ImagesID = "images"

// Images is the first step of the "Export Image" flow: a picker over
// the current session's generated images. Selecting one hands off to
// the Arguments dialog to collect (and default) an output path.
type Images struct {
	com        *common.Common
	help       help.Model
	list       *list.FilterableList
	input      textinput.Model
	workingDir string

	frame   *Frame
	metrics FrameMetrics

	keyMap struct {
		Select   key.Binding
		Next     key.Binding
		Previous key.Binding
		UpDown   key.Binding
		Close    key.Binding
	}
}

// ImageItem wraps a generated image to implement the [ListItem]
// interface for the picker's list.
type ImageItem struct {
	*list.Versioned
	image   images.Image
	t       *styles.Styles
	m       fuzzy.Match
	cache   map[int]string
	focused bool
}

// Finished implements list.Item. Image items are render-stable outside
// of explicit SetFocused / SetMatch calls.
func (i *ImageItem) Finished() bool {
	return true
}

var (
	_ Dialog   = (*Images)(nil)
	_ ListItem = (*ImageItem)(nil)
)

// NewImages creates a new "Export Image" picker dialog over a
// session's generated images. workingDir is passed through to the
// next step so it can default the output path.
func NewImages(com *common.Common, imgs []images.Image, workingDir string) *Images {
	d := &Images{com: com, workingDir: workingDir}

	d.frame = NewFrame(com.Styles, FrameSpec{
		Title:     "Export Image",
		MaxWidth:  defaultDialogMaxWidth,
		MaxHeight: defaultDialogHeight,
	})

	h := help.New()
	h.Styles = com.Styles.DialogHelpStyles()
	d.help = h

	items := make([]list.FilterableItem, len(imgs))
	for i, img := range imgs {
		items[i] = &ImageItem{Versioned: list.NewVersioned(), image: img, t: com.Styles}
	}
	d.list = list.NewFilterableList(items...)
	d.list.Focus()
	d.list.SetSelected(0)
	d.list.ScrollToSelected()

	d.input = textinput.New()
	d.input.SetVirtualCursor(false)
	d.input.Placeholder = "Type to filter images"
	d.input.SetStyles(com.Styles.TextInput)
	d.input.Focus()

	d.keyMap.Select = key.NewBinding(
		key.WithKeys("enter", "ctrl+y"),
		key.WithHelp("enter", "export"),
	)
	d.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "ctrl+n"),
		key.WithHelp("↓", "next item"),
	)
	d.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑", "previous item"),
	)
	d.keyMap.UpDown = key.NewBinding(
		key.WithKeys("up", "down"),
		key.WithHelp("↑/↓", "choose"),
	)
	d.keyMap.Close = CloseKey

	return d
}

// ID implements Dialog.
func (d *Images) ID() string {
	return ImagesID
}

// HandleMsg implements [Dialog].
func (d *Images) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, d.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, d.keyMap.Previous):
			d.list.Focus()
			if d.list.IsSelectedFirst() {
				d.list.SelectLast()
				d.list.ScrollToBottom()
				break
			}
			d.list.SelectPrev()
			d.list.ScrollToSelected()
		case key.Matches(msg, d.keyMap.Next):
			d.list.Focus()
			if d.list.IsSelectedLast() {
				d.list.SelectFirst()
				d.list.ScrollToTop()
				break
			}
			d.list.SelectNext()
			d.list.ScrollToSelected()
		case key.Matches(msg, d.keyMap.Select):
			selectedItem := d.list.SelectedItem()
			if selectedItem == nil {
				break
			}
			imageItem, ok := selectedItem.(*ImageItem)
			if !ok {
				break
			}
			return ActionSelectExportImage{Image: imageItem.image, WorkingDir: d.workingDir}
		default:
			var cmd tea.Cmd
			d.input, cmd = d.input.Update(msg)
			value := d.input.Value()
			d.list.SetFilter(value)
			d.list.ScrollToTop()
			d.list.SetSelected(0)
			return ActionCmd{cmd}
		}
	}
	return nil
}

// Cursor returns the cursor position relative to the dialog.
func (d *Images) Cursor() *tea.Cursor {
	cur := d.input.Cursor()
	if cur != nil {
		// textinput.Cursor() offsets X by rune count, not display width;
		// correct for double-width runes (CJK).
		value := []rune(d.input.Value())
		p := d.input.Position()
		cur.X += lipgloss.Width(string(value[:p])) - p
	}
	return InputCursor(d.com.Styles, cur)
}

// Draw implements [Dialog].
func (d *Images) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := d.com.Styles
	d.metrics = d.frame.Measure(area)

	d.input.SetWidth(d.frame.InputTextWidth(d.input, d.metrics.ContentWidth))
	d.frame.SizeList(d.list, d.metrics)

	inputView := t.Dialog.InputPrompt.Render(d.input.View())

	visibleCount := len(d.list.FilteredItems())
	if d.list.Height() >= visibleCount {
		d.list.ScrollToTop()
	} else {
		d.list.ScrollToSelected()
	}

	listView := t.Dialog.List.Height(d.list.Height()).Render(d.list.Render())

	view := d.frame.Render(d.metrics,
		[]string{inputView, listView},
		d.frame.RenderHelp(&d.help, d, d.metrics.ContentWidth),
	)

	cur := d.Cursor()
	return d.frame.Draw(scr, area, view, cur)
}

// ShortHelp implements [help.KeyMap].
func (d *Images) ShortHelp() []key.Binding {
	return []key.Binding{
		d.keyMap.UpDown,
		d.keyMap.Select,
		d.keyMap.Close,
	}
}

// FullHelp implements [help.KeyMap].
func (d *Images) FullHelp() [][]key.Binding {
	m := [][]key.Binding{}
	slice := []key.Binding{
		d.keyMap.Select,
		d.keyMap.Next,
		d.keyMap.Previous,
		d.keyMap.Close,
	}
	for i := 0; i < len(slice); i += 4 {
		end := min(i+4, len(slice))
		m = append(m, slice[i:end])
	}
	return m
}

// imageTitle collapses a generated image's prompt to a single line
// for the picker row, falling back to the image ID when there is no
// prompt to show (e.g. a very old or malformed record).
func imageTitle(img images.Image) string {
	title := strings.Join(strings.Fields(img.Prompt), " ")
	if title == "" {
		return img.ID
	}
	return title
}

// Filter returns the filterable value of the image item: its prompt
// and ID, so the user can also jump straight to a known image ID.
func (i *ImageItem) Filter() string {
	return imageTitle(i.image) + " " + i.image.ID
}

// ID returns the unique identifier of the image.
func (i *ImageItem) ID() string {
	return i.image.ID
}

// SetFocused sets the focus state of the image item.
func (i *ImageItem) SetFocused(focused bool) {
	if i.focused == focused {
		return
	}
	i.cache = nil
	i.focused = focused
	if i.Versioned != nil {
		i.Bump()
	}
}

// SetMatch sets the fuzzy match for the image item.
func (i *ImageItem) SetMatch(m fuzzy.Match) {
	if sameFuzzyMatch(i.m, m) {
		return
	}
	i.cache = nil
	i.m = m
	if i.Versioned != nil {
		i.Bump()
	}
}

// Render returns the string representation of the image item: its
// prompt on the left, and a relative timestamp on the right.
func (i *ImageItem) Render(width int) string {
	info := humanize.Time(time.Unix(i.image.CreatedAt, 0))
	st := ListItemStyles{
		ItemBlurred:     i.t.Dialog.NormalItem,
		ItemFocused:     i.t.Dialog.SelectedItem,
		InfoTextBlurred: i.t.Dialog.Sessions.InfoBlurred,
		InfoTextFocused: i.t.Dialog.Sessions.InfoFocused,
	}
	return renderItem(st, imageTitle(i.image), info, i.focused, width, i.cache, &i.m)
}
