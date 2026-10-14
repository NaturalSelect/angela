package dialog

import (
	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/profiling"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/list"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/sahilm/fuzzy"
)

// ProfilesID is the identifier for the "Export Profile" picker dialog.
const ProfilesID = "profiles"

// Profiles is the first step of the "Export Profile" flow: a picker over the
// runtime profiles of this process. Selecting one hands off to the Arguments
// dialog to collect (and default) an output path.
type Profiles struct {
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

// ProfileItem wraps a profile to implement the [ListItem] interface for the
// picker's list.
type ProfileItem struct {
	*list.Versioned
	profile profiling.Profile
	t       *styles.Styles
	m       fuzzy.Match
	cache   map[int]string
	focused bool
}

// Finished implements list.Item. Profile items are render-stable outside of
// explicit SetFocused / SetMatch calls.
func (i *ProfileItem) Finished() bool {
	return true
}

var (
	_ Dialog   = (*Profiles)(nil)
	_ ListItem = (*ProfileItem)(nil)
)

// NewProfiles creates a new "Export Profile" picker dialog over the given
// profiles. workingDir is passed through to the next step so it can default
// the output path.
func NewProfiles(com *common.Common, profiles []profiling.Profile, workingDir string) *Profiles {
	d := &Profiles{com: com, workingDir: workingDir}

	d.frame = NewFrame(com.Styles, FrameSpec{
		Title:     "Export Profile",
		MaxWidth:  defaultDialogMaxWidth,
		MinHeight: 8,
		MaxHeight: 16,
	})

	h := help.New()
	h.Styles = com.Styles.DialogHelpStyles()
	d.help = h

	items := make([]list.FilterableItem, len(profiles))
	for i, p := range profiles {
		items[i] = &ProfileItem{Versioned: list.NewVersioned(), profile: p, t: com.Styles}
	}
	d.list = list.NewFilterableList(items...)
	d.list.Focus()
	d.list.SetSelected(0)
	d.list.ScrollToSelected()

	d.input = textinput.New()
	d.input.SetVirtualCursor(false)
	d.input.Placeholder = "Type to filter profiles"
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
func (d *Profiles) ID() string {
	return ProfilesID
}

// HandleMsg implements [Dialog].
func (d *Profiles) HandleMsg(msg tea.Msg) Action {
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
			profileItem, ok := selectedItem.(*ProfileItem)
			if !ok {
				break
			}
			return ActionSelectExportProfile{Profile: profileItem.profile, WorkingDir: d.workingDir}
		default:
			var cmd tea.Cmd
			d.input, cmd = d.input.Update(msg)
			d.list.SetFilter(d.input.Value())
			d.list.ScrollToTop()
			d.list.SetSelected(0)
			return ActionCmd{cmd}
		}
	}
	return nil
}

// Cursor returns the cursor position relative to the dialog.
func (d *Profiles) Cursor() *tea.Cursor {
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
func (d *Profiles) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := d.com.Styles
	d.metrics = d.frame.FitHeight(area, d.frame.ListHeightOffset()+d.list.TotalHeight())

	d.input.SetWidth(d.frame.InputTextWidth(d.input, d.metrics.ContentWidth))

	listHeight, listTotalHeight, _ := d.frame.SizeList(d.list, d.metrics)

	if d.list.Height() >= len(d.list.FilteredItems()) {
		d.list.ScrollToTop()
	} else {
		d.list.ScrollToSelected()
	}

	listView := t.Dialog.List.Height(d.list.Height()).Render(d.list.Render())
	listView = d.frame.JoinScrollbar(listView, listHeight, listTotalHeight, listHeight, d.list.Offset())

	view := d.frame.Render(d.metrics,
		[]string{t.Dialog.InputPrompt.Render(d.input.View()), listView},
		d.frame.RenderHelp(&d.help, d, d.metrics.ContentWidth),
	)

	cur := d.Cursor()
	return d.frame.Draw(scr, area, view, cur)
}

// ShortHelp implements [help.KeyMap].
func (d *Profiles) ShortHelp() []key.Binding {
	return []key.Binding{
		d.keyMap.UpDown,
		d.keyMap.Select,
		d.keyMap.Close,
	}
}

// FullHelp implements [help.KeyMap].
func (d *Profiles) FullHelp() [][]key.Binding {
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

// Filter returns the filterable value of the profile item: its name and
// description, so the user can search by what the data contains.
func (i *ProfileItem) Filter() string {
	return i.profile.Name + " " + i.profile.Short
}

// ID returns the unique identifier of the profile.
func (i *ProfileItem) ID() string {
	return i.profile.Name
}

// SetFocused sets the focus state of the profile item.
func (i *ProfileItem) SetFocused(focused bool) {
	if i.focused == focused {
		return
	}
	i.cache = nil
	i.focused = focused
	if i.Versioned != nil {
		i.Bump()
	}
}

// SetMatch sets the fuzzy match for the profile item.
func (i *ProfileItem) SetMatch(m fuzzy.Match) {
	if sameFuzzyMatch(i.m, m) {
		return
	}
	i.cache = nil
	i.m = m
	if i.Versioned != nil {
		i.Bump()
	}
}

// Render returns the string representation of the profile item: its name on
// the left, and what it records on the right.
func (i *ProfileItem) Render(width int) string {
	st := ListItemStyles{
		ItemBlurred:     i.t.Dialog.NormalItem,
		ItemFocused:     i.t.Dialog.SelectedItem,
		InfoTextBlurred: i.t.Dialog.Sessions.InfoBlurred,
		InfoTextFocused: i.t.Dialog.Sessions.InfoFocused,
	}
	return renderItem(st, i.profile.Name, i.profile.Short, i.focused, width, i.cache, &i.m)
}
