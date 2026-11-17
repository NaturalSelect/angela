package dialog

import (
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

func pressEnter(d *SetEnv) Action {
	return d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
}

func pressUnset(d *SetEnv) Action {
	return d.HandleMsg(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
}

func drawSetEnv(t *testing.T, d *SetEnv) ([]string, *tea.Cursor) {
	t.Helper()

	const w, h = 80, 30
	scr := uv.NewScreenBuffer(w, h)
	cur := d.Draw(scr, image.Rect(0, 0, w, h))

	rows := strings.Split(scr.Render(), "\n")
	for i, row := range rows {
		rows[i] = strings.TrimRight(ansi.Strip(row), " ")
	}
	return rows, cur
}

func TestSetEnv_ID(t *testing.T) {
	t.Parallel()

	require.Equal(t, SetEnvID, newTestSetEnv(t, &setEnvTestWorkspace{}).ID())
}

func TestSetEnv_SetsAfterNameAndValue(t *testing.T) {
	t.Parallel()

	ws := &setEnvTestWorkspace{}
	d := newTestSetEnv(t, ws)

	typeInto(d, "  API_TOKEN ")
	require.Nil(t, pressEnter(d))
	require.Empty(t, ws.vars, "enter on the name must only move on to the value")
	require.Equal(t, setEnvFieldValue, d.focused)

	typeInto(d, "s3 cret=1")
	require.Nil(t, pressEnter(d))

	require.Equal(t, map[string]string{"API_TOKEN": "s3 cret=1"}, ws.vars)
	require.Empty(t, d.inputs[setEnvFieldName].Value())
	require.Empty(t, d.inputs[setEnvFieldValue].Value(), "the secret must not stay in the form")
	require.Equal(t, setEnvFieldName, d.focused)
	require.Equal(t, "Set API_TOKEN.", d.status)
	require.False(t, d.statusErr)
}

func TestSetEnv_RejectsInvalidName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "   ", "1ST", "A-B", "A B"} {
		ws := &setEnvTestWorkspace{}
		d := newTestSetEnv(t, ws)

		typeInto(d, name)
		pressEnter(d)

		require.Empty(t, ws.vars, name)
		require.Equal(t, setEnvFieldName, d.focused, name)
		require.True(t, d.statusErr, name)
	}
}

func TestSetEnv_RejectsEmptyValue(t *testing.T) {
	t.Parallel()

	ws := &setEnvTestWorkspace{}
	d := newTestSetEnv(t, ws)

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

	typeInto(d, "TOKEN")
	pressEnter(d)
	typeInto(d, "x")
	pressEnter(d)

	require.True(t, d.statusErr)
	require.Equal(t, "x", d.inputs[setEnvFieldValue].Value(), "a failed set must keep what was typed")
}

func TestSetEnv_Unset(t *testing.T) {
	t.Parallel()

	ws := &setEnvTestWorkspace{vars: map[string]string{"TOKEN": "x", "OTHER": "y"}}
	d := newTestSetEnv(t, ws)

	typeInto(d, "TOKEN")
	pressUnset(d)

	require.Equal(t, map[string]string{"OTHER": "y"}, ws.vars)
	require.Empty(t, d.inputs[setEnvFieldName].Value())
	require.Equal(t, "Unset TOKEN.", d.status)
	require.False(t, d.statusErr)
}

func TestSetEnv_UnsetNeedsAVariableSetHere(t *testing.T) {
	t.Parallel()

	ws := &setEnvTestWorkspace{vars: map[string]string{"TOKEN": "x"}}
	d := newTestSetEnv(t, ws)

	pressUnset(d)
	require.True(t, d.statusErr, "an empty name has nothing to unset")

	typeInto(d, "PATH")
	pressUnset(d)
	require.True(t, d.statusErr)
	require.Equal(t, "PATH was not set here", d.status)
	require.Equal(t, map[string]string{"TOKEN": "x"}, ws.vars)
}

func TestSetEnv_NavigationAndClose(t *testing.T) {
	t.Parallel()

	d := newTestSetEnv(t, &setEnvTestWorkspace{})

	d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, setEnvFieldValue, d.focused)
	d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, setEnvFieldName, d.focused)
	d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	require.Equal(t, setEnvFieldValue, d.focused)

	require.IsType(t, ActionClose{}, d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape}))
}

func TestSetEnv_NeverDrawsTheValue(t *testing.T) {
	t.Parallel()

	ws := &setEnvTestWorkspace{}
	d := newTestSetEnv(t, ws)

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
	require.Contains(t, screen, "Set this session:")
	require.Contains(t, screen, "TOKEN")
}

func TestSetEnv_ListsNamesWithoutOverflowing(t *testing.T) {
	t.Parallel()

	ws := &setEnvTestWorkspace{vars: map[string]string{}}
	for _, name := range []string{"A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L"} {
		ws.vars[name] = "v"
	}
	d := newTestSetEnv(t, ws)

	rows, _ := drawSetEnv(t, d)
	screen := strings.Join(rows, "\n")

	require.Contains(t, screen, "A, B, C, D, E, F, G, H, I, J and 2 more")
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
