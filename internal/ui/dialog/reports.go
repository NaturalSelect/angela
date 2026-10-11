package dialog

import (
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/list"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/sahilm/fuzzy"
)

// ReportsID is the identifier for the "Export Report" picker dialog.
const ReportsID = "reports"

// Reports is the first step of the "Export Report" flow: a picker over
// the agent reports the current session has collected. Selecting one
// hands off to the Arguments dialog to collect (and default) an output
// path.
type Reports struct {
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

// ReportItem wraps an agent report to implement the [ListItem]
// interface for the picker's list.
type ReportItem struct {
	*list.Versioned
	report  tools.Report
	t       *styles.Styles
	m       fuzzy.Match
	cache   map[int]string
	focused bool
}

// Finished implements list.Item. Report items are render-stable outside
// of explicit SetFocused / SetMatch calls.
func (i *ReportItem) Finished() bool {
	return true
}

var (
	_ Dialog   = (*Reports)(nil)
	_ ListItem = (*ReportItem)(nil)
)

// NewReports creates a new "Export Report" picker dialog over a
// session's agent reports. workingDir is passed through to the next
// step so it can default the output path.
func NewReports(com *common.Common, reports []tools.Report, workingDir string) *Reports {
	d := &Reports{com: com, workingDir: workingDir}

	d.frame = NewFrame(com.Styles, FrameSpec{
		Title:     "Export Report",
		MaxWidth:  defaultDialogMaxWidth,
		MaxHeight: defaultDialogHeight,
	})

	h := help.New()
	h.Styles = com.Styles.DialogHelpStyles()
	d.help = h

	items := make([]list.FilterableItem, len(reports))
	for i, report := range reports {
		items[i] = &ReportItem{Versioned: list.NewVersioned(), report: report, t: com.Styles}
	}
	d.list = list.NewFilterableList(items...)
	d.list.Focus()
	d.list.SetSelected(0)
	d.list.ScrollToSelected()

	d.input = textinput.New()
	d.input.SetVirtualCursor(false)
	d.input.Placeholder = "Type to filter reports"
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
func (d *Reports) ID() string {
	return ReportsID
}

// HandleMsg implements [Dialog].
func (d *Reports) HandleMsg(msg tea.Msg) Action {
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
			reportItem, ok := selectedItem.(*ReportItem)
			if !ok {
				break
			}
			return ActionSelectExportReport{Report: reportItem.report, WorkingDir: d.workingDir}
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
func (d *Reports) Cursor() *tea.Cursor {
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
func (d *Reports) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
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
func (d *Reports) ShortHelp() []key.Binding {
	return []key.Binding{
		d.keyMap.UpDown,
		d.keyMap.Select,
		d.keyMap.Close,
	}
}

// FullHelp implements [help.KeyMap].
func (d *Reports) FullHelp() [][]key.Binding {
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

// reportTitle collapses a report's task to a single line for the picker
// row, falling back to the report ID when the dispatch had no
// description.
func reportTitle(report tools.Report) string {
	title := strings.Join(strings.Fields(report.Task), " ")
	if title == "" {
		return report.ID
	}
	return title
}

// Filter returns the filterable value of the report item: its task,
// agent type and ID, so the user can also jump straight to a known
// report ID.
func (i *ReportItem) Filter() string {
	return reportTitle(i.report) + " " + i.report.AgentType + " " + i.report.ID
}

// ID returns the unique identifier of the report.
func (i *ReportItem) ID() string {
	return i.report.ID
}

// SetFocused sets the focus state of the report item.
func (i *ReportItem) SetFocused(focused bool) {
	if i.focused == focused {
		return
	}
	i.cache = nil
	i.focused = focused
	if i.Versioned != nil {
		i.Bump()
	}
}

// SetMatch sets the fuzzy match for the report item.
func (i *ReportItem) SetMatch(m fuzzy.Match) {
	if sameFuzzyMatch(i.m, m) {
		return
	}
	i.cache = nil
	i.m = m
	if i.Versioned != nil {
		i.Bump()
	}
}

// Render returns the string representation of the report item: its task
// on the left, and the agent type that produced it on the right.
func (i *ReportItem) Render(width int) string {
	st := ListItemStyles{
		ItemBlurred:     i.t.Dialog.NormalItem,
		ItemFocused:     i.t.Dialog.SelectedItem,
		InfoTextBlurred: i.t.Dialog.Sessions.InfoBlurred,
		InfoTextFocused: i.t.Dialog.Sessions.InfoFocused,
	}
	return renderItem(st, reportTitle(i.report), i.report.AgentType, i.focused, width, i.cache, &i.m)
}
