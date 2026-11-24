package dialog

import (
	"fmt"
	"image"
	"maps"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/env"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/NaturalSelect/angela/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// setEnvTestWorkspace keeps the variables in a map, so the dialog tests
// never touch the real process environment.
type setEnvTestWorkspace struct {
	workspace.Workspace

	vars   map[string]string
	setErr error
}

func (w *setEnvTestWorkspace) SetEnv(name, value string) error {
	if w.setErr != nil {
		return w.setErr
	}
	if w.vars == nil {
		w.vars = map[string]string{}
	}
	w.vars[name] = value
	return nil
}

func (w *setEnvTestWorkspace) UnsetEnv(name string) error {
	if _, ok := w.vars[name]; !ok {
		return env.ErrNotOverridden
	}
	delete(w.vars, name)
	return nil
}

func (w *setEnvTestWorkspace) SessionEnvNames() []string {
	return slices.Sorted(maps.Keys(w.vars))
}

func newTestSetEnv(t *testing.T, ws *setEnvTestWorkspace) *SetEnv {
	t.Helper()
	s := styles.CharmtonePantera()
	return NewSetEnv(&common.Common{Styles: &s, Workspace: ws})
}

func typeInto(d *SetEnv, text string) {
	for _, r := range text {
		d.HandleMsg(keyMsg(r))
	}
}

func pressKey(d *SetEnv, code rune, mod tea.KeyMod) Action {
	return d.HandleMsg(tea.KeyPressMsg{Code: code, Mod: mod})
}

func pressEnter(d *SetEnv) Action { return pressKey(d, tea.KeyEnter, 0) }
func pressUp(d *SetEnv) Action    { return pressKey(d, tea.KeyUp, 0) }
func pressDown(d *SetEnv) Action  { return pressKey(d, tea.KeyDown, 0) }
func pressEsc(d *SetEnv) Action   { return pressKey(d, tea.KeyEscape, 0) }
func pressUnset(d *SetEnv) Action { return pressKey(d, 'r', tea.ModCtrl) }

// rowNames lists the list rows top to bottom, with "" for the row that adds
// a variable.
func rowNames(d *SetEnv) []string {
	var names []string
	for _, it := range d.list.FilteredItems() {
		names = append(names, it.(*EnvVarItem).Name())
	}
	return names
}

func selectedName(t *testing.T, d *SetEnv) string {
	t.Helper()
	item := d.selectedItem()
	require.NotNil(t, item)
	return item.Name()
}

func drawSetEnvSized(t *testing.T, d *SetEnv, w, h int) ([]string, *tea.Cursor) {
	t.Helper()

	scr := uv.NewScreenBuffer(w, h)
	cur := d.Draw(scr, image.Rect(0, 0, w, h))

	rows := strings.Split(scr.Render(), "\n")
	for i, row := range rows {
		rows[i] = strings.TrimRight(ansi.Strip(row), " ")
	}
	return rows, cur
}

func drawSetEnv(t *testing.T, d *SetEnv) ([]string, *tea.Cursor) {
	t.Helper()
	return drawSetEnvSized(t, d, 80, 30)
}

func TestSetEnv_ID(t *testing.T) {
	t.Parallel()

	require.Equal(t, SetEnvID, newTestSetEnv(t, &setEnvTestWorkspace{}).ID())
}

func TestSetEnv_OpensOnTheListWithAddRowSelected(t *testing.T) {
	t.Parallel()

	ws := &setEnvTestWorkspace{vars: map[string]string{"ZED": "1", "ALPHA": "2"}}
	d := newTestSetEnv(t, ws)

	require.Equal(t, setEnvModeList, d.mode)
	require.Equal(t, []string{"", "ALPHA", "ZED"}, rowNames(d))
	require.Empty(t, selectedName(t, d), "the add row is the one to land on")

	rows, cur := drawSetEnv(t, d)
	screen := strings.Join(rows, "\n")
	require.Contains(t, screen, "Environment Variables")
	require.Contains(t, screen, "+ New variable")
	require.Contains(t, screen, "ALPHA")
	require.Contains(t, screen, "ZED")
	require.Nil(t, cur, "the list has no input to put a cursor in")
}

func TestSetEnv_EmptySessionListsOnlyTheAddRow(t *testing.T) {
	t.Parallel()

	d := newTestSetEnv(t, &setEnvTestWorkspace{})

	require.Equal(t, []string{""}, rowNames(d))
	pressUnset(d)
	require.True(t, d.statusErr, "the add row has nothing to unset")
}

func TestSetEnv_AddsANewVariable(t *testing.T) {
	t.Parallel()

	ws := &setEnvTestWorkspace{}
	d := newTestSetEnv(t, ws)

	require.Nil(t, pressEnter(d))
	require.Equal(t, setEnvModeForm, d.mode)
	require.Equal(t, setEnvFieldName, d.focused)

	typeInto(d, "  API_TOKEN ")
	require.Nil(t, pressEnter(d))
	require.Empty(t, ws.vars, "enter on the name must only move on to the value")
	require.Equal(t, setEnvFieldValue, d.focused)

	typeInto(d, "s3 cret=1")
	require.Nil(t, pressEnter(d))

	require.Equal(t, map[string]string{"API_TOKEN": "s3 cret=1"}, ws.vars)
	require.Equal(t, setEnvModeList, d.mode, "a successful set returns to the list")
	require.Equal(t, []string{"", "API_TOKEN"}, rowNames(d))
	require.Equal(t, "API_TOKEN", selectedName(t, d))
	require.Empty(t, d.inputs[setEnvFieldName].Value())
	require.Empty(t, d.inputs[setEnvFieldValue].Value(), "the secret must not stay in the form")
	require.Equal(t, "Set API_TOKEN.", d.status)
	require.False(t, d.statusErr)
}

func TestSetEnv_ReplacesTheValueOfAListedVariable(t *testing.T) {
	t.Parallel()

	ws := &setEnvTestWorkspace{vars: map[string]string{"ALPHA": "old", "ZED": "z"}}
	d := newTestSetEnv(t, ws)

	pressDown(d)
	require.Equal(t, "ALPHA", selectedName(t, d))
	pressEnter(d)

	require.Equal(t, setEnvModeForm, d.mode)
	require.Equal(t, "ALPHA", d.inputs[setEnvFieldName].Value())
	require.Equal(t, setEnvFieldValue, d.focused, "the name is known, so start on the value")
	require.Empty(t, d.inputs[setEnvFieldValue].Value(), "the old value is never shown")

	typeInto(d, "new")
	pressEnter(d)

	require.Equal(t, map[string]string{"ALPHA": "new", "ZED": "z"}, ws.vars)
	require.Equal(t, setEnvModeList, d.mode)
	require.Equal(t, "ALPHA", selectedName(t, d))
}

func TestSetEnv_RejectsInvalidName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "   ", "1ST", "A-B", "A B"} {
		ws := &setEnvTestWorkspace{}
		d := newTestSetEnv(t, ws)

		pressEnter(d)
		typeInto(d, name)
		pressEnter(d)

		require.Empty(t, ws.vars, name)
		require.Equal(t, setEnvModeForm, d.mode, name)
		require.Equal(t, setEnvFieldName, d.focused, name)
		require.True(t, d.statusErr, name)
	}
}

func TestSetEnv_RejectsEmptyValue(t *testing.T) {
	t.Parallel()

	ws := &setEnvTestWorkspace{}
	d := newTestSetEnv(t, ws)

	pressEnter(d)
	typeInto(d, "TOKEN")
	pressEnter(d)
	pressEnter(d)

	require.Empty(t, ws.vars)
	require.True(t, d.statusErr)
	require.Equal(t, setEnvFieldValue, d.focused)
	require.Equal(t, "TOKEN", d.inputs[setEnvFieldName].Value())
}

func TestSetEnv_ReportsWorkspaceError(t *testing.T) {
	t.Parallel()

	ws := &setEnvTestWorkspace{setErr: env.ErrNotOverridden}
	d := newTestSetEnv(t, ws)

	pressEnter(d)
	typeInto(d, "TOKEN")
	pressEnter(d)
	typeInto(d, "x")
	pressEnter(d)

	require.Equal(t, setEnvModeForm, d.mode, "a failed set stays on the form")
	require.True(t, d.statusErr)
	require.Equal(t, "x", d.inputs[setEnvFieldValue].Value(), "a failed set must keep what was typed")
}

func TestSetEnv_UnsetsTheSelectedVariable(t *testing.T) {
	t.Parallel()

	for name, press := range map[string]func(*SetEnv) Action{
		"ctrl+r": pressUnset,
		"delete": func(d *SetEnv) Action { return pressKey(d, tea.KeyDelete, 0) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ws := &setEnvTestWorkspace{vars: map[string]string{"A": "1", "B": "2", "C": "3"}}
			d := newTestSetEnv(t, ws)

			pressDown(d)
			pressDown(d)
			require.Equal(t, "B", selectedName(t, d))
			press(d)

			require.Equal(t, map[string]string{"A": "1", "C": "3"}, ws.vars)
			require.Equal(t, []string{"", "A", "C"}, rowNames(d))
			require.Equal(t, "C", selectedName(t, d), "selection stays at the same position")
			require.Equal(t, "Unset B.", d.status)
			require.False(t, d.statusErr)
		})
	}
}

func TestSetEnv_UnsettingTheLastRowSelectsTheNewLastRow(t *testing.T) {
	t.Parallel()

	ws := &setEnvTestWorkspace{vars: map[string]string{"A": "1", "B": "2"}}
	d := newTestSetEnv(t, ws)

	pressUp(d)
	require.Equal(t, "B", selectedName(t, d))
	pressUnset(d)

	require.Equal(t, "A", selectedName(t, d))
}

func TestSetEnv_UnsetNeedsAVariableRow(t *testing.T) {
	t.Parallel()

	ws := &setEnvTestWorkspace{vars: map[string]string{"TOKEN": "x"}}
	d := newTestSetEnv(t, ws)

	pressUnset(d)
	require.True(t, d.statusErr, "the add row has nothing to unset")
	require.Equal(t, map[string]string{"TOKEN": "x"}, ws.vars)
}

func TestSetEnv_UnsetReportsAVariableNoLongerSet(t *testing.T) {
	t.Parallel()

	ws := &setEnvTestWorkspace{vars: map[string]string{"TOKEN": "x"}}
	d := newTestSetEnv(t, ws)

	pressDown(d)
	delete(ws.vars, "TOKEN")
	pressUnset(d)

	require.True(t, d.statusErr)
	require.Equal(t, "TOKEN was not set here", d.status)
}

func TestSetEnv_ListNavigationWraps(t *testing.T) {
	t.Parallel()

	ws := &setEnvTestWorkspace{vars: map[string]string{"A": "1", "B": "2"}}
	d := newTestSetEnv(t, ws)

	pressUp(d)
	require.Equal(t, "B", selectedName(t, d), "up from the first row wraps to the last")
	pressDown(d)
	require.Empty(t, selectedName(t, d), "down from the last row wraps to the first")
	pressKey(d, tea.KeyTab, 0)
	require.Equal(t, "A", selectedName(t, d))
}

func TestSetEnv_NavigationClearsTheLastResult(t *testing.T) {
	t.Parallel()

	ws := &setEnvTestWorkspace{vars: map[string]string{"A": "1"}}
	d := newTestSetEnv(t, ws)

	pressDown(d)
	pressUnset(d)
	require.NotEmpty(t, d.status)

	pressDown(d)
	require.Empty(t, d.status)
}

func TestSetEnv_FormNavigation(t *testing.T) {
	t.Parallel()

	d := newTestSetEnv(t, &setEnvTestWorkspace{})
	pressEnter(d)

	pressKey(d, tea.KeyTab, 0)
	require.Equal(t, setEnvFieldValue, d.focused)
	pressKey(d, tea.KeyTab, 0)
	require.Equal(t, setEnvFieldName, d.focused)
	pressKey(d, tea.KeyTab, tea.ModShift)
	require.Equal(t, setEnvFieldValue, d.focused)
}

func TestSetEnv_EscapeClosesTheListButOnlyLeavesTheForm(t *testing.T) {
	t.Parallel()

	ws := &setEnvTestWorkspace{vars: map[string]string{"TOKEN": "x"}}
	d := newTestSetEnv(t, ws)

	pressDown(d)
	pressEnter(d)
	typeInto(d, "half typed")

	require.Nil(t, pressEsc(d), "esc on the form goes back, it does not close")
	require.Equal(t, setEnvModeList, d.mode)
	require.Equal(t, "TOKEN", selectedName(t, d))
	require.Empty(t, d.inputs[setEnvFieldValue].Value(), "what was typed must not linger")
	require.Equal(t, map[string]string{"TOKEN": "x"}, ws.vars)

	require.IsType(t, ActionClose{}, pressEsc(d))
}

func TestSetEnv_NeverDrawsTheValue(t *testing.T) {
	t.Parallel()

	ws := &setEnvTestWorkspace{}
	d := newTestSetEnv(t, ws)

	pressEnter(d)
	typeInto(d, "TOKEN")
	pressEnter(d)
	typeInto(d, "hunter2")

	rows, _ := drawSetEnv(t, d)
	screen := strings.Join(rows, "\n")
	require.NotContains(t, screen, "hunter2", "the value must be masked while typing")
	require.Contains(t, screen, "*******")

	pressEnter(d)
	rows, _ = drawSetEnv(t, d)
	screen = strings.Join(rows, "\n")
	require.NotContains(t, screen, "hunter2")
	require.NotContains(t, screen, "*******")
	require.Contains(t, screen, "TOKEN")
	require.Contains(t, screen, "Set TOKEN.")
}

func TestSetEnv_LongListScrollsAndKeepsTheHintOnScreen(t *testing.T) {
	t.Parallel()

	ws := &setEnvTestWorkspace{vars: map[string]string{}}
	for i := 1; i <= 12; i++ {
		ws.vars[fmt.Sprintf("VAR_%02d", i)] = "v"
	}
	d := newTestSetEnv(t, ws)

	rows, _ := drawSetEnv(t, d)
	screen := strings.Join(rows, "\n")
	require.Contains(t, screen, "+ New variable")
	require.Contains(t, screen, "VAR_01")
	require.NotContains(t, screen, "VAR_12", "only a capped number of rows is shown at once")
	require.Contains(t, screen, "Never saved.")

	pressUp(d)
	require.Equal(t, "VAR_12", selectedName(t, d))
	rows, _ = drawSetEnv(t, d)
	screen = strings.Join(rows, "\n")
	require.Contains(t, screen, "VAR_12", "the selected row scrolls into view")
	require.Contains(t, screen, "Never saved.")
}

func TestSetEnv_ListFitsAShortTerminal(t *testing.T) {
	t.Parallel()

	ws := &setEnvTestWorkspace{vars: map[string]string{}}
	for i := 1; i <= 12; i++ {
		ws.vars[fmt.Sprintf("VAR_%02d", i)] = "v"
	}
	d := newTestSetEnv(t, ws)

	rows, _ := drawSetEnvSized(t, d, 80, 14)
	screen := strings.Join(rows, "\n")
	require.Contains(t, screen, "Environment Variables")
	require.Contains(t, screen, "Never saved.")
	require.Contains(t, screen, "esc", "the help footer must not be pushed off screen")
}

func TestSetEnv_HelpFitsForEveryRow(t *testing.T) {
	t.Parallel()

	ws := &setEnvTestWorkspace{vars: map[string]string{"TOKEN": "x"}}
	d := newTestSetEnv(t, ws)

	for _, row := range []string{"", "TOKEN"} {
		rows, _ := drawSetEnv(t, d)
		screen := strings.Join(rows, "\n")
		require.Contains(t, screen, "esc exit", "the help footer is cut off on row %q", row)
		pressDown(d)
	}
}

// The terminal cursor has to land inside the focused field's input.
func TestSetEnv_CursorLandsInTheFocusedInput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		field       int
		placeholder string
	}{
		{"name", setEnvFieldName, "GITHUB_TOKEN"},
		{"value", setEnvFieldValue, "Hidden as you type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d := newTestSetEnv(t, &setEnvTestWorkspace{})
			pressEnter(d)
			d.focusInput(tc.field)

			rows, cur := drawSetEnv(t, d)
			require.NotNil(t, cur)
			require.Less(t, cur.Y, len(rows))

			row := rows[cur.Y]
			require.NotEqual(t, setEnvFieldLabels[tc.field], strings.TrimSpace(row),
				"the cursor sits on the field label instead of its input")
			idx := strings.Index(row, tc.placeholder)
			require.NotEqual(t, -1, idx, "the cursor row is not the focused field's input: %q", row)
			require.Equal(t, ansi.StringWidth(row[:idx]), cur.X,
				"the cursor is not at the first column of the input text")
		})
	}
}
