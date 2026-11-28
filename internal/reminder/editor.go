package reminder

import (
	_ "embed"
	"slices"
	"strings"
	"text/template"
	"unicode/utf8"
)

const (
	// maxSelectionLines and maxSelectionRunes cap how much of a selection
	// reaches the model, whichever is hit first. The model can read the
	// file for the rest, and the notice repeats whenever the editor
	// state changes.
	maxSelectionLines = 50
	maxSelectionRunes = 2000

	// editorNoticeHeading opens every editor notice. Recognizing a
	// persisted notice by it is what lets the agent compare a new one
	// against the last one sent.
	editorNoticeHeading = "The user's VS Code editor as of the message that follows:"
)

//go:embed templates/vscode_context.md.tpl
var editorContextTemplate string

var editorContextTmpl = template.Must(template.New("vscode_context").Parse(editorContextTemplate))

// EditorContext is what the user's VS Code window shows. Paths are written
// the way the model should see them.
type EditorContext struct {
	ActiveFile string
	// Selection is nil while the editor holds a bare cursor.
	Selection *EditorSelection
	// RecentFiles lists the files that were active, newest first.
	RecentFiles []string
}

// EditorSelection is the text selected in the active file.
type EditorSelection struct {
	// StartLine and EndLine are 1-based and inclusive.
	StartLine, EndLine int
	Text               string
}

type editorNoticeView struct {
	Heading     string
	ActiveFile  string
	RecentFiles []string
	Selection   *selectionView
}

type selectionView struct {
	StartLine, EndLine int
	Text               string
	Fence              string
	Truncated          bool
}

// EditorNotice tells the model what the user is looking at in VS Code.
// Without it the model cannot resolve "this function" or "the selected
// code" without asking.
//
// Unlike the sources in DefaultSources, it is not collected every turn: the
// agent asks for it when a user message starts, and it returns "" when the
// notice would repeat previous, the text of the last one sent, so a long
// stretch in one file costs tokens once. It also returns "" when the editor
// shows no file.
func EditorNotice(editor EditorContext, previous string) string {
	if editor.ActiveFile == "" {
		return ""
	}
	var out strings.Builder
	if err := editorContextTmpl.Execute(&out, newEditorNoticeView(editor)); err != nil {
		return ""
	}
	text := strings.TrimSpace(out.String())
	if text == previous {
		return ""
	}
	return text
}

// PersistedEditorNotice returns the text of an editor notice from the
// text of a persisted reminder. It reports false for any other reminder.
func PersistedEditorNotice(persisted string) (string, bool) {
	if !IsWrapped(persisted) {
		return "", false
	}
	text := strings.TrimSpace(persisted)
	text = strings.TrimPrefix(text, "<"+Tag+">")
	text = strings.TrimSuffix(text, "</"+Tag+">")
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, editorNoticeHeading) {
		return "", false
	}
	return text, true
}

func newEditorNoticeView(editor EditorContext) editorNoticeView {
	// File names and the selected text come from the user's repository,
	// which cannot be trusted to stay inside the reminder block.
	recent := slices.DeleteFunc(slices.Clone(editor.RecentFiles), func(file string) bool {
		return file == editor.ActiveFile
	})
	view := editorNoticeView{
		Heading:     editorNoticeHeading,
		ActiveFile:  Escape(editor.ActiveFile),
		RecentFiles: escapeAll(recent),
	}
	if editor.Selection != nil {
		view.Selection = newSelectionView(*editor.Selection)
	}
	return view
}

func newSelectionView(sel EditorSelection) *selectionView {
	lines := strings.Split(strings.TrimSuffix(sel.Text, "\n"), "\n")
	shown := lines[:min(len(lines), maxSelectionLines)]
	text := strings.Join(shown, "\n")
	truncated := len(shown) < len(lines)
	if utf8.RuneCountInString(text) > maxSelectionRunes {
		text = string([]rune(text)[:maxSelectionRunes])
		truncated = true
	}
	text = Escape(text)
	return &selectionView{
		StartLine: sel.StartLine,
		EndLine:   sel.EndLine,
		Text:      text,
		Fence:     fenceFor(text),
		Truncated: truncated,
	}
}

// fenceFor returns a code fence longer than any run of backticks in text,
// so the text cannot close the block early.
func fenceFor(text string) string {
	longest, run := 0, 0
	for _, r := range text {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}
