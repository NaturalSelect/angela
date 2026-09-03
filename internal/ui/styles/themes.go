package styles

import (
	"image/color"

	"charm.land/glamour/v2/ansi"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
)

// c is a shorthand for declaring palette colors by hex.
func c(hex string) color.Color {
	return lipgloss.Color(hex)
}

// AngelaTeal returns Angela's default theme: a dark palette modeled on
// VS Code's Dark+ theme. Surfaces and text follow the editor/sidebar
// backgrounds and foreground ramp; hue is reserved for genuine status
// signals, code syntax categories, and heading/emphasis accents, mapped
// onto VS Code's own keyword/type/string hues so the chat's markdown and
// code blocks read as a natural extension of the editor.
func AngelaTeal() Styles {
	s := quickStyle(quickStyleOpts{
		// Brand. Drives H1/H2 headings, inline emphasis, and the landing
		// logo gradient.
		primary:   c("#569cd6"),
		secondary: c("#4ec9b0"),
		accent:    c("#9cdcfe"),
		keyword:   c("#c586c0"),

		// Text ramp: body, prose, labels, metadata. Matches VS Code's
		// editor foreground and its dimmer UI-text steps.
		fgBase:       c("#d4d4d4"),
		fgSubtle:     c("#cccccc"),
		fgMoreSubtle: c("#9d9d9d"),
		fgMostSubtle: c("#6a6a6a"),

		// Surfaces, darkest to lightest. Matches VS Code's editor,
		// sidebar, and input-widget backgrounds.
		bgBase:         c("#1e1e1e"),
		bgLeastVisible: c("#252526"),
		bgLessVisible:  c("#2d2d2d"),
		bgMostVisible:  c("#3c3c3c"),
		bgSelected:     c("#04395e"),
		bgHeader:       c("#252526"),

		separator: c("#454545"),

		// Ink for text sitting on a saturated background.
		onPrimary: c("#1e1e1e"),

		// Statuses, matching VS Code's editor diagnostic colors.
		destructive:       c("#f44747"),
		error:             c("#f44747"),
		warning:           c("#cca700"),
		warningSubtle:     c("#d7ba7d"),
		attention:         c("#cca700"),
		busy:              c("#cca700"),
		info:              c("#3794ff"),
		infoMoreSubtle:    c("#9cdcfe"),
		infoMostSubtle:    c("#2d5c8a"),
		success:           c("#89d185"),
		successMoreSubtle: c("#6a9955"),
		successMostSubtle: c("#4b7043"),

		// ANSI 16-color palette for remapping raw terminal output (e.g.
		// bang-mode shell commands) onto VS Code's integrated terminal
		// defaults.
		ansiBlack:   c("#000000"),
		ansiRed:     c("#cd3131"),
		ansiGreen:   c("#0dbc79"),
		ansiYellow:  c("#e5e510"),
		ansiBlue:    c("#2472c8"),
		ansiMagenta: c("#bc3fbc"),
		ansiCyan:    c("#11a8cd"),
		ansiWhite:   c("#e5e5e5"),

		ansiBrightBlack:   c("#666666"),
		ansiBrightRed:     c("#f14c4c"),
		ansiBrightGreen:   c("#23d18b"),
		ansiBrightYellow:  c("#f5f543"),
		ansiBrightBlue:    c("#3b8eea"),
		ansiBrightMagenta: c("#d670d6"),
		ansiBrightCyan:    c("#29b8db"),
		ansiBrightWhite:   c("#e5e5e5"),
	})

	// Code-block syntax highlighting: quickStyle leaves these Chroma
	// token categories hardcoded to Charmtone colors pending
	// tokenization (see internal/ui/AGENTS.md). Override them here with
	// VS Code Dark+'s own token colors so code rendered in chat matches
	// VS Code exactly, decoupled from shared UI tokens reused elsewhere
	// (e.g. fgMostSubtle, successMostSubtle) for unrelated chrome.
	chroma := s.Markdown.CodeBlock.Chroma
	chroma.Comment = ansi.StylePrimitive{Color: hex(c("#6a9955"))}
	chroma.CommentPreproc = ansi.StylePrimitive{Color: hex(c("#569cd6"))}
	chroma.Keyword = ansi.StylePrimitive{Color: hex(c("#569cd6"))}
	chroma.KeywordReserved = ansi.StylePrimitive{Color: hex(c("#c586c0"))}
	chroma.KeywordNamespace = ansi.StylePrimitive{Color: hex(c("#c586c0"))}
	chroma.KeywordType = ansi.StylePrimitive{Color: hex(c("#4ec9b0"))}
	chroma.Operator = ansi.StylePrimitive{Color: hex(c("#d4d4d4"))}
	chroma.Punctuation = ansi.StylePrimitive{Color: hex(c("#d4d4d4"))}
	chroma.NameBuiltin = ansi.StylePrimitive{Color: hex(c("#569cd6"))}
	chroma.NameTag = ansi.StylePrimitive{Color: hex(c("#569cd6"))}
	chroma.NameAttribute = ansi.StylePrimitive{Color: hex(c("#9cdcfe"))}
	chroma.NameClass = ansi.StylePrimitive{Color: hex(c("#4ec9b0"))}
	chroma.NameDecorator = ansi.StylePrimitive{Color: hex(c("#dcdcaa"))}
	chroma.NameFunction = ansi.StylePrimitive{Color: hex(c("#dcdcaa"))}
	chroma.LiteralNumber = ansi.StylePrimitive{Color: hex(c("#b5cea8"))}
	chroma.LiteralString = ansi.StylePrimitive{Color: hex(c("#ce9178"))}
	chroma.LiteralStringEscape = ansi.StylePrimitive{Color: hex(c("#d7ba7d"))}

	// Markdown bold reads brighter than intended at secondary's full
	// saturation; mute it here without touching secondary itself, which
	// the landing logo gradient still uses at full strength.
	s.Markdown.Strong.Color = hex(c("#3ca08d"))

	return s
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
		bgHeader:       charmtone.Char,

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

	// Bang ! rail override - use the Hazy accent. Yolo keeps quickStyle's
	// default (error-red), since it needs to read as dangerous rather than
	// share Bang's color.
	s.Editor.RailBang = s.Editor.RailBang.Foreground(charmtone.Hazy)

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
