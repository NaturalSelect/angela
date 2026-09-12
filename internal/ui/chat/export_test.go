package chat

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/reminder"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
)

// TestSessionToMarkdown_RendersExpectedSectionsAndExcludesReminders builds a
// small session covering a plain user message, an assistant message with a
// tool call, the separate Tool-role message carrying that call's result, and
// a reminder disguised as a user message. It asserts the document contains
// the expected headers and sections and never leaks the reminder's text, and
// that no assets directory is created when nothing binary was exported.
func TestSessionToMarkdown_RendersExpectedSectionsAndExcludesReminders(t *testing.T) {
	t.Parallel()

	sess := session.Session{
		ID:        "sess-1",
		Title:     "Demo Session",
		Agent:     "coder",
		CreatedAt: 1700000000,
		UpdatedAt: 1700000100,
	}

	messages := []message.Message{
		{
			ID:        "msg-user",
			Role:      message.User,
			SessionID: sess.ID,
			Parts: []message.ContentPart{
				message.TextContent{Text: "Please add a greeting function."},
			},
		},
		{
			ID:        "msg-reminder",
			Role:      message.User,
			SessionID: sess.ID,
			Parts: []message.ContentPart{
				message.TextContent{Text: reminder.Wrap("SECRET_INTERNAL_REMINDER_TEXT")},
				message.Finish{Reason: message.FinishReasonEndTurn},
			},
		},
		{
			ID:        "msg-assistant",
			Role:      message.Assistant,
			SessionID: sess.ID,
			Parts: []message.ContentPart{
				message.TextContent{Text: "Sure thing, running a command."},
				message.ToolCall{ID: "call-1", Name: toolnames.Bash, Input: `{"command":"echo hi"}`, Finished: true},
			},
		},
		{
			ID:        "msg-tool-result",
			Role:      message.Tool,
			SessionID: sess.ID,
			Parts: []message.ContentPart{
				message.ToolResult{ToolCallID: "call-1", Name: toolnames.Bash, Content: "hi"},
			},
		},
	}

	assetsDir := filepath.Join(t.TempDir(), "demo-session.assets")
	doc, err := SessionToMarkdown(sess, messages, assetsDir)
	require.NoError(t, err)

	// Document header.
	require.Contains(t, doc, "# Demo Session")
	require.Contains(t, doc, "- **Session ID:** sess-1")
	require.Contains(t, doc, "- **Agent:** coder")

	// Plain user text under its own heading.
	require.Contains(t, doc, "## User")
	require.Contains(t, doc, "Please add a greeting function.")

	// Assistant text plus its paired tool call/result, reusing the same
	// clipboard-copy formatting helpers the in-app "copy tool" keybind uses.
	require.Contains(t, doc, "## Assistant")
	require.Contains(t, doc, "Sure thing, running a command.")
	require.Contains(t, doc, "### Tool: Bash")
	require.Contains(t, doc, "**Command:** echo hi")
	require.Contains(t, doc, "```bash\nhi\n```")

	// The reminder is filtered out entirely: neither its wrapper tag nor
	// its payload text should appear anywhere in the document.
	require.NotContains(t, doc, "SECRET_INTERNAL_REMINDER_TEXT")
	require.NotContains(t, doc, "<system-reminder>")

	// Nothing binary was exported, so the assets directory must not
	// have been created.
	require.NoDirExists(t, assetsDir)
}

// TestSessionToMarkdown_SkipsMessagesWithNoRenderableContent verifies an
// assistant message that carries nothing but bookkeeping (no text, no
// thinking, no tool calls) produces no section at all.
func TestSessionToMarkdown_SkipsMessagesWithNoRenderableContent(t *testing.T) {
	t.Parallel()

	sess := session.Session{ID: "sess-empty", Title: "Empty"}
	messages := []message.Message{
		{
			ID:   "msg-empty",
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.Finish{Reason: message.FinishReasonEndTurn},
			},
		},
	}

	doc, err := SessionToMarkdown(sess, messages, filepath.Join(t.TempDir(), "empty.assets"))
	require.NoError(t, err)
	require.NotContains(t, doc, "## Assistant")
}

// TestSessionToMarkdown_WritesBinaryAttachmentAsset verifies a BinaryContent
// attachment (e.g. a pasted image) is written to disk under assetsDir and
// linked from the document with a relative path.
func TestSessionToMarkdown_WritesBinaryAttachmentAsset(t *testing.T) {
	t.Parallel()

	sess := session.Session{ID: "sess-2", Title: "With Attachment"}
	imgBytes := []byte("fake-png-bytes")
	messages := []message.Message{
		{
			ID:   "msg-user",
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "Here's a screenshot."},
				message.BinaryContent{Path: "screenshot.png", MIMEType: "image/png", Data: imgBytes},
			},
		},
	}

	assetsDir := filepath.Join(t.TempDir(), "with-attachment.assets")
	doc, err := SessionToMarkdown(sess, messages, assetsDir)
	require.NoError(t, err)

	wantLink := "with-attachment.assets/001-screenshot.png"
	require.Contains(t, doc, "![001-screenshot.png]("+wantLink+")")

	data, err := os.ReadFile(filepath.Join(assetsDir, "001-screenshot.png"))
	require.NoError(t, err)
	require.Equal(t, imgBytes, data)
}

// TestSessionToMarkdown_WritesToolResultImageAsset verifies a tool result
// carrying base64 binary Data (e.g. a screenshot tool) is decoded and
// written out as a real file rather than left as a "[Image: ...]"
// clipboard-style placeholder.
func TestSessionToMarkdown_WritesToolResultImageAsset(t *testing.T) {
	t.Parallel()

	sess := session.Session{ID: "sess-3", Title: "Tool Image"}
	imgBytes := []byte("fake-image-bytes")
	messages := []message.Message{
		{
			ID:   "msg-assistant",
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.ToolCall{ID: "call-1", Name: toolnames.Download, Input: `{"url":"https://example.com/x.png"}`, Finished: true},
			},
		},
		{
			ID:   "msg-tool-result",
			Role: message.Tool,
			Parts: []message.ContentPart{
				message.ToolResult{
					ToolCallID: "call-1",
					Name:       toolnames.Download,
					Data:       base64.StdEncoding.EncodeToString(imgBytes),
					MIMEType:   "image/png",
				},
			},
		},
	}

	assetsDir := filepath.Join(t.TempDir(), "tool-image.assets")
	doc, err := SessionToMarkdown(sess, messages, assetsDir)
	require.NoError(t, err)

	wantLink := "tool-image.assets/001-" + toolnames.Download + ".png"
	require.Contains(t, doc, wantLink)
	require.NotContains(t, doc, "[Image: image/png]")

	data, err := os.ReadFile(filepath.Join(assetsDir, "001-"+toolnames.Download+".png"))
	require.NoError(t, err)
	require.Equal(t, imgBytes, data)
}

func TestSessionExportSlug(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		title     string
		sessionID string
		want      string
	}{
		{"simple title", "Fix the login bug", "sess-1", "fix-the-login-bug"},
		{"punctuation collapses to hyphens", "Wait... what?!", "sess-2", "wait-what"},
		{"empty title falls back to session id", "", "Sess-ABC-123", "sess-abc-123"},
		{"title with no safe characters falls back", "!!!", "sess-3", "sess-3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, SessionExportSlug(tt.title, tt.sessionID))
		})
	}
}

func TestAssetsDirName(t *testing.T) {
	t.Parallel()

	require.Equal(t, "notes.assets", AssetsDirName("notes.md"))
	require.Equal(t, filepath.Join("out", "notes.assets"), AssetsDirName(filepath.Join("out", "notes.md")))
}

// TestUniqueExportPath verifies a colliding path gets a numeric suffix
// instead of silently overwriting the existing file, and that repeated
// collisions keep incrementing the suffix.
func TestUniqueExportPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "session.md")

	// Nothing on disk yet: the path is returned unchanged.
	got, err := UniqueExportPath(target)
	require.NoError(t, err)
	require.Equal(t, target, got)

	// One collision: falls back to a "-2" suffix.
	require.NoError(t, os.WriteFile(target, []byte("existing"), 0o644))
	got, err = UniqueExportPath(target)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "session-2.md"), got)

	// Two collisions: skips to "-3".
	require.NoError(t, os.WriteFile(filepath.Join(dir, "session-2.md"), []byte("existing"), 0o644))
	got, err = UniqueExportPath(target)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "session-3.md"), got)
}
