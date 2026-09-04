package common

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/ui/notification"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestCapabilities_Update(t *testing.T) {
	t.Parallel()

	t.Run("EnvMsg sets Env", func(t *testing.T) {
		t.Parallel()
		var c Capabilities
		c.Update(tea.EnvMsg{"TERM=xterm"})
		v, ok := c.Env.LookupEnv("TERM")
		require.True(t, ok)
		require.Equal(t, "xterm", v)
	})

	t.Run("ColorProfileMsg sets the color profile", func(t *testing.T) {
		t.Parallel()
		var c Capabilities
		require.False(t, c.SupportsTrueColor())
		c.Update(tea.ColorProfileMsg{Profile: colorprofile.TrueColor})
		require.True(t, c.SupportsTrueColor())
	})

	t.Run("WindowSizeMsg sets columns and rows", func(t *testing.T) {
		t.Parallel()
		var c Capabilities
		c.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		require.Equal(t, 80, c.Columns)
		require.Equal(t, 24, c.Rows)
	})

	t.Run("PixelSizeEvent sets the pixel dimensions", func(t *testing.T) {
		t.Parallel()
		var c Capabilities
		c.Update(uv.PixelSizeEvent{Width: 800, Height: 600})
		require.Equal(t, 800, c.PixelX)
		require.Equal(t, 600, c.PixelY)
	})

	t.Run("KittyGraphicsEvent enables Kitty graphics support", func(t *testing.T) {
		t.Parallel()
		var c Capabilities
		require.False(t, c.SupportsKittyGraphics())
		c.Update(uv.KittyGraphicsEvent{})
		require.True(t, c.SupportsKittyGraphics())
	})

	t.Run("PrimaryDeviceAttributesEvent with sixel support", func(t *testing.T) {
		t.Parallel()
		var c Capabilities
		c.Update(uv.PrimaryDeviceAttributesEvent{1, 4, 6})
		require.True(t, c.SupportsSixelGraphics())
	})

	t.Run("PrimaryDeviceAttributesEvent without sixel support", func(t *testing.T) {
		t.Parallel()
		var c Capabilities
		c.Update(uv.PrimaryDeviceAttributesEvent{1, 6})
		require.False(t, c.SupportsSixelGraphics())
	})

	t.Run("TerminalVersionMsg sets the terminal version", func(t *testing.T) {
		t.Parallel()
		var c Capabilities
		c.Update(tea.TerminalVersionMsg{Name: "iTerm2 3.5"})
		require.Equal(t, "iTerm2 3.5", c.TerminalVersion)
	})

	t.Run("ModeReportMsg for focus event sets ReportFocusEvents", func(t *testing.T) {
		t.Parallel()
		var c Capabilities
		c.Update(tea.ModeReportMsg{Mode: ansi.ModeFocusEvent, Value: ansi.ModeSet})
		require.True(t, c.ReportFocusEvents)
	})

	t.Run("ModeReportMsg not recognized leaves it unset", func(t *testing.T) {
		t.Parallel()
		var c Capabilities
		c.Update(tea.ModeReportMsg{Mode: ansi.ModeFocusEvent, Value: ansi.ModeNotRecognized})
		require.False(t, c.ReportFocusEvents)
	})

	t.Run("ModeReportMsg for an unrelated mode is ignored", func(t *testing.T) {
		t.Parallel()
		var c Capabilities
		c.Update(tea.ModeReportMsg{Mode: ansi.DECMode(2004), Value: ansi.ModeSet})
		require.False(t, c.ReportFocusEvents)
	})

	t.Run("UnknownOscEvent with a valid OSC99 response marks it detected", func(t *testing.T) {
		t.Parallel()
		var c Capabilities
		c.Update(uv.UnknownOscEvent("\x1b]99;i=angela-osc99-query:p=?;p=title\x07"))
		require.True(t, c.OSC99Notifications)
	})

	t.Run("UnknownOscEvent unrelated to OSC99 is ignored", func(t *testing.T) {
		t.Parallel()
		var c Capabilities
		c.Update(uv.UnknownOscEvent("\x1b]52;c;aGVsbG8=\x07"))
		require.False(t, c.OSC99Notifications)
	})

	t.Run("an unrecognized message type is ignored", func(t *testing.T) {
		t.Parallel()
		var c Capabilities
		require.NotPanics(t, func() { c.Update("not a known capability message") })
	})
}

func TestCapabilities_CellSize(t *testing.T) {
	t.Parallel()

	t.Run("zero columns or rows returns zero", func(t *testing.T) {
		t.Parallel()
		c := Capabilities{PixelX: 800, PixelY: 600}
		w, h := c.CellSize()
		require.Equal(t, 0, w)
		require.Equal(t, 0, h)
	})

	t.Run("computes the per-cell pixel size", func(t *testing.T) {
		t.Parallel()
		c := Capabilities{Columns: 80, Rows: 24, PixelX: 800, PixelY: 480}
		w, h := c.CellSize()
		require.Equal(t, 10, w)
		require.Equal(t, 20, h)
	})
}

func TestQueryCmd(t *testing.T) {
	t.Parallel()

	extractRaw := func(t *testing.T, cmd tea.Cmd) string {
		t.Helper()
		require.NotNil(t, cmd)
		msg := cmd()
		raw, ok := msg.(tea.RawMsg)
		require.True(t, ok)
		s, ok := raw.Msg.(string)
		require.True(t, ok)
		return s
	}

	t.Run("always includes the baseline queries", func(t *testing.T) {
		t.Parallel()
		s := extractRaw(t, QueryCmd(uv.Environ{}))
		require.Contains(t, s, ansi.RequestPrimaryDeviceAttributes)
		require.Contains(t, s, ansi.QueryModifyOtherKeys)
		require.Contains(t, s, ansi.RequestModeFocusEvent)
		require.Contains(t, s, notification.OSC99QuerySequence())
	})

	t.Run("a smart terminal gets the extra queries", func(t *testing.T) {
		t.Parallel()
		s := extractRaw(t, QueryCmd(uv.Environ{"TERM=xterm-kitty"}))
		require.Contains(t, s, ansi.RequestNameVersion)
	})

	t.Run("an Apple terminal program skips the extra queries", func(t *testing.T) {
		t.Parallel()
		s := extractRaw(t, QueryCmd(uv.Environ{"TERM_PROGRAM=Apple_Terminal", "TERM=xterm-256color"}))
		require.NotContains(t, s, ansi.RequestNameVersion)
	})

	t.Run("tmux wraps the Kitty query in a passthrough sequence", func(t *testing.T) {
		t.Parallel()
		s := extractRaw(t, QueryCmd(uv.Environ{"TMUX=/tmp/tmux-1000/default,1234,0", "TERM=xterm-kitty"}))
		require.Contains(t, s, ansi.RequestNameVersion)
		require.Contains(t, s, "\x1bPtmux;", "the kitty query must be wrapped for the outer terminal")
	})
}

func TestModeSupported(t *testing.T) {
	t.Parallel()

	require.True(t, modeSupported(ansi.ModeSet))
	require.True(t, modeSupported(ansi.ModePermanentlySet))
	require.True(t, modeSupported(ansi.ModeReset))
	require.True(t, modeSupported(ansi.ModePermanentlyReset))
	require.False(t, modeSupported(ansi.ModeNotRecognized))
}

func TestShouldQueryCapabilities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  uv.Environ
		want bool
	}{
		{name: "no term program or ssh queries by default", env: uv.Environ{}, want: true},
		{name: "an Apple terminal program never queries", env: uv.Environ{"TERM_PROGRAM=Apple_Terminal"}, want: false},
		{name: "a non-Apple term program without ssh queries", env: uv.Environ{"TERM_PROGRAM=iTerm.app"}, want: true},
		{
			name: "a non-Apple term program over ssh does not query",
			env:  uv.Environ{"TERM_PROGRAM=iTerm.app", "SSH_TTY=/dev/ttys001", "TERM=xterm-256color"},
			want: false,
		},
		{
			name: "a kitty-capable TERM over ssh still queries",
			env:  uv.Environ{"TERM_PROGRAM=iTerm.app", "SSH_TTY=/dev/ttys001", "TERM=xterm-kitty"},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, shouldQueryCapabilities(tt.env))
		})
	}
}
