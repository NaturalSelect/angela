package chat

import (
	"fmt"
	"strings"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/NaturalSelect/angela/internal/ui/anim"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/list"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
)

// sideQuestionSeq provides unique IDs for SideQuestionItems even when the
// same question is asked multiple times.
var sideQuestionSeq atomic.Int64

// SideQuestionItem renders the question and answer of a /btw side question
// in the chat with a vertical bar on the left, mirroring ShellItem. It is
// never persisted: the question and answer live only in this in-memory
// item and are gone on session reload.
type SideQuestionItem struct {
	*list.Versioned
	*highlightableMessageItem
	*cachedMessageItem
	*focusableMessageItem

	id       string
	question string
	answer   string
	err      error
	pending  bool
	sty      *styles.Styles
	anim     *anim.Anim
}

var (
	_ list.Highlightable = (*SideQuestionItem)(nil)
	_ KeyEventHandler    = (*SideQuestionItem)(nil)
	_ Animatable         = (*SideQuestionItem)(nil)
)

// NewPendingSideQuestionItem creates a SideQuestionItem in a pending state
// that displays a spinner until Complete or Fail is called.
func NewPendingSideQuestionItem(sty *styles.Styles, question string) *SideQuestionItem {
	v := list.NewVersioned()
	id := fmt.Sprintf("btw-%d", sideQuestionSeq.Add(1))
	s := &SideQuestionItem{
		Versioned:                v,
		highlightableMessageItem: defaultHighlighter(sty, v),
		cachedMessageItem:        &cachedMessageItem{},
		focusableMessageItem:     newFocusableMessageItem(v),
		id:                       id,
		question:                 question,
		sty:                      sty,
		pending:                  true,
	}
	s.anim = anim.New(anim.Settings{
		ID:         id,
		Label:      "Thinking",
		LabelColor: sty.WorkingLabelColor,
		GradColorA: sty.WorkingGradFromColor,
		GradColorB: sty.WorkingGradToColor,
		NoScramble: true,
	})
	return s
}

// Complete transitions a pending SideQuestionItem to a finished state with
// the answer.
func (s *SideQuestionItem) Complete(answer string) {
	s.answer = answer
	s.err = nil
	s.pending = false
	s.clearCache()
	s.Bump()
}

// Fail transitions a pending SideQuestionItem to a finished state showing
// the error in place of an answer.
func (s *SideQuestionItem) Fail(err error) {
	s.err = err
	s.pending = false
	s.clearCache()
	s.Bump()
}

func (s *SideQuestionItem) ID() string          { return s.id }
func (s *SideQuestionItem) FilterValue() string { return s.question }
func (s *SideQuestionItem) Finished() bool      { return !s.pending }

// StartAnimation starts the spinner animation while the answer is pending.
func (s *SideQuestionItem) StartAnimation() tea.Cmd {
	if !s.pending {
		return nil
	}
	return s.anim.Start()
}

// Animate advances the spinner animation while the answer is pending.
func (s *SideQuestionItem) Animate(msg anim.StepMsg) tea.Cmd {
	if !s.pending {
		return nil
	}
	s.Bump()
	return s.anim.Animate(msg)
}

func (s *SideQuestionItem) Render(width int) string {
	innerWidth := max(0, width-MessageLeftPaddingTotal)
	content := s.RawRender(innerWidth)

	var prefix string
	if s.focused {
		prefix = s.sty.Messages.ShellBarFocused.Render()
	} else {
		prefix = s.sty.Messages.ShellBarBlurred.Render()
	}
	lines := strings.Split(content, "\n")
	for i, ln := range lines {
		lines[i] = prefix + ln
	}
	out := strings.Join(lines, "\n")

	return s.renderHighlighted(out, width, lipgloss.Height(out))
}

// HandleMouseClick implements MouseClickable so clicks select this item.
func (s *SideQuestionItem) HandleMouseClick(btn ansi.MouseButton, x, y int) bool {
	return btn == ansi.MouseLeft
}

// HandleKeyEvent implements KeyEventHandler for copying the question and
// answer to the clipboard.
func (s *SideQuestionItem) HandleKeyEvent(key tea.KeyMsg) (bool, tea.Cmd) {
	switch key.String() {
	case "c", "y":
		text := "? " + s.question + "\n" + ansi.Strip(s.answer)
		return true, common.CopyToClipboard(text, "Side question copied to clipboard")
	}
	return false, nil
}

func (s *SideQuestionItem) RawRender(width int) string {
	cappedWidth := cappedMessageWidth(width)

	q := strings.ReplaceAll(s.question, "\n", " ")
	q = strings.ReplaceAll(q, "\t", "    ")

	var prompt string
	if s.focused {
		prompt = s.sty.Messages.ShellPrompt.Render("?")
	} else {
		prompt = s.sty.Messages.ShellPromptBlurred.Render("?")
	}
	header := prompt + " " + s.sty.Messages.ShellCommand.Render(q)

	if s.pending {
		return header + "\n" + s.anim.Render()
	}

	if s.err != nil {
		return header + "\n" + s.sty.Messages.ShellExitCode.Render(s.err.Error())
	}

	content, _, ok := s.getCachedRender(cappedWidth)
	if !ok {
		content = s.renderAnswer(cappedWidth)
		s.setCachedRender(content, cappedWidth, lipgloss.Height(content))
	}
	return header + "\n" + content
}

func (s *SideQuestionItem) renderAnswer(width int) string {
	renderer := common.MarkdownRenderer(s.sty, width)
	mu := common.LockMarkdownRenderer(renderer)
	mu.Lock()
	rendered, err := renderer.Render(s.answer)
	mu.Unlock()
	if err != nil {
		return s.answer
	}
	return strings.TrimSuffix(rendered, "\n")
}
