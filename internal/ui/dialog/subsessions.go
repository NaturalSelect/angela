package dialog

import (
	"image"
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

	// subSessionMinRowWidth is the room a row keeps for its own text before
	// the Stop button is dropped, so a narrow list never trades the title
	// away for the button.
	subSessionMinRowWidth = 16
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

	items []*SubSessionItem
	// stopFocused is true while the keyboard focus sits on the Stop button
	// of the selected row instead of on the row itself.
	stopFocused bool
	// listRect and listWidth are where the list was last drawn, in screen
	// coordinates, so a mouse event can be matched to a row and its button.
	listRect  image.Rectangle
	listWidth int

	keyMap struct {
		Select   key.Binding
		Stop     key.Binding
		Tab      key.Binding
		Next     key.Binding
		Previous key.Binding
		UpDown   key.Binding
		Close    key.Binding
	}
}

// SubSessionItem is one row of the [SubSessions] list.
type SubSessionItem struct {
	*list.Versioned
	entry       SubSessionEntry
	t           *styles.Styles
	m           fuzzy.Match
	cache       map[int]string
	focused     bool
	stopFocused bool
	stopHovered bool
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
	s.keyMap.Tab = key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "stop button"),
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
			s.setStopFocus(false)
			s.list.Focus()
			if s.list.IsSelectedFirst() {
				s.list.SelectLast()
				s.list.ScrollToBottom()
				break
			}
			s.list.SelectPrev()
			s.list.ScrollToSelected()
		case key.Matches(msg, s.keyMap.Next):
			s.setStopFocus(false)
			s.list.Focus()
			if s.list.IsSelectedLast() {
				s.list.SelectFirst()
				s.list.ScrollToTop()
				break
			}
			s.list.SelectNext()
			s.list.ScrollToSelected()
		case key.Matches(msg, s.keyMap.Select):
			if s.stopFocused {
				return s.stopSelected()
			}
			item, ok := s.list.SelectedItem().(*SubSessionItem)
			if !ok {
				break
			}
			return ActionEnterSubSession{SessionID: item.entry.SessionID}
		case key.Matches(msg, s.keyMap.Stop):
			// A row that cannot be stopped swallows the key: handing it to
			// the filter box would type nothing, but it would also move the
			// cursor for a key the user meant as a command.
			return s.stopSelected()
		case key.Matches(msg, s.keyMap.Tab):
			if s.selectedStoppable() != nil {
				s.setStopFocus(!s.stopFocused)
			}
		default:
			s.setStopFocus(false)
			var cmd tea.Cmd
			s.input, cmd = s.input.Update(msg)
			s.list.SetFilter(s.input.Value())
			s.list.ScrollToTop()
			s.list.SetSelected(0)
			return ActionCmd{cmd}
		}
	case tea.MouseClickMsg:
		if msg.Button == uv.MouseLeft {
			return s.click(msg.X, msg.Y)
		}
	case tea.MouseMotionMsg:
		s.hover(msg.X, msg.Y)
	}
	return nil
}

// click selects the row under the pointer and stops its agent when the
// pointer is on the Stop button. A click elsewhere on the row only selects
// it, so entering a sub-session stays a deliberate keyboard action.
func (s *SubSessions) click(x, y int) Action {
	idx, item := s.rowAt(x, y)
	if item == nil {
		return nil
	}
	s.list.SetSelected(idx)
	s.setStopFocus(false)
	if item.overStop(x-s.listRect.Min.X, s.listWidth) {
		return s.stopSelected()
	}
	return nil
}

func (s *SubSessions) hover(x, y int) {
	_, item := s.rowAt(x, y)
	over := item != nil && item.overStop(x-s.listRect.Min.X, s.listWidth)
	for _, it := range s.items {
		it.setStopHovered(over && it == item)
	}
}

// rowAt returns the list index and row under a screen position, or nil when
// the position is outside the list or on no row.
func (s *SubSessions) rowAt(x, y int) (int, *SubSessionItem) {
	if !image.Pt(x, y).In(s.listRect) {
		return -1, nil
	}
	idx, _ := s.list.ItemIndexAtPosition(x-s.listRect.Min.X, y-s.listRect.Min.Y)
	item, _ := s.list.ItemAt(idx).(*SubSessionItem)
	return idx, item
}

// setStopFocus moves the keyboard focus onto the selected row's Stop button
// or back to the row.
func (s *SubSessions) setStopFocus(focused bool) {
	s.stopFocused = focused
	selected := s.selectedStoppable()
	for _, item := range s.items {
		item.setStopFocused(focused && item == selected)
	}
}

// stopSelected ends the selected row's agent and leaves the dialog open with
// the row showing the outcome, so several agents can be stopped in a row. It
// returns nil when the selected row cannot be stopped.
func (s *SubSessions) stopSelected() Action {
	item := s.selectedStoppable()
	if item == nil {
		return nil
	}
	item.markStopped()
	s.setStopFocus(false)
	return ActionStopSubSession{SessionID: item.entry.SessionID}
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

	listHeight, listTotalHeight, listWidth := s.frame.SizeList(s.list, s.metrics)

	if s.list.Height() >= len(s.list.FilteredItems()) {
		s.list.ScrollToTop()
	} else {
		s.list.ScrollToSelected()
	}

	listView := t.Dialog.List.Height(s.list.Height()).Render(s.list.Render())
	listView = s.frame.JoinScrollbar(listView, listHeight, listTotalHeight, listHeight, s.list.Offset())

	inputView := t.Dialog.InputPrompt.Render(s.input.View())
	view := s.frame.Render(s.metrics,
		[]string{inputView, listView},
		s.frame.RenderHelp(&s.help, s, s.metrics.ContentWidth),
	)
	s.locateList(area, view, lipgloss.Height(inputView), listWidth)

	return s.frame.Draw(scr, area, view, s.Cursor())
}

// locateList records where the list lands on screen. It mirrors how
// [Frame.Draw] centers the view and how the frame stacks the title and the
// input above the list.
func (s *SubSessions) locateList(area uv.Rectangle, view string, inputHeight, listWidth int) {
	t := s.com.Styles
	vw, vh := lipgloss.Size(view)
	center := common.CenterRect(area, min(vw, area.Dx()), min(vh, area.Dy()))
	dialogStyle, listStyle := t.Dialog.View, t.Dialog.List

	x := center.Min.X + dialogStyle.GetBorderLeftSize() + dialogStyle.GetPaddingLeft() +
		dialogStyle.GetMarginLeft() + listStyle.GetMarginLeft()
	y := center.Min.Y + dialogStyle.GetBorderTopSize() + dialogStyle.GetPaddingTop() +
		dialogStyle.GetMarginTop() + t.Dialog.Title.GetVerticalFrameSize() +
		titleContentHeight + inputHeight + listStyle.GetMarginTop()

	s.listRect = image.Rect(x, y, x+listWidth, y+s.list.Height())
	s.listWidth = listWidth
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

// selectBinding is the enter binding, relabeled while the Stop button has
// the focus because enter then ends the agent instead of entering it.
func (s *SubSessions) selectBinding() key.Binding {
	if !s.stopFocused {
		return s.keyMap.Select
	}
	return key.NewBinding(
		key.WithKeys(s.keyMap.Select.Keys()...),
		key.WithHelp("enter", "stop agent"),
	)
}

// ShortHelp implements [help.KeyMap].
func (s *SubSessions) ShortHelp() []key.Binding {
	if s.selectedStoppable() == nil {
		return []key.Binding{s.keyMap.UpDown, s.selectBinding(), s.keyMap.Close}
	}
	// NOTE: The choose hint makes way for the two stop hints; with it the
	// line outgrows the dialog and the close hint is the first to be cut.
	return []key.Binding{s.selectBinding(), s.keyMap.Tab, s.keyMap.Stop, s.keyMap.Close}
}

// FullHelp implements [help.KeyMap].
func (s *SubSessions) FullHelp() [][]key.Binding {
	bindings := []key.Binding{s.selectBinding()}
	if s.selectedStoppable() != nil {
		bindings = append(bindings, s.keyMap.Tab, s.keyMap.Stop)
	}
	return [][]key.Binding{append(bindings, s.keyMap.Next, s.keyMap.Previous, s.keyMap.Close)}
}

func (s *SubSessions) setItems(entries []SubSessionEntry) {
	s.items = make([]*SubSessionItem, 0, len(entries))
	for _, wantActive := range []bool{true, false} {
		for _, entry := range slices.Backward(entries) {
			if entry.State.active() != wantActive {
				continue
			}
			s.items = append(s.items, &SubSessionItem{
				Versioned: list.NewVersioned(),
				entry:     entry,
				t:         s.com.Styles,
			})
		}
	}

	items := make([]list.FilterableItem, len(s.items))
	for i, item := range s.items {
		items[i] = item
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

func (i *SubSessionItem) setStopFocused(focused bool) {
	if i.stopFocused == focused {
		return
	}
	i.stopFocused = focused
	i.invalidate()
}

func (i *SubSessionItem) setStopHovered(hovered bool) {
	if i.stopHovered == hovered {
		return
	}
	i.stopHovered = hovered
	i.invalidate()
}

// markStopped shows the outcome of a stop the user just asked for, without
// waiting for the transcript to catch up.
func (i *SubSessionItem) markStopped() {
	i.entry.State = SubSessionStopped
	i.entry.Stoppable = false
	i.stopFocused = false
	i.stopHovered = false
	i.invalidate()
}

func (i *SubSessionItem) invalidate() {
	i.cache = nil
	if i.Versioned != nil {
		i.Bump()
	}
}

// showsStop reports whether a row of the given width has room for its Stop
// button. Render and the mouse hit test both ask it, so they cannot disagree.
func (i *SubSessionItem) showsStop(width int) bool {
	return i.entry.Stoppable && width >= stopButtonWidth(i.t)+1+subSessionMinRowWidth
}

// overStop reports whether a column of a row of the given width is on the
// Stop button, which is pinned to the right edge.
func (i *SubSessionItem) overStop(column, width int) bool {
	return i.showsStop(width) && column >= width-stopButtonWidth(i.t) && column < width
}

func stopButton(t *styles.Styles, focused, hovered bool) string {
	return common.Button(t, common.ButtonOpts{
		Text:           "Stop",
		Padding:        1,
		UnderlineIndex: -1,
		Negative:       true,
		Selected:       focused,
		Hovered:        hovered,
	})
}

func stopButtonWidth(t *styles.Styles) int {
	return lipgloss.Width(stopButton(t, false, false))
}

// Render returns the string representation of the sub-session item.
func (i *SubSessionItem) Render(width int) string {
	itemStyles := ListItemStyles{
		ItemBlurred:     i.t.Dialog.NormalItem,
		ItemFocused:     i.t.Dialog.SelectedItem,
		InfoTextBlurred: i.t.Dialog.ListItem.InfoBlurred,
		InfoTextFocused: i.t.Dialog.ListItem.InfoFocused,
	}
	if !i.showsStop(width) {
		return renderItem(itemStyles, i.title(), i.entry.State.label(), i.focused, width, i.cache, &i.m)
	}
	rowWidth := width - stopButtonWidth(i.t) - 1
	row := renderItem(itemStyles, i.title(), i.entry.State.label(), i.focused, rowWidth, i.cache, &i.m)
	return row + " " + stopButton(i.t, i.stopFocused, i.stopHovered)
}
