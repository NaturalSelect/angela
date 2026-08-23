package model

import (
	"strings"
	"testing"

	"github.com/NaturalSelect/angela/internal/session"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// drawableUI builds a UI that can render a full frame. newBusyUI leaves the
// status bar without a keymap because its tests never draw one.
func drawableUI(t *testing.T, w, h int) *UI {
	t.Helper()
	m := newBusyUI(detailsWorkspace())
	m.status = NewStatus(m.com, m)
	m.width, m.height = w, h
	m.session = &session.Session{ID: "s1", Title: "a fairly long session title here"}
	return m
}

// drawFrame renders a full frame in the given state and returns its lines.
func drawFrame(m *UI, state uiState) []string {
	m.state = state
	m.updateLayoutAndSize()
	scr := uv.NewScreenBuffer(m.width, m.height)
	m.Draw(scr, scr.Bounds())
	return strings.Split(scr.Render(), "\n")
}

// The header band is one row in every state: it is an instrument bar, not a
// brand banner. The letterform wall belongs to the landing body, where it can
// be composed with the menu and dropped on a short terminal.
func TestHeaderFitsItsBand(t *testing.T) {
	pinTTLs(t)

	for _, tc := range []struct {
		name  string
		state uiState
	}{
		{"chat", uiChat},
		{"landing", uiLanding},
		{"initialize", uiInitialize},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := drawableUI(t, 140, 40)
			m.state = tc.state
			m.updateLayoutAndSize()

			band := m.layout.header
			require.Equal(t, headerHeight, band.Dy())

			scr := uv.NewScreenBuffer(m.width, m.height)
			m.drawHeader(scr, band)
			lines := strings.Split(scr.Render(), "\n")

			for i, line := range lines {
				inBand := i >= band.Min.Y && i < band.Max.Y
				content := strings.TrimSpace(ansi.Strip(line))
				if !inBand {
					require.Empty(t, content,
						"header wrote outside its band on row %d", i)
				}
			}
			require.NotEmpty(t,
				strings.TrimSpace(ansi.Strip(lines[band.Min.Y])),
				"the header band must not be blank")

			require.Contains(t, ansi.Strip(lines[band.Min.Y]), "A N G E L A",
				"the header bar must carry the inline wordmark")
		})
	}
}

// The landing page is an entry screen, not a status report: the diagnostics it
// used to print now live behind ctrl+d. What it does owe the user is a way in.
func TestLandingOffersEntryPoints(t *testing.T) {
	pinTTLs(t)

	m := drawableUI(t, 140, 40)
	body := ansi.Strip(strings.Join(drawFrame(m, uiLanding), "\n"))

	for _, entry := range []string{"Resume session", "Commands", "Switch model", "Quit"} {
		require.Contains(t, body, entry,
			"the landing page must offer %q as a way in", entry)
	}
	for _, gone := range []string{"LSP", "MCP", "Skills"} {
		require.NotContains(t, body, gone,
			"%q belongs to the details panel, not the landing page", gone)
	}
}

// Whatever the terminal size, no frame may render a line wider than the
// terminal: one overflowing line corrupts every row below it.
func TestFramesNeverExceedTerminalWidth(t *testing.T) {
	pinTTLs(t)

	for _, size := range []struct{ w, h int }{
		{80, 24}, {120, 30}, {200, 50}, {60, 20},
	} {
		for _, state := range []uiState{uiLanding, uiChat, uiInitialize} {
			m := drawableUI(t, size.w, size.h)

			for i, line := range drawFrame(m, state) {
				require.LessOrEqual(t, ansi.StringWidth(line), size.w,
					"state %d at %dx%d: line %d overflows: %q",
					state, size.w, size.h, i, line)
			}
		}
	}
}
