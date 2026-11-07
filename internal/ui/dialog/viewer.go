package dialog

import (
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/ui/common"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// ViewerID is the identifier for the read-only text viewer dialog.
const ViewerID = "viewer"

// Viewer sizing caps. The viewer otherwise tracks the terminal, but Markdown
// lines stop being readable on very wide terminals.
const (
	viewerMaxWidth  = 120
	viewerMaxHeight = 60
)

// ViewerSpec is what a [Viewer] shows. Content is also what the copy key
// puts on the clipboard.
type ViewerSpec struct {
	Title     string
	TitleInfo string
	Content   string
	// Markdown renders Content through glamour; plain text otherwise.
	Markdown bool
}

// Viewer is a scrollable, read-only view over a block of text. It knows
// nothing about what it shows, so any feature that needs to display text in
// the TUI can reuse it.
type Viewer struct {
	com  *common.Common
	spec ViewerSpec

	frame    *Frame
	metrics  FrameMetrics
	help     help.Model
	viewport viewport.Model

	// renderedWidth is the content width viewport's content was last built
	// for, so the content is only re-rendered when the width changes.
	renderedWidth int

	keyMap struct {
		UpDown   key.Binding
		Up       key.Binding
		Down     key.Binding
		PageUp   key.Binding
		PageDown key.Binding
		Top      key.Binding
		Bottom   key.Binding
		Copy     key.Binding
		Close    key.Binding
	}
}

var _ Dialog = (*Viewer)(nil)

// NewViewer creates a viewer dialog showing spec.
func NewViewer(com *common.Common, spec ViewerSpec) *Viewer {
	v := &Viewer{com: com, spec: spec}

	v.frame = NewFrame(com.Styles, FrameSpec{
		Title:       spec.Title,
		TitleInfo:   spec.TitleInfo,
		MaxWidth:    viewerMaxWidth,
		WidthRatio:  0.8,
		MaxHeight:   viewerMaxHeight,
		HeightRatio: 0.8,
	})

	h := help.New()
	h.Styles = com.Styles.DialogHelpStyles()
	v.help = h

	v.viewport = viewport.New()

	v.keyMap.UpDown = key.NewBinding(
		key.WithKeys("up", "down"),
		key.WithHelp("↑/↓", "scroll"),
	)
	v.keyMap.Up = key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("↑", "scroll up"),
	)
	v.keyMap.Down = key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("↓", "scroll down"),
	)
	v.keyMap.PageUp = key.NewBinding(
		key.WithKeys("pgup", "b"),
		key.WithHelp("pgup", "page up"),
	)
	v.keyMap.PageDown = key.NewBinding(
		key.WithKeys("pgdown", "space", "f"),
		key.WithHelp("pgdn", "page down"),
	)
	v.keyMap.Top = key.NewBinding(
		key.WithKeys("home", "g"),
		key.WithHelp("g", "top"),
	)
	v.keyMap.Bottom = key.NewBinding(
		key.WithKeys("end", "G"),
		key.WithHelp("G", "bottom"),
	)
	v.keyMap.Copy = key.NewBinding(
		key.WithKeys("c", "y"),
		key.WithHelp("c", "copy"),
	)
	v.keyMap.Close = CloseKey

	return v
}

// ID implements [Dialog].
func (v *Viewer) ID() string {
	return ViewerID
}

// HandleMsg implements [Dialog].
func (v *Viewer) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, v.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, v.keyMap.Up):
			v.viewport.ScrollUp(1)
		case key.Matches(msg, v.keyMap.Down):
			v.viewport.ScrollDown(1)
		case key.Matches(msg, v.keyMap.PageUp):
			v.viewport.PageUp()
		case key.Matches(msg, v.keyMap.PageDown):
			v.viewport.PageDown()
		case key.Matches(msg, v.keyMap.Top):
			v.viewport.GotoTop()
		case key.Matches(msg, v.keyMap.Bottom):
			v.viewport.GotoBottom()
		case key.Matches(msg, v.keyMap.Copy):
			return ActionCmd{Cmd: common.CopyToClipboard(v.spec.Content, "Copied to clipboard")}
		}
	case common.CoalescedWheelMsg:
		v.viewport, _ = v.viewport.Update(tea.MouseWheelMsg(msg.Mouse))
	}
	return nil
}

// Draw implements [Dialog].
func (v *Viewer) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := v.com.Styles
	v.metrics = v.frame.Measure(area)

	helpLine := v.frame.RenderHelp(&v.help, v, v.metrics.ContentWidth)
	titleHeight := t.Dialog.Title.GetVerticalFrameSize() + titleContentHeight
	bodyHeight := max(0, v.metrics.ContentHeight-titleHeight-lipgloss.Height(helpLine))

	// NOTE: The scrollbar column is always reserved so the text does not
	// re-wrap when scrolling reveals that the content overflows.
	bodyWidth := max(1, v.metrics.ContentWidth-1)
	if v.renderedWidth != bodyWidth {
		v.viewport.SetContent(v.renderContent(bodyWidth))
		v.renderedWidth = bodyWidth
	}
	v.viewport.SetWidth(bodyWidth)
	v.viewport.SetHeight(bodyHeight)

	body := v.frame.JoinScrollbar(
		v.viewport.View(),
		bodyHeight,
		v.viewport.TotalLineCount(),
		bodyHeight,
		v.viewport.YOffset(),
	)

	view := v.frame.Render(v.metrics, []string{body}, helpLine)
	return v.frame.Draw(scr, area, view, nil)
}

// renderContent lays the spec's content out for width columns.
func (v *Viewer) renderContent(width int) string {
	if !v.spec.Markdown {
		return ansi.Wrap(v.spec.Content, width, "")
	}
	renderer := common.MarkdownRenderer(v.com.Styles, width)
	mu := common.LockMarkdownRenderer(renderer)
	mu.Lock()
	rendered, err := renderer.Render(v.spec.Content)
	mu.Unlock()
	if err != nil {
		return ansi.Wrap(v.spec.Content, width, "")
	}
	return strings.TrimSuffix(rendered, "\n")
}

// ShortHelp implements [help.KeyMap].
func (v *Viewer) ShortHelp() []key.Binding {
	return []key.Binding{
		v.keyMap.UpDown,
		v.keyMap.Copy,
		v.keyMap.Close,
	}
}

// FullHelp implements [help.KeyMap].
func (v *Viewer) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{v.keyMap.Up, v.keyMap.Down, v.keyMap.PageUp, v.keyMap.PageDown},
		{v.keyMap.Top, v.keyMap.Bottom, v.keyMap.Copy, v.keyMap.Close},
	}
}
