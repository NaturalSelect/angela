package model

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/fsext"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/logo"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

const (
	// The app rect already insets the content, so the header adds none of
	// its own.
	leftPadding         = 0
	rightPadding        = 0
	logoToDetailSpacing = 1 // minimum gap between the wordmark and details

	// headerDivider separates the brand from the session context, so the
	// wordmark does not read as the first crumb of the breadcrumb.
	headerDivider = "  │  "
)

type header struct {
	// mark is the cached one-line wordmark.
	mark string

	com *common.Common
}

// newHeader creates a new header model.
func newHeader(com *common.Common) *header {
	h := &header{
		com: com,
	}
	h.refresh()
	return h
}

// refresh rebuilds the cached wordmark using the current styles. Call after
// the theme changes.
func (h *header) refresh() {
	h.mark = logo.Wordmark(h.com.Styles, 0)
}

// drawHeader draws the one-row header band: the wordmark, the session
// breadcrumb, and the working directory flush right. lspErrorCount comes from
// the UI's memoized state — drawing runs on every frame and must not probe the
// workspace (a synchronous HTTP round-trip in client/server mode).
func (h *header) drawHeader(
	scr uv.Screen,
	area uv.Rectangle,
	trail []string,
	width int,
	lspErrorCount int,
) {
	uv.NewStyledString(h.renderBar(trail, width, lspErrorCount)).Draw(scr, area)
}

// renderTrail joins the session titles into a breadcrumb, shedding outer
// levels as the width shrinks. The level in view is the last to go.
func (h *header) renderTrail(trail []string, width int) string {
	t := h.com.Styles
	if width <= 0 {
		return ""
	}

	sep := t.Header.Breadcrumb.Render(" › ")
	render := func(levels []string, elided bool) string {
		parts := make([]string, 0, len(levels)+1)
		if elided {
			parts = append(parts, t.Header.Breadcrumb.Render("…"))
		}
		for i, title := range levels {
			style := t.Header.Breadcrumb
			if i == len(levels)-1 {
				style = t.Header.SessionTitle
			}
			parts = append(parts, style.Render(title))
		}
		return strings.Join(parts, sep)
	}

	// Drop ancestors one at a time. The leading "…" keeps the fact that there
	// are levels above in view even once their names no longer fit.
	for n := len(trail); n >= 1; n-- {
		candidate := render(trail[len(trail)-n:], n < len(trail))
		if lipgloss.Width(candidate) <= width {
			return candidate
		}
	}
	return t.Header.SessionTitle.Render(ansi.Truncate(trail[len(trail)-1], width, "…"))
}

// renderBar renders the one-row header: wordmark on the left, session title in
// the middle, working directory and LSP errors flush right.
func (h *header) renderBar(trail []string, width int, lspErrorCount int) string {
	t := h.com.Styles

	inner := width - leftPadding - rightPadding
	markWidth := lipgloss.Width(h.mark)

	details := renderHeaderDetails(
		h.com,
		lspErrorCount,
		max(0, inner-markWidth-logoToDetailSpacing),
	)

	left := h.mark
	if len(trail) > 0 {
		divider := t.Header.Separator.Render(headerDivider)
		avail := inner - markWidth - lipgloss.Width(details) -
			logoToDetailSpacing - lipgloss.Width(divider)
		if avail > 0 {
			left += divider + h.renderTrail(trail, avail)
		}
	}

	gap := inner - lipgloss.Width(left) - lipgloss.Width(details)

	var b strings.Builder
	b.WriteString(left)
	b.WriteString(strings.Repeat(" ", max(logoToDetailSpacing, gap)))
	b.WriteString(details)

	return t.Header.Wrapper.
		Padding(0, rightPadding, 0, leftPadding).
		Render(b.String())
}

// renderHeaderDetails renders the right-hand side of the one-row header. The
// context percentage lives in the turn status and the details keystroke in the
// help line, so neither appears here.
func renderHeaderDetails(
	com *common.Common,
	lspErrorCount int,
	availWidth int,
) string {
	t := com.Styles

	const dirTrimLimit = 4
	cwd := fsext.DirTrim(fsext.PrettyPath(com.Workspace.WorkingDir()), dirTrimLimit)
	result := t.Header.WorkingDir.Render(cwd)

	if lspErrorCount > 0 {
		errs := t.LSP.ErrorDiagnostic.Render(
			fmt.Sprintf("%s%d", styles.LSPErrorIcon, lspErrorCount),
		)
		result = errs + t.Header.Separator.Render(" • ") + result
	}

	return ansi.Truncate(result, max(0, availWidth), "…")
}
