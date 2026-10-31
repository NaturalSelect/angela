package model

import (
	"context"
	"image"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/list"
	"github.com/NaturalSelect/angela/internal/ui/util"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// mode. Narrow terminals get the bare prompt without the hint tail.
func (m *UI) editorPlaceholder() string {
	if m.bangMode {
		return "Run a shell command"
	}
	switch m.permissionModeCached() {
	case permission.ModeYolo:
		return "Yolo mode — permissions are skipped"
	case permission.ModeAutoAcceptEdits:
		return "Auto-accepting edits — other actions still ask"
	}
	if m.width < narrowWidthBreakpoint {
		return "Ask anything…"
	}
	return "Ask anything — / for commands, @ for agents, # for files, & for skills"
}

// editorCaption returns the one-line run context: which agent and model the
// next message will go to, the reasoning effort when the model has one, and
// the input mode when it is not the ordinary one. The text is unstyled; the
// caller owns how it is drawn.
func (m *UI) editorCaption(width int) string {
	if width <= 0 {
		return ""
	}

	var modelName, agentName, effort string
	if active := m.activeAgent(); active != nil {
		modelName = active.CatwalkCfg.Name
		agentName = active.AgentName
		// A graduated effort scale is worth naming; a plain think
		// on/off toggle already has nowhere near this much nuance and
		// belongs to the sidebar's fuller model info instead.
		if active.CatwalkCfg.CanReason && len(active.CatwalkCfg.ReasoningLevels) > 0 {
			effort = common.FormatReasoningEffort(active.CatwalkCfg.DefaultReasoningEffort)
		}
	}

	var mode string
	switch {
	case m.bangMode:
		mode = "shell"
	case m.permissionModeCached() == permission.ModeYolo:
		mode = "yolo"
	case m.permissionModeCached() == permission.ModeAutoAcceptEdits:
		mode = "auto-accept"
	}

	// Narrow terminals keep only what identifies the run: the model, and
	// the mode when it is escalated.
	fields := []string{agentName, modelName, effort, mode}
	if width < narrowWidthBreakpoint {
		fields = []string{modelName, mode}
	}

	var parts []string
	for _, f := range fields {
		if f != "" {
			parts = append(parts, f)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return ansi.Truncate(strings.Join(parts, " · "), width, "…")
}

// editorBorderStyle is the box border color. It carries the input mode, which
// is why the gutter no longer needs an accent rail.
func (m *UI) editorBorderStyle() lipgloss.Style {
	t := m.com.Styles
	switch {
	case m.bangMode:
		return t.Editor.RailBang
	case m.permissionModeCached() == permission.ModeYolo:
		if m.sandboxActive {
			return t.Editor.RailYoloSandbox
		}
		return t.Editor.RailYolo
	case m.permissionModeCached() == permission.ModeAutoAcceptEdits:
		return t.Editor.RailAutoAcceptEdits
	case m.focus == uiFocusEditor:
		return t.Editor.BorderFocused
	default:
		return t.Editor.Border
	}
}

// drawPromptBox draws the input as a bordered box with the attachments strip
// above it and the run context inset into its bottom border.
func (m *UI) drawPromptBox(scr uv.Screen, area uv.Rectangle) {
	if area.Dx() <= editorBoxBorders || area.Dy() <= editorBoxBorders {
		return
	}

	if len(m.attachments.List()) > 0 {
		uv.NewStyledString(m.attachments.Render(area.Dx())).Draw(scr, image.Rect(
			area.Min.X, area.Min.Y, area.Max.X, area.Min.Y+1,
		))
	}

	// The box occupies the band below the attachments row.
	box := image.Rect(area.Min.X, area.Min.Y+1, area.Max.X, area.Max.Y)
	top, bottom, right := box.Min.Y, box.Max.Y-1, box.Max.X-1
	span := strings.Repeat("─", box.Dx()-editorBoxBorders)
	border := list.ToStyle(m.editorBorderStyle())

	common.SetSpan(scr, box.Min.X, top, border, "╭"+span+"╮")
	common.SetSpan(scr, box.Min.X, bottom, border, "╰"+span+"╯")
	for y := top + 1; y < bottom; y++ {
		common.SetSpan(scr, box.Min.X, y, border, "│")
		common.SetSpan(scr, right, y, border, "│")
	}

	content := m.textarea.View()
	if m.viewingSubAgent() && !m.textarea.Focused() {
		content = m.subAgentNotice(box.Dx() - editorBoxBorders)
	}
	uv.NewStyledString(content).Draw(scr, image.Rect(
		box.Min.X+1, top+1, right, bottom,
	))

	m.drawPromptLabel(scr, box, bottom)
}

// subAgentNotice replaces the prompt while a sub-agent's transcript is on
// screen and the editor is not in use, so the box reads as a closed door
// rather than an empty one. Tabbing or clicking in still opens it: a
// command or a shell line is a local action, not a message queued behind
// the parent's back, so only sendMessage needs to refuse those.
func (m *UI) subAgentNotice(width int) string {
	notice := "Sub-agent transcript · read only · ctrl+up to parent · tab for commands"
	return m.com.Styles.Editor.Caption.Render(ansi.Truncate(notice, width, "…"))
}

// drawPromptLabel writes the run context onto the bottom border row. The
// spaces the label pads itself with overwrite the border glyphs, so it reads
// as inset into the border rather than printed over it.
func (m *UI) drawPromptLabel(scr uv.Screen, box uv.Rectangle, row int) {
	// Two columns of border plus the two spaces the label pads itself with.
	caption := m.editorCaption(box.Dx() - 4)
	if caption == "" {
		return
	}
	label := " " + caption + " "
	x := box.Max.X - 1 - ansi.StringWidth(label)
	if x <= box.Min.X {
		return
	}
	common.SetSpan(scr, x, row, list.ToStyle(m.com.Styles.Editor.BottomLabel), label)
}

// attachSkill reads a skill's content by ID and returns it as a markdown
// attachment to be added to the attachment toolbar. The user can then
// compose a message and send it with the skill attached.
// The name parameter is used as a fallback when the skill does not
// report one.
func (m *UI) attachSkill(skillID, name string) tea.Cmd {
	return func() tea.Msg {
		content, result, err := m.com.Workspace.ReadSkill(context.Background(), skillID)
		if err != nil {
			return util.NewErrorMsg(err)
		}
		fileName := result.Name
		if fileName == "" {
			fileName = name
		}
		return message.Attachment{
			FilePath: fileName,
			FileName: fileName,
			MimeType: "text/markdown",
			Content:  content,
		}
	}
}
