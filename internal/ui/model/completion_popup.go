package model

import (
	"cmp"
	"context"
	"image"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/completions"
)

// completionPopup is the state of the "@" and "&" mention popup. It is
// plain state embedded in [UI], not a model of its own.
type completionPopup struct {
	completions              *completions.Completions
	completionsOpen          bool
	completionsStartIndex    int
	completionsQuery         string
	completionsTrigger       string      // the character that opened the popup
	completionsPositionStart image.Point // x,y where user typed the trigger
	// completionsRect is the screen rectangle the popup was drawn into
	// on the last Draw call, in the same coordinate space as
	// tea.MouseClickMsg. It resets to the zero Rectangle whenever the
	// popup isn't drawn, so a stale click target can never linger past
	// the frame that closed it.
	completionsRect image.Rectangle
}

// closeCompletions closes the completions popup and resets state.
func (m *UI) closeCompletions() {
	m.completionsOpen = false
	m.completionsQuery = ""
	m.completionsStartIndex = 0
	m.completionsTrigger = ""
	m.completions.Close()
}

// openCompletions arms the popup state for a trigger character typed at
// startIndex. The caller supplies the items separately, because agents come
// from config in hand while files have to be walked first.
func (m *UI) openCompletions(trigger string, startIndex int) {
	m.completionsOpen = true
	m.completionsQuery = ""
	m.completionsTrigger = trigger
	m.completionsStartIndex = startIndex
	m.completionsPositionStart = m.completionsPosition()
}

// mentionableAgents returns the agents the coder can be asked to dispatch,
// sorted by ID so the popup is stable across openings. Mentioning the
// primary agent would name the one already reading the message, and hidden
// agents back Angela's own internal calls rather than delegation. Disabled
// agents never reach the resolved map at all.
func (m *UI) mentionableAgents() []completions.AgentCompletionValue {
	cfg := m.com.Config()
	if cfg == nil {
		return nil
	}

	var agents []completions.AgentCompletionValue
	for _, agentCfg := range cfg.Agents {
		if agentCfg.Mode == config.AgentModePrimary || agentCfg.IsHidden() {
			continue
		}
		agents = append(agents, completions.AgentCompletionValue{ID: agentCfg.ID})
	}
	slices.SortFunc(agents, func(a, b completions.AgentCompletionValue) int {
		return strings.Compare(a.ID, b.ID)
	})
	return agents
}

// mentionableSkills returns the full skill catalog (unlike the "/"
// command palette, which only lists UserInvocable skills) as mention
// values, sorted by name so the popup is stable across openings.
func (m *UI) mentionableSkills() []completions.SkillCompletionValue {
	values := make([]completions.SkillCompletionValue, 0, len(m.skillEntries))
	for _, entry := range m.skillEntries {
		values = append(values, completions.SkillCompletionValue{
			Name:        entry.Name,
			Description: entry.Description,
		})
	}
	slices.SortFunc(values, func(a, b completions.SkillCompletionValue) int {
		return strings.Compare(a.Name, b.Name)
	})
	return values
}

// insertCompletionText replaces the trigger word in the textarea with the
// given text. The trigger character is part of what gets replaced, so a
// caller that wants to keep it has to include it in text.
func (m *UI) insertCompletionText(text string) bool {
	value := m.textarea.Value()
	if m.completionsStartIndex > len(value) {
		return false
	}

	word := m.textareaWord()
	endIdx := min(m.completionsStartIndex+len(word), len(value))
	newValue := value[:m.completionsStartIndex] + text + value[endIdx:]
	m.textarea.SetValue(newValue)
	m.textarea.MoveToEnd()
	m.textarea.InsertRune(' ')
	return true
}

// insertAgentCompletion writes the mention into the textarea. The "@" is
// kept: unlike a file, whose contents ride along as an attachment, the
// mention is only ever the text the coder reads.
func (m *UI) insertAgentCompletion(id string) tea.Cmd {
	prevHeight := m.textarea.Height()
	if !m.insertCompletionText("@" + id) {
		return nil
	}
	return m.handleTextareaHeightChange(prevHeight)
}

// insertSkillCompletion writes the mention into the textarea as
// "[skill:name]". Like an agent mention, this is plain text for the
// coder to read; it does not attach the skill's content the way
// selecting an invocable skill from the "/" command palette does.
func (m *UI) insertSkillCompletion(name string) tea.Cmd {
	prevHeight := m.textarea.Height()
	if !m.insertCompletionText("[skill:" + name + "]") {
		return nil
	}
	return m.handleTextareaHeightChange(prevHeight)
}

// applyCompletionSelection reacts to a message carrying a specific
// selected completion value, whichever popup kind it came from, and
// whether it arrived via keyboard (through completions.Update) or a
// mouse click on the popup (through completions.SelectAt). Other
// messages Update reacts to internally (arrow-key navigation,
// ClosedMsg) are the caller's concern; handled reports false so the
// caller leaves those alone.
func (m *UI) applyCompletionSelection(msg tea.Msg) (cmd tea.Cmd, keepOpen bool, handled bool) {
	switch msg := msg.(type) {
	case completions.SelectionMsg[completions.FileCompletionValue]:
		return m.insertFileCompletion(msg.Value.Path), msg.KeepOpen, true
	case completions.SelectionMsg[completions.ResourceCompletionValue]:
		return m.insertMCPResourceCompletion(msg.Value), msg.KeepOpen, true
	case completions.SelectionMsg[completions.AgentCompletionValue]:
		return m.insertAgentCompletion(msg.Value.ID), msg.KeepOpen, true
	case completions.SelectionMsg[completions.SkillCompletionValue]:
		return m.insertSkillCompletion(msg.Value.Name), msg.KeepOpen, true
	}
	return nil, false, false
}

// insertFileCompletion inserts the selected file path into the textarea,
// replacing the trigger word, and adds the file as an attachment.
func (m *UI) insertFileCompletion(path string) tea.Cmd {
	prevHeight := m.textarea.Height()
	if !m.insertCompletionText(path) {
		return nil
	}
	heightCmd := m.handleTextareaHeightChange(prevHeight)

	fileCmd := func() tea.Msg {
		absPath, _ := filepath.Abs(path)

		if m.hasSession() {
			// Skip attachment if file was already read and hasn't been modified.
			lastRead := m.com.Workspace.FileTrackerLastReadTime(context.Background(), m.session.ID, absPath)
			if !lastRead.IsZero() {
				if info, err := os.Stat(path); err == nil && !info.ModTime().After(lastRead) {
					return nil
				}
			}
		} else if slices.Contains(m.sessionFileReads, absPath) {
			return nil
		}

		m.sessionFileReads = append(m.sessionFileReads, absPath)

		// Add file as attachment.
		content, err := os.ReadFile(path)
		if err != nil {
			// If it fails, let the LLM handle it later.
			return nil
		}

		return message.Attachment{
			FilePath: path,
			FileName: filepath.Base(path),
			MimeType: mimeOf(content),
			Content:  content,
		}
	}
	return tea.Batch(heightCmd, fileCmd)
}

// insertMCPResourceCompletion inserts the selected resource into the textarea,
// replacing the @query, and adds the resource as an attachment.
func (m *UI) insertMCPResourceCompletion(item completions.ResourceCompletionValue) tea.Cmd {
	displayText := cmp.Or(item.Title, item.URI)

	prevHeight := m.textarea.Height()
	if !m.insertCompletionText(displayText) {
		return nil
	}
	heightCmd := m.handleTextareaHeightChange(prevHeight)

	resourceCmd := func() tea.Msg {
		contents, err := m.com.Workspace.ReadMCPResource(
			context.Background(),
			item.MCPName,
			item.URI,
		)
		if err != nil {
			slog.Warn("Failed to read MCP resource", "uri", item.URI, "error", err)
			return nil
		}
		if len(contents) == 0 {
			return nil
		}

		content := contents[0]
		var data []byte
		if content.Text != "" {
			data = []byte(content.Text)
		} else if len(content.Blob) > 0 {
			data = content.Blob
		}
		if len(data) == 0 {
			return nil
		}

		mimeType := item.MIMEType
		if mimeType == "" && content.MIMEType != "" {
			mimeType = content.MIMEType
		}
		if mimeType == "" {
			mimeType = "text/plain"
		}

		return message.Attachment{
			FilePath: item.URI,
			FileName: displayText,
			MimeType: mimeType,
			Content:  data,
		}
	}
	return tea.Batch(heightCmd, resourceCmd)
}

// completionsPosition returns the X and Y position for the completions popup.
func (m *UI) completionsPosition() image.Point {
	cur := m.textarea.Cursor()
	if cur == nil {
		return image.Point{
			X: m.layout.editor.Min.X,
			Y: m.layout.editor.Min.Y,
		}
	}
	return image.Point{
		X: cur.X + m.layout.editor.Min.X,
		Y: m.layout.editor.Min.Y + cur.Y,
	}
}

// textareaWord returns the current word at the cursor position.
func (m *UI) textareaWord() string {
	return m.textarea.Word()
}

// isWhitespace returns true if the byte is a whitespace character.
func isWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}
