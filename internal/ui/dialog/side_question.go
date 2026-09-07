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
)

// SideQuestionID is the identifier for the side question answer dialog.
const SideQuestionID = "side_question"

// SideQuestion displays the answer to a one-off question asked via
// /btw from the session's existing context. It is read-only: the
// question and its answer never enter the session history, and the
// only actions here are scrolling and dismissing.
type SideQuestion struct {
	com     *common.Common
	content string // markdown source: the quoted question plus the answer

	frame *Frame
	help  help.Model

	viewport      viewport.Model
	viewportDirty bool

	keyMap struct {
		Close   key.Binding
		Confirm key.Binding
		Scroll  key.Binding
	}
}

var _ Dialog = (*SideQuestion)(nil)

// NewSideQuestion creates a dialog showing the answer to a side
// question asked from the session's existing context.
func NewSideQuestion(com *common.Common, question, answer string) *SideQuestion {
	quoted := "> " + strings.ReplaceAll(strings.TrimSpace(question), "\n", "\n> ")

	s := &SideQuestion{
		com:     com,
		content: quoted + "\n\n" + answer,
		frame: NewFrame(com.Styles, FrameSpec{
			Title:       "Side Question",
			MaxWidth:    simpleMaxWidth,
			WidthRatio:  simpleSizeRatio,
			HeightRatio: simpleHeightRatio,
			Gap:         1,
		}),
		viewportDirty: true,
	}
	s.viewport = viewport.New()

	s.help = help.New()
	s.help.Styles = com.Styles.DialogHelpStyles()

	s.keyMap.Close = CloseKey
	s.keyMap.Confirm = key.NewBinding(key.WithKeys("enter", "space"), key.WithHelp("enter/space", "close"))
	s.keyMap.Scroll = key.NewBinding(key.WithKeys("up", "down", "pgup", "pgdown"), key.WithHelp("↑/↓", "scroll"))

	return s
}

// ID implements [Dialog].
func (*SideQuestion) ID() string {
	return SideQuestionID
}

// HandleMsg implements [Dialog].
func (s *SideQuestion) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, s.keyMap.Close), key.Matches(msg, s.keyMap.Confirm):
			return ActionClose{}
		default:
			s.viewport, _ = s.viewport.Update(msg)
		}
	case common.CoalescedWheelMsg:
		s.viewport, _ = s.viewport.Update(tea.MouseWheelMsg(msg.Mouse))
	}
	return nil
}

// renderContent renders the quoted question and the markdown-formatted
// answer at the given width.
func (s *SideQuestion) renderContent(width int) string {
	t := s.com.Styles
	r := common.MarkdownRenderer(t, width)
	mu := common.LockMarkdownRenderer(r)
	mu.Lock()
	rendered, err := r.Render(s.content)
	mu.Unlock()
	if err != nil {
		return s.content
	}
	return strings.TrimSuffix(rendered, "\n")
}

// Draw implements [Dialog].
func (s *SideQuestion) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	metrics := s.frame.Measure(area)
	contentWidth := metrics.ContentWidth

	helpView := s.frame.RenderHelp(&s.help, s, contentWidth)
	// Fixed rows outside the scrollable viewport: the one-line title,
	// the help footer, and the two blank gap lines Frame.Render inserts
	// around the single content part (FrameSpec.Gap: 1).
	fixedHeight := titleContentHeight + 2 + lipgloss.Height(helpView)
	availableHeight := max(metrics.ContentHeight-fixedHeight, 3)

	viewportWidth := contentWidth
	renderedContent := s.renderContent(viewportWidth)
	needsScrollbar := lipgloss.Height(renderedContent) > availableHeight
	if needsScrollbar {
		viewportWidth = contentWidth - 1
		renderedContent = s.renderContent(viewportWidth)
	}

	if s.viewport.Width() != viewportWidth {
		s.viewportDirty = true
	}
	s.viewport.SetWidth(viewportWidth)
	s.viewport.SetHeight(availableHeight)
	if s.viewportDirty {
		s.viewport.SetContent(renderedContent)
		s.viewportDirty = false
	}

	content := s.viewport.View()
	if needsScrollbar {
		content = s.frame.JoinScrollbar(content, availableHeight, s.viewport.TotalLineCount(), availableHeight, s.viewport.YOffset())
	}

	view := s.frame.Render(metrics, []string{content}, helpView)
	return s.frame.Draw(scr, area, view, nil)
}

// ShortHelp implements [help.KeyMap].
func (s *SideQuestion) ShortHelp() []key.Binding {
	return []key.Binding{s.keyMap.Scroll, s.keyMap.Confirm, s.keyMap.Close}
}

// FullHelp implements [help.KeyMap].
func (s *SideQuestion) FullHelp() [][]key.Binding {
	return [][]key.Binding{s.ShortHelp()}
}
