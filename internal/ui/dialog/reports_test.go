package dialog

import (
	"image"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/ui/common"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func twoTestReports() []tools.Report {
	return []tools.Report{
		{ID: "rpt_aaaaaaaa", AgentType: "explore", Task: "find the parser"},
		{ID: "rpt_bbbbbbbb", AgentType: "deep-research", Task: "websocket vs sse"},
	}
}

func newTestReports(t *testing.T, reports []tools.Report, workingDir string) *Reports {
	t.Helper()
	com := &common.Common{Styles: testStyles()}
	return NewReports(com, reports, workingDir)
}

func TestReports_ID(t *testing.T) {
	t.Parallel()

	d := newTestReports(t, twoTestReports(), "/work")
	require.Equal(t, ReportsID, d.ID())
}

// TestReports_ListsEveryReport verifies every report passed to
// NewReports shows up in the list, in the order given (conversation
// order, as the caller collected them).
func TestReports_ListsEveryReport(t *testing.T) {
	t.Parallel()

	d := newTestReports(t, twoTestReports(), "/work")
	require.Len(t, d.list.FilteredItems(), 2)

	item, ok := d.list.FilteredItems()[0].(*ReportItem)
	require.True(t, ok)
	require.Equal(t, "rpt_aaaaaaaa", item.ID())
}

func TestReports_HandleMsg_Close(t *testing.T) {
	t.Parallel()

	d := newTestReports(t, twoTestReports(), "/work")
	require.Equal(t, ActionClose{}, d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape}))
}

// TestReports_HandleMsg_Navigation verifies up/down wrap around the
// ends of the list.
func TestReports_HandleMsg_Navigation(t *testing.T) {
	t.Parallel()

	d := newTestReports(t, twoTestReports(), "/work")
	require.True(t, d.list.IsSelectedFirst())

	d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyUp})
	require.True(t, d.list.IsSelectedLast(), "up from the first item must wrap to the last")

	d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})
	require.True(t, d.list.IsSelectedFirst(), "down from the last item must wrap to the first")
}

// TestReports_HandleMsg_Select verifies enter emits the highlighted
// report together with the working directory the dialog was opened
// with.
func TestReports_HandleMsg_Select(t *testing.T) {
	t.Parallel()

	d := newTestReports(t, twoTestReports(), "/work")
	d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyDown})

	action := d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	resp, ok := action.(ActionSelectExportReport)
	require.True(t, ok)
	require.Equal(t, "rpt_bbbbbbbb", resp.Report.ID)
	require.Equal(t, "websocket vs sse", resp.Report.Task)
	require.Equal(t, "/work", resp.WorkingDir)
}

// TestReports_HandleMsg_TypingFiltersList verifies free text narrows the
// list through the shared fuzzy filter, matching on the task.
func TestReports_HandleMsg_TypingFiltersList(t *testing.T) {
	t.Parallel()

	d := newTestReports(t, twoTestReports(), "/work")
	var lastAction Action
	for _, r := range "websocket" {
		lastAction = d.HandleMsg(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	_, ok := lastAction.(ActionCmd)
	require.True(t, ok)
	require.Len(t, d.list.FilteredItems(), 1)
	item, ok := d.list.SelectedItem().(*ReportItem)
	require.True(t, ok)
	require.Equal(t, "rpt_bbbbbbbb", item.ID())
}

// TestReports_Draw verifies every report's task renders along with the
// agent type that produced it.
func TestReports_Draw(t *testing.T) {
	t.Parallel()

	d := newTestReports(t, twoTestReports(), "/work")
	const w, h = 60, 20
	scr := uv.NewScreenBuffer(w, h)
	d.Draw(scr, image.Rect(0, 0, w, h))

	view := ansi.Strip(scr.Render())
	require.Contains(t, view, "Export Report")
	require.Contains(t, view, "find the parser")
	require.Contains(t, view, "websocket vs sse")
	require.Contains(t, view, "deep-research")
}

func TestReports_ShortAndFullHelp(t *testing.T) {
	t.Parallel()

	d := newTestReports(t, twoTestReports(), "/work")
	require.Len(t, d.ShortHelp(), 3)

	full := d.FullHelp()
	var total int
	for _, row := range full {
		total += len(row)
	}
	require.Equal(t, 4, total)
}

// TestReportItem_FilterAndID verifies the small list.Item /
// list.FilterableItem accessor methods used by the filterable list.
func TestReportItem_FilterAndID(t *testing.T) {
	t.Parallel()

	d := newTestReports(t, twoTestReports(), "/work")
	item, ok := d.list.SelectedItem().(*ReportItem)
	require.True(t, ok)

	require.Contains(t, item.Filter(), "find the parser")
	require.Contains(t, item.Filter(), "explore")
	require.Contains(t, item.Filter(), "rpt_aaaaaaaa")
	require.Equal(t, "rpt_aaaaaaaa", item.ID())
	require.True(t, item.Finished())
}

// TestReportItem_TitleFallsBackToIDWhenTaskIsEmpty verifies the picker
// never renders a blank row for a dispatch with no description.
func TestReportItem_TitleFallsBackToIDWhenTaskIsEmpty(t *testing.T) {
	t.Parallel()

	d := newTestReports(t, []tools.Report{{ID: "rpt_cccccccc", AgentType: "explore"}}, "/work")
	item, ok := d.list.SelectedItem().(*ReportItem)
	require.True(t, ok)
	require.Equal(t, "rpt_cccccccc", reportTitle(item.report))
}
