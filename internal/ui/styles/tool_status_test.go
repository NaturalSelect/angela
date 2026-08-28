package styles

import (
	"image/color"
	"testing"

	"github.com/stretchr/testify/require"
)

// sentinel returns a color no palette would produce by accident, so a test
// can tell which token a style actually read.
func sentinel(r, g, b uint8) color.Color {
	return color.RGBA{R: r, G: g, B: b, A: 0xff}
}

// neutralPalette fills every token with one unmistakable value. A test then
// overrides only the tokens it cares about, so a style that reads the wrong
// one lands on neutral and fails loudly instead of coincidentally matching.
func neutralPalette(neutral color.Color) quickStyleOpts {
	return quickStyleOpts{
		primary: neutral, secondary: neutral, accent: neutral, keyword: neutral,

		fgBase: neutral, bgBase: neutral, separator: neutral,
		fgSubtle: neutral, fgMoreSubtle: neutral, fgMostSubtle: neutral,
		onPrimary: neutral,

		bgMostVisible: neutral, bgLessVisible: neutral, bgLeastVisible: neutral,
		bgSelected: neutral,

		destructive: neutral, error: neutral,
		warning: neutral, warningSubtle: neutral,
		attention: neutral, busy: neutral,
		info: neutral, infoMoreSubtle: neutral, infoMostSubtle: neutral,
		success: neutral, successMoreSubtle: neutral, successMostSubtle: neutral,

		ansiBlack: neutral, ansiRed: neutral, ansiGreen: neutral, ansiYellow: neutral,
		ansiBlue: neutral, ansiMagenta: neutral, ansiCyan: neutral, ansiWhite: neutral,
		ansiBrightBlack: neutral, ansiBrightRed: neutral, ansiBrightGreen: neutral,
		ansiBrightYellow: neutral, ansiBrightBlue: neutral, ansiBrightMagenta: neutral,
		ansiBrightCyan: neutral, ansiBrightWhite: neutral,
	}
}

// TestToolStatusIconsReadTheirSemanticToken pins the four status colors to
// the tokens that carry their meaning, not merely to four distinct values.
//
// Asserting only that the colors differ lets any of them drift back to a
// neutral grey while the test stays green — which is exactly how a finished
// call ends up looking like one that never ran.
func TestToolStatusIconsReadTheirSemanticToken(t *testing.T) {
	t.Parallel()

	var (
		info    = sentinel(0x00, 0x00, 0xfe)
		success = sentinel(0x00, 0xfe, 0x00)
		warning = sentinel(0xfe, 0xfe, 0x00)
		errCol  = sentinel(0xfe, 0x00, 0x00)
	)

	opts := neutralPalette(sentinel(0x7f, 0x7f, 0x7f))
	opts.info = info
	opts.success = success
	opts.warning = warning
	opts.error = errCol

	s := quickStyle(opts)

	for _, tt := range []struct {
		name string
		got  color.Color
		want color.Color
	}{
		{"running is blue", s.Tool.IconPending.GetForeground(), info},
		{"awaiting approval is yellow", s.Tool.IconAwaitingPermission.GetForeground(), warning},
		{"success is green", s.Tool.IconSuccess.GetForeground(), success},
		{"failure is red", s.Tool.IconError.GetForeground(), errCol},
		{"cancelled is red", s.Tool.IconCancelled.GetForeground(), errCol},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, tt.got)
		})
	}
}

// TestToolStatusIconsKeepTheirGlyphs pins that recoloring did not disturb
// which glyph each status carries. The header reserves exactly one cell for
// it, and a success tick turning back into a dot would erase the difference
// between "done" and "still going" for anyone not reading color.
func TestToolStatusIconsKeepTheirGlyphs(t *testing.T) {
	t.Parallel()

	s := AngelaTeal()

	require.Equal(t, ToolPending, s.Tool.IconPending.Value())
	require.Equal(t, ToolPending, s.Tool.IconAwaitingPermission.Value())
	require.Equal(t, ToolSuccess, s.Tool.IconSuccess.Value())
	require.Equal(t, ToolError, s.Tool.IconError.Value())
	require.Equal(t, ToolPending, s.Tool.IconCancelled.Value())
}

// TestThemesDefineEveryStatusToken guards the two shipped themes against a
// missing token. quickStyle would silently apply a nil foreground, which
// renders as the terminal default and collapses the status colors together.
func TestThemesDefineEveryStatusToken(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		sty  Styles
	}{
		{"AngelaTeal", AngelaTeal()},
		{"CharmtonePantera", CharmtonePantera()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			icons := map[string]color.Color{
				"running":  tt.sty.Tool.IconPending.GetForeground(),
				"awaiting": tt.sty.Tool.IconAwaitingPermission.GetForeground(),
				"success":  tt.sty.Tool.IconSuccess.GetForeground(),
				"error":    tt.sty.Tool.IconError.GetForeground(),
			}

			seen := map[color.Color]string{}
			for name, col := range icons {
				require.NotNil(t, col, "%s has no color", name)
				if prev, dup := seen[col]; dup {
					t.Errorf("%s and %s share a color: the statuses are indistinguishable", prev, name)
				}
				seen[col] = name
			}
		})
	}
}
