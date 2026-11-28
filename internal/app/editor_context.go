package app

import (
	"github.com/NaturalSelect/angela/internal/editorcontext"
	"github.com/NaturalSelect/angela/internal/reminder"
)

// agentEditorContext hands the agent what the user's VS Code window shows. It
// returns nil when the window is not being followed, which leaves the editor
// out of the conversation.
func (app *App) agentEditorContext() func() (reminder.EditorContext, bool) {
	if app.editorContext == nil {
		return nil
	}
	return func() (reminder.EditorContext, bool) {
		return reminderEditor(app.editorContext.Snapshot())
	}
}

// reminderEditor reports false while there is no window to describe or no
// file in it.
func reminderEditor(snap editorcontext.Snapshot) (reminder.EditorContext, bool) {
	if !snap.Connected || snap.ActiveFile == "" {
		return reminder.EditorContext{}, false
	}
	editor := reminder.EditorContext{ActiveFile: snap.ActiveFile, RecentFiles: snap.RecentFiles}
	if sel := snap.Selection; sel != nil {
		editor.Selection = &reminder.EditorSelection{StartLine: sel.StartLine, EndLine: sel.EndLine, Text: sel.Text}
	}
	return editor, true
}
