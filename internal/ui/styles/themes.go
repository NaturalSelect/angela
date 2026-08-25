package styles

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
)

// c is a shorthand for declaring palette colors by hex.
func c(hex string) color.Color {
	return lipgloss.Color(hex)
}

// AngelaTeal returns Angela's default theme: a near-monochrome graphite UI
// built on a four-step gray ramp. Chrome — borders, spinners, prompts,
// selection — never leaves that ramp. Hue is reserved for genuine status
// signals (error, success, warning) and appears as a foreground color, never
// as a filled chip. The one exception is the landing-page logo, which is the
// only place the teal brand gradient is drawn.
func AngelaTeal() Styles {
	return quickStyle(quickStyleOpts{
		// Brand. Used by the landing logo gradient and nothing else.
		primary:   c("#2dd4bf"),
		secondary: c("#5eead4"),
		accent:    c("#14b8a6"),
		keyword:   c("#bb9af7"),

		// Text ramp: body, prose, labels, metadata.
		fgBase:       c("#e1e1e1"),
		fgSubtle:     c("#c8c8c8"),
		fgMoreSubtle: c("#6c6c6c"),
		fgMostSubtle: c("#585858"),

		// Surfaces, darkest to lightest.
		bgBase:         c("#141414"),
		bgLeastVisible: c("#1c1c1c"),
		bgLessVisible:  c("#242424"),
		bgMostVisible:  c("#505058"),
		bgSelected:     c("#363636"),

		separator: c("#323237"),

		// Ink for text sitting on a saturated background.
		onPrimary: c("#08100f"),

		// Statuses. Desaturated on purpose: these read as signals against
		// the gray ramp without competing with the text they annotate.
		destructive:       c("#f7768e"),
		error:             c("#f7768e"),
		warning:           c("#e0af68"),
		warningSubtle:     c("#c99a3f"),
		attention:         c("#e0af68"),
		busy:              c("#e0af68"),
		info:              c("#7aa2f7"),
		infoMoreSubtle:    c("#5aa9d6"),
		infoMostSubtle:    c("#3f7fa3"),
		success:           c("#9ece6a"),
		successMoreSubtle: c("#7fae57"),
		successMostSubtle: c("#68914a"),

		// ANSI 16-color palette for remapping raw terminal output
		// (e.g. bang-mode shell commands) onto legible colors.
		ansiBlack:   c("#1a1d20"),
		ansiRed:     c("#f7768e"),
		ansiGreen:   c("#9ece6a"),
		ansiYellow:  c("#e0af68"),
		ansiBlue:    c("#7aa2f7"),
		ansiMagenta: c("#bb9af7"),
		ansiCyan:    c("#2dd4bf"),
		ansiWhite:   c("#b1bac4"),

		ansiBrightBlack:   c("#616a73"),
		ansiBrightRed:     c("#ff8080"),
		ansiBrightGreen:   c("#b4dd8a"),
		ansiBrightYellow:  c("#f0c674"),
		ansiBrightBlue:    c("#91cbff"),
		ansiBrightMagenta: c("#e2c5ff"),
		ansiBrightCyan:    c("#5eead4"),
		ansiBrightWhite:   c("#e6e9ec"),
	})
}

// CharmtonePantera returns the Charmtone dark theme inherited from Crush.
func CharmtonePantera() Styles {
	s := quickStyle(quickStyleOpts{
		primary:   charmtone.Charple,
		secondary: charmtone.Dolly,
		accent:    charmtone.Bok,
		keyword:   charmtone.Blush,

		fgBase:       charmtone.Sash,
		fgMoreSubtle: charmtone.Squid,
		fgSubtle:     charmtone.Smoke,
		fgMostSubtle: charmtone.Oyster,

		onPrimary: charmtone.Butter,

		bgBase:         charmtone.Pepper,
		bgLeastVisible: charmtone.BBQ,
		bgLessVisible:  charmtone.Char,
		bgMostVisible:  charmtone.Iron,
		bgSelected:     charmtone.Iron,

		separator: charmtone.Char,

		destructive:       charmtone.Coral,
		error:             charmtone.Sriracha,
		warningSubtle:     charmtone.Zest,
		warning:           charmtone.Mustard,
		attention:         charmtone.Tang,
		busy:              charmtone.Citron,
		info:              charmtone.Malibu,
		infoMoreSubtle:    charmtone.Sardine,
		infoMostSubtle:    charmtone.Damson,
		success:           charmtone.Julep,
		successMoreSubtle: charmtone.Bok,
		successMostSubtle: charmtone.Guac,

		// ANSI 16-color palette for remapping raw terminal output
		// (e.g. bang-mode shell commands) onto legible Charmtone colors.
		ansiBlack:   charmtone.BBQ,
		ansiRed:     charmtone.Coral,
		ansiGreen:   charmtone.Guac,
		ansiYellow:  charmtone.Mustard,
		ansiBlue:    charmtone.Charple,
		ansiMagenta: charmtone.Dolly,
		ansiCyan:    charmtone.Malibu,
		ansiWhite:   charmtone.Smoke,

		ansiBrightBlack:   charmtone.Iron,
		ansiBrightRed:     charmtone.Tuna,
		ansiBrightGreen:   charmtone.Julep,
		ansiBrightYellow:  charmtone.Zest,
		ansiBrightBlue:    charmtone.Guppy,
		ansiBrightMagenta: charmtone.Blush,
		ansiBrightCyan:    charmtone.Sardine,
		ansiBrightWhite:   charmtone.Salt,
	})

	// Bang ! rail override - use the Hazy accent.
	s.Editor.RailBang = s.Editor.RailBang.Foreground(charmtone.Hazy)
	s.Editor.RailYolo = s.Editor.RailYolo.Foreground(charmtone.Hazy)

	// Shell bar/prompt overrides - use Charple/Iron/Hazy colors.
	s.Messages.ShellBarFocused = s.Messages.ShellBarFocused.
		BorderForeground(charmtone.Charple)
	s.Messages.ShellBarBlurred = s.Messages.ShellBarBlurred.
		BorderForeground(charmtone.Iron)
	s.Messages.ShellPrompt = s.Messages.ShellPrompt.
		Foreground(charmtone.Hazy)
	s.Messages.ShellPromptBlurred = s.Messages.ShellPromptBlurred.
		Foreground(charmtone.Hazy)

	return s
}
