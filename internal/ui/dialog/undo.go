package dialog

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/undo"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// UndoID is the identifier for the undo confirmation dialog.
const UndoID = "undo"

// maxUndoListItems caps how many paths are listed per section before
// the rest collapse into a "+N more" line, so a turn that touched many
// files doesn't blow out the dialog's height.
const maxUndoListItems = 5

// maxUndoPathWidth caps how much of a single path is shown, so one
// deeply nested file doesn't force the whole dialog wider than the
// terminal.
const maxUndoPathWidth = 64

// Undo represents a confirmation dialog for undoing a session's last
// turn, built from a previously fetched [undo.Preview].
type Undo struct {
	com        *common.Common
	sessionID  string
	preview    undo.Preview
	selectedNo bool // true if "Cancel" is selected
	keyMap     struct {
		LeftRight,
		EnterSpace,
		Tab,
		Close key.Binding
	}
}

var _ Dialog = (*Undo)(nil)

// NewUndo creates a new undo confirmation dialog for the given
// session, from a preview describing what the undo would do.
func NewUndo(com *common.Common, sessionID string, preview undo.Preview) *Undo {
	u := &Undo{
		com:        com,
		sessionID:  sessionID,
		preview:    preview,
		selectedNo: true,
	}
	u.keyMap.LeftRight = key.NewBinding(
		key.WithKeys("left", "right"),
		key.WithHelp("←/→", "switch options"),
	)
	u.keyMap.EnterSpace = key.NewBinding(
		key.WithKeys("enter", " "),
		key.WithHelp("enter/space", "confirm"),
	)
	u.keyMap.Tab = key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "switch options"),
	)
	u.keyMap.Close = CloseKey
	return u
}

// ID implements [Dialog].
func (*Undo) ID() string {
	return UndoID
}

// HandleMsg implements [Dialog].
func (u *Undo) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, u.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, u.keyMap.LeftRight, u.keyMap.Tab):
			u.selectedNo = !u.selectedNo
		case key.Matches(msg, u.keyMap.EnterSpace):
			if !u.selectedNo {
				return ActionUndoConfirmed{SessionID: u.sessionID, CutMessageID: u.preview.CutMessageID}
			}
			return ActionClose{}
		}
	}

	return nil
}

// Draw implements [Dialog].
func (u *Undo) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	var (
		baseStyle = u.com.Styles.Dialog.Quit.Content
		hintStyle = u.com.Styles.Dialog.Quit.Hint
	)

	sections := []string{"Undo the last turn?"}
	if len(u.preview.Revert) > 0 {
		sections = append(sections, fmt.Sprintf("Revert (%d):\n%s", len(u.preview.Revert), formatUndoPaths(u.preview.Revert)))
	}
	if len(u.preview.Delete) > 0 {
		sections = append(sections, fmt.Sprintf("Delete (%d):\n%s", len(u.preview.Delete), formatUndoPaths(u.preview.Delete)))
	}
	if len(u.preview.Skipped) > 0 {
		sections = append(sections, fmt.Sprintf("Skipped (%d):\n%s", len(u.preview.Skipped), formatUndoSkipped(u.preview.Skipped)))
	}
	sections = append(sections, fmt.Sprintf("%d message(s) will be removed and their text returned to the editor.", u.preview.MessageCount))
	body := strings.Join(sections, "\n\n")

	buttonOpts := []common.ButtonOpts{
		{Text: "Undo", Selected: !u.selectedNo, Padding: 3},
		{Text: "Cancel", Selected: u.selectedNo, Padding: 3},
	}
	buttons := common.ButtonGroup(u.com.Styles, buttonOpts, " ")

	content := baseStyle.Render(
		lipgloss.JoinVertical(
			lipgloss.Left,
			body,
			"",
			lipgloss.PlaceHorizontal(lipgloss.Width(body), lipgloss.Center, buttons),
			"",
			hintStyle.Render("Only file edits are reverted; other side effects (e.g. Bash) are not."),
		),
	)

	frameStyle := u.com.Styles.Dialog.Quit.Frame
	maxWidth := area.Dx() - frameStyle.GetHorizontalBorderSize()
	if maxWidth < lipgloss.Width(content) {
		frameStyle = frameStyle.Padding(1, 0)
	}
	view := frameStyle.Render(content)
	DrawCenter(scr, area, view)
	return nil
}

// formatUndoPaths renders a bulleted, capped list of file paths.
func formatUndoPaths(paths []string) string {
	shown := paths
	var suffix string
	if len(paths) > maxUndoListItems {
		shown = paths[:maxUndoListItems]
		suffix = fmt.Sprintf("\n  … +%d more", len(paths)-maxUndoListItems)
	}
	lines := make([]string, len(shown))
	for i, p := range shown {
		lines[i] = "  " + ansi.Truncate(p, maxUndoPathWidth, "…")
	}
	return strings.Join(lines, "\n") + suffix
}

// formatUndoSkipped renders a bulleted, capped list of skipped files
// with their reason.
func formatUndoSkipped(files []undo.SkippedFile) string {
	shown := files
	var suffix string
	if len(files) > maxUndoListItems {
		shown = files[:maxUndoListItems]
		suffix = fmt.Sprintf("\n  … +%d more", len(files)-maxUndoListItems)
	}
	lines := make([]string, len(shown))
	for i, f := range shown {
		lines[i] = fmt.Sprintf("  %s (%s)", ansi.Truncate(f.Path, maxUndoPathWidth, "…"), f.Reason)
	}
	return strings.Join(lines, "\n") + suffix
}

// ShortHelp implements [help.KeyMap].
func (u *Undo) ShortHelp() []key.Binding {
	return []key.Binding{
		u.keyMap.LeftRight,
		u.keyMap.EnterSpace,
	}
}

// FullHelp implements [help.KeyMap].
func (u *Undo) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{u.keyMap.LeftRight, u.keyMap.EnterSpace},
		{u.keyMap.Tab, u.keyMap.Close},
	}
}
