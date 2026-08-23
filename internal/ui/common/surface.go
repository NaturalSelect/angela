package common

import (
	"image"
	"image/color"

	uv "github.com/charmbracelet/ultraviolet"
)

// This file is the one place that treats a rectangle as a surface rather than
// as somewhere to print a string. Background and foreground are independent
// per-cell attributes, so filling first and drawing text after cannot be undone
// by a style reset inside the text — which is what breaks the equivalent
// lipgloss.Background() approach on multi-line content.

// FillRect paints every cell in area with base.
func FillRect(scr uv.Screen, area uv.Rectangle, base uv.Style) {
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			scr.SetCell(x, y, &uv.Cell{Content: " ", Style: base, Width: 1})
		}
	}
}

// DrawOnSurface fills area with base and draws content over it. Cells that the
// content leaves without a background of their own inherit the fill, so an
// inner reset cannot punch a hole in the surface.
func DrawOnSurface(scr uv.Screen, area uv.Rectangle, base uv.Style, content string) {
	FillRect(scr, area, base)
	uv.NewStyledString(content).Draw(scr, area)

	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			cell := scr.CellAt(x, y)
			if cell == nil {
				continue
			}
			if cell.Content == "" {
				scr.SetCell(x, y, &uv.Cell{Content: " ", Style: base, Width: 1})
				continue
			}
			if cell.Style.Bg == nil {
				cell.Style.Bg = base.Bg
				scr.SetCell(x, y, cell)
			}
		}
	}
}

// RenderSurface renders content onto a base-filled surface and returns it as a
// string. It is the entry point for components whose contract is
// Render(width int) string but which own a surface.
func RenderSurface(w, h int, base uv.Style, content string) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	buf := uv.NewScreenBuffer(w, h)
	DrawOnSurface(&buf, buf.Bounds(), base, content)
	return buf.Render()
}

// SetSpan writes content at (x, y) in style. When style carries no background
// the cells keep whatever background the surface already had, which is what
// lets a label sit on a filled row without cutting a notch out of it.
func SetSpan(scr uv.Screen, x, y int, style uv.Style, content string) {
	if content == "" {
		return
	}

	width := scr.WidthMethod().StringWidth(content)
	if width <= 0 {
		return
	}

	under := make([]color.Color, width)
	for i := range width {
		if cell := scr.CellAt(x+i, y); cell != nil {
			under[i] = cell.Style.Bg
		}
	}

	uv.NewStyledString(content).Draw(scr, image.Rect(x, y, x+width, y+1))

	for i := range width {
		cell := scr.CellAt(x+i, y)
		if cell == nil {
			continue
		}
		cell.Style = style
		if style.Bg == nil {
			cell.Style.Bg = under[i]
		}
		scr.SetCell(x+i, y, cell)
	}
}
