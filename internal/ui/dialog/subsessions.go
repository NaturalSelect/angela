package dialog

import (
	"slices"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/list"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/sahilm/fuzzy"
)

const (
	// SubSessionsID is the identifier for the sub-session picker dialog.
	SubSessionsID = "sub-sessions"

	subSessionsDialogMaxWidth  = 80
	subSessionsDialogMinHeight = 8
	subSessionsDialogMaxHeight = 20
)

// SubSessionState is where a sub-session stands from the parent's view.
type SubSessionState uint8

const (
	// SubSessionRunning means the dispatching tool call has no result yet.
	SubSessionRunning SubSessionState = iota
	// SubSessionWaiting means a branch agent is waiting for the user.
	SubSessionWaiting
	// SubSessionDone means the sub-agent finished successfully.
	SubSessionDone
	// SubSessionFailed means the sub-agent errored or was canceled.
	SubSessionFailed
	// SubSessionStopped means a background sub-agent was stopped on purpose,
	// which is neither a success nor a failure of its own.
	SubSessionStopped
)

// active reports whether the sub-session still needs attention.
func (s SubSessionState) active() bool {
	return s == SubSessionRunning || s == SubSessionWaiting
}

func (s SubSessionState) marker() string {
	switch s {
	case SubSessionRunning:
		return "●"
	case SubSessionWaiting:
		return "◆"
	case SubSessionDone:
		return "✓"
	case SubSessionStopped:
		return "■"
	default:
		return "✗"
	}
}

func (s SubSessionState) label() string {
	switch s {
	case SubSessionRunning:
		return "running"
	case SubSessionWaiting:
		return "waiting for you"
	case SubSessionDone:
		return "done"
	case SubSessionStopped:
		return "stopped"
	default:
		return "failed"
	}
}

// SubSessionEntry describes one sub-session of the session in view. It is
// plain data so the dialog does not depend on how the transcript is built.
type SubSessionEntry struct {
	// SessionID is the ID of the child session to enter.
	SessionID string
	// Agent is the name of the sub-agent that runs in it.
	Agent string
	// Task is the short description of the work it was given.
	Task  string
	State SubSessionState
	// Stoppable is true for a running background agent, the only kind the
	// user can end from the list.
	Stoppable bool
}

// SubSessions is a dialog for picking one of the current session's
// sub-sessions to enter.
type SubSessions struct {
	com   *common.Common
	help  help.Model
	list  *list.FilterableList
	input textinput.Model

	frame   *Frame
	metrics FrameMetrics

	keyMap struct {
		Select   key.Binding
		Stop     key.Binding
		Next     key.Binding
		Previous key.Binding
		UpDown   key.Binding
		Close    key.Binding
	}
}

// SubSessionItem is one row of the [SubSessions] list.
type SubSessionItem struct {
	*list.Versioned
	entry   SubSessionEntry
	t       *styles.Styles
	m       fuzzy.Match
	cache   map[int]string
	focused bool
}

var (
	_ Dialog   = (*SubSessions)(nil)
	_ ListItem = (*SubSessionItem)(nil)
)

// NewSubSessions creates the sub-session picker. entries arrive in
// transcript order; running ones are listed first, and within each group the
// newest comes first, which is what the user most likely came to find.
func NewSubSessions(com *common.Common, entries []SubSessionEntry) *SubSessions {
	s := &SubSessions{com: com}

	s.frame = NewFrame(com.Styles, FrameSpec{
		Title:     "Sub-sessions",
		MaxWidth:  subSessionsDialogMaxWidth,
		MinHeight: subSessionsDialogMinHeight,
		MaxHeight: subSessionsDialogMaxHeight,
	})

	h := help.New()
	h.Styles = com.Styles.DialogHelpStyles()
	s.help = h

	s.list = list.NewFilterableList()
	s.list.Focus()

	s.input = textinput.New()
	s.input.SetVirtualCursor(false)
	s.input.Placeholder = "Type to filter"
	s.input.SetStyles(com.Styles.TextInput)
	s.input.Focus()

	s.keyMap.Select = key.NewBinding(
		key.WithKeys("enter", "ctrl+y"),
		key.WithHelp("enter", "enter sub-session"),
	)
	s.keyMap.Stop = key.NewBinding(
		key.WithKeys("ctrl+x"),
		key.WithHelp("ctrl+x", "stop agent"),
	)
	s.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "ctrl+n"),
		key.WithHelp("↓", "next item"),
	)
	s.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑", "previous item"),
	)
	s.keyMap.UpDown = key.NewBinding(
		key.WithKeys("up", "down"),
		key.WithHelp("↑/↓", "choose"),
	)
	s.keyMap.Close = CloseKey

	s.setItems(entries)

	return s
}

// ID implements Dialog.
func (s *SubSessions) ID() string {
	return SubSessionsID
}

// HandleMsg implements [Dialog].
func (s *SubSessions) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, s.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, s.keyMap.Previous):
			s.list.Focus()
			if s.list.IsSelectedFirst() {
				s.list.SelectLast()
				s.list.ScrollToBottom()
				break
			}
			s.list.SelectPrev()
			s.list.ScrollToSelected()
		case key.Matches(msg, s.keyMap.Next):
			s.list.Focus()
			if s.list.IsSelectedLast() {
				s.list.SelectFirst()
				s.list.ScrollToTop()
				break
			}
			s.list.SelectNext()
			s.list.ScrollToSelected()
		case key.Matches(msg, s.keyMap.Select):
			item, ok := s.list.SelectedItem().(*SubSessionItem)
			if !ok {
				break
			}
			return ActionEnterSubSession{SessionID: item.entry.SessionID}
		case key.Matches(msg, s.keyMap.Stop):
			// A row that cannot be stopped swallows the key: handing it to
			// the filter box would type nothing, but it would also move the
			// cursor for a key the user meant as a command.
			if item := s.selectedStoppable(); item != nil {
				return ActionStopSubSession{SessionID: item.entry.SessionID}
			}
		default:
			var cmd tea.Cmd
			s.input, cmd = s.input.Update(msg)
			s.list.SetFilter(s.input.Value())
			s.list.ScrollToTop()
			s.list.SetSelected(0)
			return ActionCmd{cmd}
		}
	}
	return nil
}

// Cursor returns the cursor position relative to the dialog.
func (s *SubSessions) Cursor() *tea.Cursor {
	cur := s.input.Cursor()
	if cur != nil {
		// textinput.Cursor() offsets X by rune count, not display width;
		// correct for double-width runes (CJK).
		value := []rune(s.input.Value())
		n := s.input.Position()
		cur.X += lipgloss.Width(string(value[:n])) - n
	}
	return InputCursor(s.com.Styles, cur)
}

// Draw implements [Dialog].
func (s *SubSessions) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := s.com.Styles
	s.metrics = s.frame.FitHeight(area, s.frame.ListHeightOffset()+s.list.TotalHeight())

	s.input.SetWidth(s.frame.InputTextWidth(s.input, s.metrics.ContentWidth))

	listHeight, listTotalHeight, _ := s.frame.SizeList(s.list, s.metrics)

	if s.list.Height() >= len(s.list.FilteredItems()) {
		s.list.ScrollToTop()
	} else {
		s.list.ScrollToSelected()
	}

	listView := t.Dialog.List.Height(s.list.Height()).Render(s.list.Render())
	listView = s.frame.JoinScrollbar(listView, listHeight, listTotalHeight, listHeight, s.list.Offset())

	view := s.frame.Render(s.metrics,
		[]string{t.Dialog.InputPrompt.Render(s.input.View()), listView},
		s.frame.RenderHelp(&s.help, s, s.metrics.ContentWidth),
	)

	return s.frame.Draw(scr, area, view, s.Cursor())
}

// selectedStoppable returns the highlighted row when the user can end it
// from the list, and nil otherwise.
func (s *SubSessions) selectedStoppable() *SubSessionItem {
	item, ok := s.list.SelectedItem().(*SubSessionItem)
	if !ok || !item.entry.Stoppable {
		return nil
	}
	return item
}

// ShortHelp implements [help.KeyMap].
func (s *SubSessions) ShortHelp() []key.Binding {
	bindings := []key.Binding{s.keyMap.UpDown, s.keyMap.Select}
	if s.selectedStoppable() != nil {
		bindings = append(bindings, s.keyMap.Stop)
	}
	return append(bindings, s.keyMap.Close)
}

// FullHelp implements [help.KeyMap].
func (s *SubSessions) FullHelp() [][]key.Binding {
	bindings := []key.Binding{s.keyMap.Select}
	if s.selectedStoppable() != nil {
		bindings = append(bindings, s.keyMap.Stop)
	}
	return [][]key.Binding{append(bindings, s.keyMap.Next, s.keyMap.Previous, s.keyMap.Close)}
}

func (s *SubSessions) setItems(entries []SubSessionEntry) {
	items := make([]list.FilterableItem, 0, len(entries))
	for _, wantActive := range []bool{true, false} {
		for _, entry := range slices.Backward(entries) {
			if entry.State.active() != wantActive {
				continue
			}
			items = append(items, &SubSessionItem{
				Versioned: list.NewVersioned(),
				entry:     entry,
				t:         s.com.Styles,
			})
		}
	}

	s.list.SetItems(items...)
	s.list.SetSelected(0)
	s.list.ScrollToSelected()
}

// Finished implements list.Item. Sub-session items are render-stable
// outside of explicit SetFocused / SetMatch.
func (i *SubSessionItem) Finished() bool {
	return true
}

// title reads like the agent block in the transcript.
func (i *SubSessionItem) title() string {
	title := i.entry.State.marker() + " " + toolnames.Agent
	if i.entry.Agent != "" {
		title += "(" + i.entry.Agent + ")"
	}
	if i.entry.Task != "" {
		title += " — " + i.entry.Task
	}
	return title
}

// Filter returns the filter value for the sub-session item.
func (i *SubSessionItem) Filter() string {
	return i.entry.Agent + " " + i.entry.Task + " " + i.entry.State.label()
}

// ID returns the ID of the child session behind the item.
func (i *SubSessionItem) ID() string {
	return i.entry.SessionID
}

// SetFocused sets the focus state of the sub-session item.
func (i *SubSessionItem) SetFocused(focused bool) {
	if i.focused == focused {
		return
	}
	i.cache = nil
	i.focused = focused
	if i.Versioned != nil {
		i.Bump()
	}
}

// SetMatch sets the fuzzy match for the sub-session item.
func (i *SubSessionItem) SetMatch(m fuzzy.Match) {
	if sameFuzzyMatch(i.m, m) {
		return
	}
	i.cache = nil
	i.m = m
	if i.Versioned != nil {
		i.Bump()
	}
}

// Render returns the string representation of the sub-session item.
func (i *SubSessionItem) Render(width int) string {
	itemStyles := ListItemStyles{
		ItemBlurred:     i.t.Dialog.NormalItem,
		ItemFocused:     i.t.Dialog.SelectedItem,
		InfoTextBlurred: i.t.Dialog.ListItem.InfoBlurred,
		InfoTextFocused: i.t.Dialog.ListItem.InfoFocused,
	}
	return renderItem(itemStyles, i.title(), i.entry.State.label(), i.focused, width, i.cache, &i.m)
}
