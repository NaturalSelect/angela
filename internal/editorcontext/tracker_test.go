package editorcontext_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/editorcontext"
	"github.com/NaturalSelect/angela/internal/vscodemcp/vscodemcptest"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type fixture struct {
	srv        *vscodemcptest.Server
	tracker    *editorcontext.Tracker
	workingDir string
}

// startTracker follows a fake VS Code window. seed, when set, answers the
// get_selection query the tracker makes on connecting, given the working
// directory the window has open.
func startTracker(t *testing.T, termProgram string, seed func(workingDir string) string) *fixture {
	t.Helper()

	f := &fixture{}
	f.srv = vscodemcptest.Start(t, vscodemcptest.Options{
		Tools: map[string]func(context.Context, json.RawMessage) *mcp.CallToolResult{
			"get_selection": func(context.Context, json.RawMessage) *mcp.CallToolResult {
				text := "null"
				if seed != nil {
					text = seed(f.workingDir)
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
			},
		},
	})
	copilotHome, workingDir := f.srv.Install(t)
	f.workingDir = workingDir

	f.tracker = editorcontext.New(vscodemcptest.Getenv(copilotHome, termProgram), workingDir)
	f.tracker.Start(t.Context())
	t.Cleanup(f.tracker.Close)
	return f
}

func (f *fixture) path(elem ...string) string {
	return filepath.Join(append([]string{f.workingDir}, elem...)...)
}

// selectionChanged pushes a selection_changed notification, retrying
// until the tracker's event stream is up.
func (f *fixture) selectionChanged(t *testing.T, params map[string]any) {
	t.Helper()
	require.Eventually(t, func() bool {
		return f.srv.Notify("selection_changed", params) == nil
	}, 5*time.Second, 10*time.Millisecond)
}

func (f *fixture) waitFor(t *testing.T, done func(editorcontext.Snapshot) bool) editorcontext.Snapshot {
	t.Helper()
	var snap editorcontext.Snapshot
	require.Eventually(t, func() bool {
		snap = f.tracker.Snapshot()
		return done(snap)
	}, 5*time.Second, 10*time.Millisecond)
	return snap
}

func params(path string, startLine, startChar, endLine, endChar int, text string) map[string]any {
	return map[string]any{
		"text":     text,
		"filePath": path,
		"fileUrl":  "file://" + filepath.ToSlash(path),
		"selection": map[string]any{
			"start":   map[string]any{"line": startLine, "character": startChar},
			"end":     map[string]any{"line": endLine, "character": endChar},
			"isEmpty": text == "",
		},
	}
}

func connected(snap editorcontext.Snapshot) bool { return snap.Connected }

func activeFile(name string) func(editorcontext.Snapshot) bool {
	return func(snap editorcontext.Snapshot) bool { return snap.ActiveFile == name }
}

func TestTracker_SeedsFromTheActiveEditor(t *testing.T) {
	t.Parallel()

	t.Run("current selection", func(t *testing.T) {
		t.Parallel()
		f := startTracker(t, "vscode", func(workingDir string) string {
			p := params(filepath.Join(workingDir, "pkg", "a.go"), 2, 0, 4, 5, "body")
			p["current"] = true
			raw, err := json.Marshal(p)
			require.NoError(t, err)
			return string(raw)
		})

		snap := f.waitFor(t, activeFile(filepath.Join("pkg", "a.go")))
		require.Equal(t, editorcontext.Snapshot{
			Connected:   true,
			ActiveFile:  filepath.Join("pkg", "a.go"),
			Selection:   &editorcontext.Selection{StartLine: 3, EndLine: 5, Text: "body"},
			RecentFiles: []string{filepath.Join("pkg", "a.go")},
		}, snap)
	})

	t.Run("replayed selection is not the active editor", func(t *testing.T) {
		t.Parallel()
		f := startTracker(t, "vscode", func(workingDir string) string {
			p := params(filepath.Join(workingDir, "a.go"), 0, 0, 0, 3, "old")
			p["current"] = false
			raw, err := json.Marshal(p)
			require.NoError(t, err)
			return string(raw)
		})

		snap := f.waitFor(t, connected)
		require.Empty(t, snap.ActiveFile)
		require.Nil(t, snap.Selection)
		require.Empty(t, snap.RecentFiles)
	})
}

func TestTracker_FollowsSelectionChanges(t *testing.T) {
	t.Parallel()

	t.Run("selection inside the working directory", func(t *testing.T) {
		t.Parallel()
		f := startTracker(t, "vscode", nil)

		f.selectionChanged(t, params(f.path("pkg", "a.go"), 9, 4, 11, 7, "some\ntext"))

		snap := f.waitFor(t, activeFile(filepath.Join("pkg", "a.go")))
		require.True(t, snap.Connected)
		require.Equal(t, &editorcontext.Selection{StartLine: 10, EndLine: 12, Text: "some\ntext"}, snap.Selection)
	})

	t.Run("whole lines end on the last selected line", func(t *testing.T) {
		t.Parallel()
		f := startTracker(t, "vscode", nil)

		f.selectionChanged(t, params(f.path("a.go"), 2, 0, 5, 0, "l3\nl4\nl5\n"))

		snap := f.waitFor(t, activeFile("a.go"))
		require.Equal(t, &editorcontext.Selection{StartLine: 3, EndLine: 5, Text: "l3\nl4\nl5\n"}, snap.Selection)
	})

	t.Run("a bare cursor keeps the file and drops the selection", func(t *testing.T) {
		t.Parallel()
		f := startTracker(t, "vscode", nil)

		f.selectionChanged(t, params(f.path("a.go"), 0, 0, 0, 2, "ab"))
		f.waitFor(t, func(snap editorcontext.Snapshot) bool { return snap.Selection != nil })
		f.selectionChanged(t, params(f.path("a.go"), 3, 1, 3, 1, ""))

		snap := f.waitFor(t, func(snap editorcontext.Snapshot) bool { return snap.Selection == nil })
		require.Equal(t, "a.go", snap.ActiveFile)
	})

	t.Run("a file outside the working directory stays absolute", func(t *testing.T) {
		t.Parallel()
		f := startTracker(t, "vscode", nil)
		outside := filepath.Join(t.TempDir(), "b.go")

		f.selectionChanged(t, params(outside, 0, 0, 0, 1, "x"))

		f.waitFor(t, activeFile(outside))
	})

	t.Run("editors that are not files are ignored", func(t *testing.T) {
		t.Parallel()
		f := startTracker(t, "vscode", nil)

		untitled := params("Untitled-1", 0, 0, 0, 1, "x")
		untitled["fileUrl"] = "untitled:Untitled-1"
		f.selectionChanged(t, untitled)
		f.selectionChanged(t, params(f.path("a.go"), 0, 0, 0, 1, "y"))

		snap := f.waitFor(t, activeFile("a.go"))
		require.Equal(t, []string{"a.go"}, snap.RecentFiles)
	})
}

func TestTracker_RecentFiles(t *testing.T) {
	t.Parallel()
	f := startTracker(t, "vscode", nil)

	for _, name := range []string{"1.go", "2.go", "3.go", "4.go", "5.go", "6.go"} {
		f.selectionChanged(t, params(f.path(name), 0, 0, 0, 1, "x"))
	}
	snap := f.waitFor(t, activeFile("6.go"))
	require.Equal(t, []string{"6.go", "5.go", "4.go", "3.go", "2.go"}, snap.RecentFiles, "the oldest file is dropped past five")

	f.selectionChanged(t, params(f.path("4.go"), 0, 0, 0, 1, "x"))
	snap = f.waitFor(t, activeFile("4.go"))
	require.Equal(t, []string{"4.go", "6.go", "5.go", "3.go", "2.go"}, snap.RecentFiles, "a revisited file moves to the front once")
}

func TestTracker_PublishesChanges(t *testing.T) {
	t.Parallel()
	f := startTracker(t, "vscode", nil)
	events := f.tracker.Subscribe(t.Context())

	f.selectionChanged(t, params(f.path("a.go"), 0, 0, 0, 1, "x"))

	require.Eventually(t, func() bool {
		select {
		case ev := <-events:
			return ev.Payload.ActiveFile == "a.go"
		default:
			return false
		}
	}, 5*time.Second, 10*time.Millisecond)
}

func TestTracker_StaysIdleOutsideTheIntegratedTerminal(t *testing.T) {
	t.Parallel()
	f := startTracker(t, "", nil)

	require.Never(t, func() bool {
		select {
		case <-f.srv.SessionHeaders:
			return true
		default:
			return f.tracker.Snapshot().Connected
		}
	}, 300*time.Millisecond, 20*time.Millisecond)
}
