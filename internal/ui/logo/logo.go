// Package logo renders an Angela wordmark in a stylized way.
package logo

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
)

// letterform represents a letterform. It can be stretched horizontally by
// a given amount via the boolean argument.
type letterform func(bool) string

// angelaLetterforms spells ANGELA.
var angelaLetterforms = []letterform{LetterA, LetterN, LetterG, LetterE, LetterL, LetterA}

// Opts are the options for rendering the Angela title art.
type Opts struct {
	TitleColorA  color.Color // left gradient ramp point
	TitleColorB  color.Color // right gradient ramp point
	VersionColor color.Color // version text color
	Width        int         // width of the rendered logo, used for truncation

	// When true, stretch a random letterform on each render. Mainly for
	// testing. In production you will want to cache the stretched
	// letterform to keep the logo from jittering on resize.
	Unstable bool
}

// Render renders the Angela wordmark as a block-character letterform wall,
// gradient-tinted column by column.
//
// The compact argument selects the single-line wordmark used where a full
// letterform wall doesn't fit, such as a one-row header.
func Render(base lipgloss.Style, version string, compact bool, o Opts) string {
	if compact {
		return compactRender(base, version, o)
	}

	stretch := -1
	if o.Unstable {
		stretch = cachedRandN(len(angelaLetterforms))
	}

	wall := renderWord(1, stretch, angelaLetterforms...)
	logo := tintColumns(wall, o.TitleColorA, o.TitleColorB)
	logo = appendVersion(logo, version, o.VersionColor)

	if o.Width > 0 {
		lines := strings.Split(logo, "\n")
		for i, line := range lines {
			lines[i] = ansi.Truncate(line, o.Width, "")
		}
		logo = strings.Join(lines, "\n")
	}
	return logo
}

// compactRender renders the single-line wordmark plus the version.
func compactRender(base lipgloss.Style, version string, o Opts) string {
	title := styles.ApplyBoldForegroundGrad(base, WordmarkText, o.TitleColorA, o.TitleColorB)
	if version == "" {
		return title
	}
	return title + "  " + lipgloss.NewStyle().Foreground(o.VersionColor).Render(version)
}

// WordmarkText is the letter-spaced Angela wordmark used wherever the
// letterform wall is too tall to fit.
const WordmarkText = "A N G E L A"

// Wordmark renders the single-line Angela wordmark, letter-spaced and
// gradient-tinted. It's the mark used in one-row chrome such as the header and
// the post-exit banner.
func Wordmark(t *styles.Styles, width int) string {
	mark := styles.ApplyBoldForegroundGrad(
		t.Logo.GradCanvas,
		WordmarkText,
		t.Logo.SmallGradFromColor,
		t.Logo.SmallGradToColor,
	)
	if width > 0 {
		mark = ansi.Truncate(mark, width, "")
	}
	return mark
}

// appendVersion adds a right-aligned caption row beneath the wordmark.
func appendVersion(logo, version string, c color.Color) string {
	if version == "" {
		return logo
	}

	lines := strings.Split(logo, "\n")
	width := 0
	for _, line := range lines {
		width = max(width, ansi.StringWidth(line))
	}

	version = ansi.Truncate(version, width, "…")
	gap := max(0, width-ansi.StringWidth(version))
	caption := strings.Repeat(" ", gap) +
		lipgloss.NewStyle().Foreground(c).Render(version)

	return logo + "\n" + caption
}

// tintColumns tints the letterform wall column by column along the gradient,
// leaving blank cells untouched.
func tintColumns(wall string, from, to color.Color) string {
	rows := strings.Split(wall, "\n")
	width := 0
	grid := make([][]rune, len(rows))
	for y, row := range rows {
		grid[y] = []rune(row)
		width = max(width, len(grid[y]))
	}

	ramp := lipgloss.Blend1D(max(width, 1), from, to)
	var b strings.Builder
	for y, row := range grid {
		if y > 0 {
			b.WriteRune('\n')
		}
		for x, r := range row {
			if r == ' ' {
				b.WriteRune(' ')
				continue
			}
			b.WriteString(lipgloss.NewStyle().Foreground(ramp[x]).Render(string(r)))
		}
	}
	return b.String()
}
