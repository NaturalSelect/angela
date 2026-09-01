package model

import (
	"image"
	"strings"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// TestEditorPromptCarriesMode pins that the three input modes are told apart
// by color alone — the glyphs must stay identical so the gutter width never
// shifts under the textarea.
func TestEditorPromptCarriesMode(t *testing.T) {
	pinTTLs(t)

	render := func(bang bool, mode permission.PermissionMode) string {
		m, ws := newMockBusyUI(t)
		ws.EXPECT().AgentIsReady().Return(true).AnyTimes()
		m.textarea.Focus()
		m.textarea.SetWidth(40)
		m.bangMode = bang
		m.permissionModeCache.set(mode)
		m.setEditorPrompt(mode)
		return m.textarea.View()
	}

	normal := render(false, permission.ModeManual)
	bang := render(true, permission.ModeManual)
	autoAccept := render(false, permission.ModeAutoAcceptEdits)
	yolo := render(false, permission.ModeYolo)

	require.NotEqual(t, normal, bang, "bang mode must recolor the prompt")
	require.NotEqual(t, normal, autoAccept, "auto-accept-edits mode must recolor the prompt")
	require.NotEqual(t, normal, yolo, "yolo mode must recolor the prompt")
	require.NotEqual(t, autoAccept, yolo, "auto-accept-edits and yolo must use different colors")

	// Colors differ, glyphs do not: the gutter keeps its width in every mode.
	require.Equal(t, ansi.Strip(normal), ansi.Strip(bang))
	require.Equal(t, ansi.Strip(normal), ansi.Strip(autoAccept))
	require.Equal(t, ansi.Strip(normal), ansi.Strip(yolo))

	lines := strings.Split(ansi.Strip(normal), "\n")
	require.True(t, strings.HasPrefix(lines[0], editorPromptGlyph),
		"the marker opens the first line, got %q", lines[0])
	for _, line := range lines[1:] {
		require.False(t, strings.HasPrefix(line, editorPromptGlyph),
			"the marker belongs on the first line only, got %q", line)
	}
}

// TestEditorCaptionDegradesWithWidth pins that the caption never overflows and
// drops the agent name before the model name when space runs out.
func TestEditorCaptionDegradesWithWidth(t *testing.T) {
	pinTTLs(t)

	ws, _ := detailsMockWorkspace(t)
	m := newBusyUIWithWorkspace(ws)
	m.session = &session.Session{ID: "s1", Title: "a session"}
	m.agentReady = true
	m.agentActiveKnown = true
	m.agentActiveSession = m.currentSessionID()
	m.agentActive = workspace.ActiveAgent{
		AgentID:    "coder",
		AgentName:  "coder",
		CatwalkCfg: catwalk.Model{Name: "claude-sonnet-4-5"},
	}
	require.NotNil(t, m.activeAgent(), "the caption needs a resolved agent")

	for _, width := range []int{200, 100, 80, 60, 30, 12, 1} {
		m.width = width
		out := m.editorCaption(width)
		require.LessOrEqual(t, ansi.StringWidth(out), width,
			"caption must fit width %d, got %q", width, out)
	}

	m.width = 200
	wide := m.editorCaption(200)
	m.width = 60
	narrow := m.editorCaption(60)
	require.Contains(t, wide, "coder", "wide captions name the agent")
	require.NotContains(t, narrow, "coder",
		"narrow captions drop the agent name and keep the model")
	require.Contains(t, narrow, "claude-sonnet-4-5")
}

// TestEditorPlaceholderHasNoPersonality pins the fixed placeholder: it must
// tell the user what to do rather than emit a random mood word.
func TestEditorPlaceholderHasNoPersonality(t *testing.T) {
	pinTTLs(t)

	m, ws := newMockBusyUI(t)
	ws.EXPECT().AgentIsReady().Return(true).AnyTimes()
	m.width = 120

	require.Equal(t, "Ask anything — / for commands, @ for agents, # for files", m.editorPlaceholder())

	m.width = 40
	require.Equal(t, "Ask anything…", m.editorPlaceholder())

	m.width = 120
	m.bangMode = true
	require.Equal(t, "Run a shell command", m.editorPlaceholder())

	m.bangMode = false
	m.permissionModeCache.set(permission.ModeYolo)
	require.Contains(t, m.editorPlaceholder(), "permissions are skipped")

	m.permissionModeCache.set(permission.ModeAutoAcceptEdits)
	require.Contains(t, m.editorPlaceholder(), "Auto-accepting edits")
}

// TestEditorHeightIsStable pins the editor's vertical footprint: the textarea
// plus its box chrome, never varying with the input mode.
func TestEditorHeightIsStable(t *testing.T) {
	pinTTLs(t)

	m, ws := newMockBusyUI(t)
	ws.EXPECT().AgentIsReady().Return(true).AnyTimes()
	m.width = 120
	m.textarea.SetWidth(100)

	for _, tc := range []struct {
		name string
		bang bool
		mode permission.PermissionMode
	}{
		{"normal", false, permission.ModeManual},
		{"bang", true, permission.ModeManual},
		{"auto-accept-edits", false, permission.ModeAutoAcceptEdits},
		{"yolo", false, permission.ModeYolo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m.bangMode = tc.bang
			m.permissionModeCache.set(tc.mode)
			m.setEditorPrompt(tc.mode)

			require.Equal(t, m.textarea.Height()+editorHeightMargin, m.editorHeight(),
				"editor height must not depend on the input mode")
		})
	}
}

// TestPromptBoxDrawsBorderAndLabel pins the input as a box rather than a run of
// printed characters: a closed frame, with the run context inset into the
// bottom border instead of printed on a row of its own.
func TestPromptBoxDrawsBorderAndLabel(t *testing.T) {
	pinTTLs(t)

	ws, _ := detailsMockWorkspace(t)
	m := newBusyUIWithWorkspace(ws)
	m.session = &session.Session{ID: "s1", Title: "a session"}
	m.agentReady = true
	m.agentActiveKnown = true
	m.agentActiveSession = m.currentSessionID()
	m.agentActive = workspace.ActiveAgent{
		AgentID:    "coder",
		AgentName:  "coder",
		CatwalkCfg: catwalk.Model{Name: "claude-sonnet-4-5"},
	}
	m.width = 60
	m.textarea.SetWidth(60 - editorBoxBorders)

	const w, h = 60, 6
	buf := uv.NewScreenBuffer(w, h)
	m.drawPromptBox(&buf, image.Rect(0, 0, w, h))

	rows := strings.Split(buf.Render(), "\n")
	require.Len(t, rows, h)
	for i, r := range rows {
		rows[i] = strings.TrimRight(ansi.Strip(r), " ")
	}

	// Row 0 is the attachments strip; the box opens beneath it.
	require.True(t, strings.HasPrefix(rows[1], "╭"), "box opens with a corner, got %q", rows[1])
	require.True(t, strings.HasSuffix(rows[1], "╮"), "box top closes, got %q", rows[1])

	for i, row := range rows[2 : h-1] {
		require.True(t, strings.HasPrefix(row, "│"),
			"box row %d carries the left border, got %q", i, row)
	}

	bottom := rows[h-1]
	require.True(t, strings.HasPrefix(bottom, "╰"), "box bottom opens, got %q", bottom)
	require.True(t, strings.HasSuffix(bottom, "╯"), "box bottom closes, got %q", bottom)
	require.Contains(t, bottom, "claude-sonnet-4-5",
		"the run context is inset into the bottom border, not printed on its own row")
}

// TestPromptBoxPaintsNoBackground pins the box as chrome drawn onto whatever
// is already underneath it. None of its styles set a background, so none of
// its cells may end up carrying one: a painted background turns the frame into
// a visibly different patch on any terminal whose own background is not an
// exact match — which is every terminal with transparency.
func TestPromptBoxPaintsNoBackground(t *testing.T) {
	pinTTLs(t)

	ws, _ := detailsMockWorkspace(t)
	m := newBusyUIWithWorkspace(ws)
	m.session = &session.Session{ID: "s1", Title: "a session"}
	m.agentReady = true
	m.agentActiveKnown = true
	m.agentActiveSession = m.currentSessionID()
	m.agentActive = workspace.ActiveAgent{
		AgentID:    "coder",
		AgentName:  "coder",
		CatwalkCfg: catwalk.Model{Name: "claude-sonnet-4-5"},
	}
	m.width = 60
	m.textarea.SetWidth(60 - editorBoxBorders)

	const w, h = 60, 6
	buf := uv.NewScreenBuffer(w, h)
	m.drawPromptBox(&buf, image.Rect(0, 0, w, h))

	for y := range h {
		for x := range w {
			cell := buf.CellAt(x, y)
			if cell == nil {
				continue
			}
			require.Nil(t, cell.Style.Bg,
				"cell (%d,%d) %q paints a background of its own", x, y, cell.Content)
		}
	}
}
