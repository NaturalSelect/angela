// Package logo renders an Angela wordmark in a stylized way.
package logo

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
)

// letterform represents a letterform. It can be stretched horizontally by
// a given amount via the boolean argument.
type letterform func(bool) string

const diag = `╱`

// Opts are the options for rendering the Angela title art.
type Opts struct {
	FieldColor   color.Color // diagonal lines
	TitleColorA  color.Color // left gradient ramp point
	TitleColorB  color.Color // right gradient ramp point
	CharmColor   color.Color // Charm™ text color
	VersionColor color.Color // version text color
	Width        int         // width of the rendered logo, used for truncation

	// When true, stretch a random letterform on each render. Has no effect in
	// compact mode. Mainly for testing. In production you will want to cache
	// the stretched letterform to keep the logo from jittering on resize.
	//
	// NOTE: currently a no-op — Render doesn't use letterforms while the
	// angela title is a placeholder (see Render's doc comment).
	Unstable bool
}

// Render renders the Angela logo. Set the argument to true to render the narrow
// version, intended for use in a sidebar.
//
// The compact argument determines whether it renders compact for the sidebar
// or wider for the main pane.
//
// NOTE: the title is rendered as plain gradient text rather than blocky
// letterforms. The letterform set in letterforms.go only covers
// C/E/EAlt/H/P/R/SAlt/U/Y/YAlt and can't spell "ANGELA" (missing A/N/G/L).
// This is a placeholder until those letterforms are designed.
func Render(base lipgloss.Style, version string, compact bool, o Opts) string {
	fg := func(c color.Color, s string) string {
		return lipgloss.NewStyle().Foreground(c).Render(s)
	}

	// Title.
	name := "angela"
	angela := styles.ApplyForegroundGrad(base, name, o.TitleColorA, o.TitleColorB)
	angelaWidth := lipgloss.Width(angela)

	// Version. (There is no Charm™-equivalent mark configured for this
	// build, so the meta row is just the version, right-aligned to the
	// title width.)
	version = ansi.Truncate(version, angelaWidth, "…") // truncate version if too long.
	gap := max(0, angelaWidth-lipgloss.Width(version))
	metaRow := strings.Repeat(" ", gap) + fg(o.VersionColor, version)

	// Join the meta row and big title.
	angela = strings.TrimSpace(metaRow + "\n" + angela)

	// Narrow version.
	if compact {
		field := fg(o.FieldColor, strings.Repeat(diag, angelaWidth))
		return strings.Join([]string{field, field, angela, field, ""}, "\n")
	}

	fieldHeight := lipgloss.Height(angela)

	// Left field.
	const leftWidth = 6
	leftFieldRow := fg(o.FieldColor, strings.Repeat(diag, leftWidth))
	leftField := new(strings.Builder)
	for range fieldHeight {
		fmt.Fprintln(leftField, leftFieldRow)
	}

	// Right field.
	rightWidth := max(15, o.Width-angelaWidth-leftWidth-2) // 2 for the gap.
	const stepDownAt = 0
	rightField := new(strings.Builder)
	for i := range fieldHeight {
		width := rightWidth
		if i >= stepDownAt {
			width = rightWidth - (i - stepDownAt)
		}
		fmt.Fprint(rightField, fg(o.FieldColor, strings.Repeat(diag, width)), "\n")
	}

	// Return the wide version.
	const hGap = " "
	logo := lipgloss.JoinHorizontal(lipgloss.Top, leftField.String(), hGap, angela, hGap, rightField.String())
	if o.Width > 0 {
		// Truncate the logo to the specified width.
		lines := strings.Split(logo, "\n")
		for i, line := range lines {
			lines[i] = ansi.Truncate(line, o.Width, "")
		}
		logo = strings.Join(lines, "\n")
	}
	return logo
}

// SmallRender renders a smaller version of the Angela logo, suitable for
// smaller windows or sidebar usage.
func SmallRender(t *styles.Styles, width int) string {
	title := styles.ApplyBoldForegroundGrad(t.Logo.GradCanvas, "angela", t.Logo.SmallGradFromColor, t.Logo.SmallGradToColor)
	remainingWidth := width - lipgloss.Width(title) - 1 // 1 for the space after the name
	if remainingWidth > 0 {
		lines := strings.Repeat("╱", remainingWidth)
		title = fmt.Sprintf("%s %s", title, t.Logo.SmallDiagonals.Render(lines))
	}
	return title
}
