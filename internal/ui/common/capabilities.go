package common

import (
	"bytes"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	xstrings "github.com/charmbracelet/x/exp/strings"

	"github.com/NaturalSelect/angela/internal/ui/notification"
)

// Capabilities define different terminal capabilities supported.
type Capabilities struct {
	// Profile is the terminal color profile used to determine how colors are
	// rendered.
	Profile colorprofile.Profile
	// Columns is the number of character columns in the terminal.
	Columns int
	// Rows is the number of character rows in the terminal.
	Rows int
	// PixelX is the width of the terminal in pixels.
	PixelX int
	// PixelY is the height of the terminal in pixels.
	PixelY int
	// KittyGraphics indicates whether the terminal supports the Kitty graphics
	// protocol.
	KittyGraphics bool
	// SixelGraphics indicates whether the terminal supports Sixel graphics.
	SixelGraphics bool
	// Env is the terminal environment variables.
	Env uv.Environ
	// TerminalVersion is the terminal version string.
	TerminalVersion string
	// ReportFocusEvents indicates whether the terminal supports focus events.
	ReportFocusEvents bool
	// OSC99Notifications indicates whether the terminal supports OSC 99 notifications.
	OSC99Notifications bool
	// KittyPlaceholdersOverride overrides auto-detection of Kitty graphics
	// Unicode placeholder (virtual placement) support; nil defers to
	// [kittyPlaceholderTerminal]. Set from --kitty-placeholders or
	// options.tui.kitty_placeholders.
	KittyPlaceholdersOverride *bool
}

// Update updates the capabilities based on the given message.
func (c *Capabilities) Update(msg any) {
	switch m := msg.(type) {
	case tea.EnvMsg:
		c.Env = uv.Environ(m)
	case tea.ColorProfileMsg:
		c.Profile = m.Profile
	case tea.WindowSizeMsg:
		c.Columns = m.Width
		c.Rows = m.Height
	case uv.PixelSizeEvent:
		c.PixelX = m.Width
		c.PixelY = m.Height
	case uv.KittyGraphicsEvent:
		// The terminal's response payload is "OK" on success and
		// "ERROR:..." (or similar) on failure; only an affirmative
		// response means the terminal actually supports the Kitty
		// graphics protocol. See
		// https://sw.kovidgoyal.net/kitty/graphics-protocol/.
		if bytes.HasPrefix(m.Payload, []byte("OK")) {
			c.KittyGraphics = true
		}
	case uv.PrimaryDeviceAttributesEvent:
		if slices.Contains(m, 4) {
			c.SixelGraphics = true
		}
	case tea.TerminalVersionMsg:
		c.TerminalVersion = m.Name
	case tea.ModeReportMsg:
		switch m.Mode {
		case ansi.ModeFocusEvent:
			c.ReportFocusEvents = modeSupported(m.Value)
		}
	case uv.UnknownOscEvent:
		if notification.DetectOSC99Support(string(m)) {
			c.OSC99Notifications = true
		}
	}
}

// QueryCmd returns a [tea.Cmd] that queries the terminal for different
// capabilities.
func QueryCmd(env uv.Environ) tea.Cmd {
	var sb strings.Builder
	sb.WriteString(ansi.RequestPrimaryDeviceAttributes)
	sb.WriteString(ansi.QueryModifyOtherKeys)
	sb.WriteString(ansi.RequestModeFocusEvent)
	sb.WriteString(notification.OSC99QuerySequence())

	// Queries that should only be sent to "smart" normal terminals.
	shouldQueryFor := shouldQueryCapabilities(env)
	if shouldQueryFor {
		sb.WriteString(ansi.RequestNameVersion)
		sb.WriteString(ansi.WindowOp(14)) // Window size in pixels
		kittyReq := ansi.KittyGraphics([]byte("AAAA"), "i=31", "s=1", "v=1", "a=q", "t=d", "f=24")
		if _, isTmux := env.LookupEnv("TMUX"); isTmux {
			kittyReq = ansi.TmuxPassthrough(kittyReq)
		}
		sb.WriteString(kittyReq)
	}

	return tea.Raw(sb.String())
}

// SupportsTrueColor returns true if the terminal supports true color.
func (c Capabilities) SupportsTrueColor() bool {
	return c.Profile == colorprofile.TrueColor
}

// SupportsKittyGraphics returns true if the terminal supports Kitty graphics.
func (c Capabilities) SupportsKittyGraphics() bool {
	return c.KittyGraphics
}

// placeholderTerminals lists terminals known to correctly render Kitty
// graphics Unicode placeholders (virtual placement), which inline image
// previews and the built-in ImageGenerate/ImageEdit tools require.
// Terminals that only answer the basic Kitty graphics query (a=q) without
// rendering placeholders correctly, such as VS Code's integrated terminal
// (xterm.js) and WezTerm, are deliberately left out.
var placeholderTerminals = []string{"kitty", "ghostty", "rio"}

// multiplexerPrefixes lists terminal multiplexers whose own XTVERSION or
// TERM_PROGRAM value must be looked past, in favor of environment
// variables inherited from the terminal underneath, to identify the real
// terminal.
var multiplexerPrefixes = []string{"tmux", "screen", "zellij"}

// hasAnyPrefix reports whether s starts with any of prefixes.
func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// kittyPlaceholderTerminal reports whether the terminal identified by
// version (the XTVERSION reply) and env is known to render Kitty
// graphics Unicode placeholders correctly. A basic Kitty graphics query
// response never revokes once acted on (see App.SetClientImageSupport),
// so this never returns true for an unrecognized, non-empty version.
func kittyPlaceholderTerminal(version string, env uv.Environ) bool {
	v := strings.ToLower(version)
	if v != "" && !hasAnyPrefix(v, multiplexerPrefixes...) {
		return hasAnyPrefix(v, placeholderTerminals...)
	}

	if termProg, ok := env.LookupEnv("TERM_PROGRAM"); ok {
		tp := strings.ToLower(termProg)
		if tp != "tmux" {
			return slices.Contains(placeholderTerminals, tp)
		}
	}

	termType := strings.ToLower(env.Getenv("TERM"))
	if xstrings.ContainsAnyOf(termType, placeholderTerminals...) {
		return true
	}
	if _, ok := env.LookupEnv("KITTY_WINDOW_ID"); ok {
		return true
	}
	_, ok := env.LookupEnv("GHOSTTY_RESOURCES_DIR")
	return ok
}

// SupportsKittyPlaceholders returns true if the terminal supports the
// Kitty graphics protocol's Unicode placeholders (virtual placement),
// which the chat's inline image previews and the built-in
// ImageGenerate/ImageEdit tools require. A positive [Capabilities.SupportsKittyGraphics]
// only proves the terminal understands the basic protocol; many
// terminals (notably VS Code's integrated terminal and WezTerm) answer
// that query but do not render placeholders, so this is checked
// separately. KittyPlaceholdersOverride, when set, always wins.
func (c Capabilities) SupportsKittyPlaceholders() bool {
	if !c.KittyGraphics {
		return false
	}
	if c.KittyPlaceholdersOverride != nil {
		return *c.KittyPlaceholdersOverride
	}
	return kittyPlaceholderTerminal(c.TerminalVersion, c.Env)
}

// SupportsSixelGraphics returns true if the terminal supports Sixel graphics.
func (c Capabilities) SupportsSixelGraphics() bool {
	return c.SixelGraphics
}

// CellSize returns the size of a single terminal cell in pixels.
func (c Capabilities) CellSize() (width, height int) {
	if c.Columns == 0 || c.Rows == 0 {
		return 0, 0
	}
	return c.PixelX / c.Columns, c.PixelY / c.Rows
}

func modeSupported(v ansi.ModeSetting) bool {
	return v.IsSet() || v.IsReset()
}

// kittyTerminals defines terminals supporting querying capabilities.
var kittyTerminals = []string{"alacritty", "ghostty", "kitty", "rio", "wezterm"}

func shouldQueryCapabilities(env uv.Environ) bool {
	const osVendorTypeApple = "Apple"
	termType := env.Getenv("TERM")
	termProg, okTermProg := env.LookupEnv("TERM_PROGRAM")
	_, okSSHTTY := env.LookupEnv("SSH_TTY")
	if okTermProg && strings.Contains(termProg, osVendorTypeApple) {
		return false
	}
	return (!okTermProg && !okSSHTTY) ||
		(!strings.Contains(termProg, osVendorTypeApple) && !okSSHTTY) ||
		// Terminals that do support XTVERSION.
		xstrings.ContainsAnyOf(termType, kittyTerminals...)
}
