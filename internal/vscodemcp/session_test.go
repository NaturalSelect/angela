package vscodemcp_test

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/vscodemcp"
	"github.com/NaturalSelect/angela/internal/vscodemcp/vscodemcptest"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// notifyEventually pushes a notification, retrying until the session's
// standalone event stream is up, since the client opens it in the
// background after connecting.
func notifyEventually(t *testing.T, srv *vscodemcptest.Server, method string, params any) {
	t.Helper()
	require.Eventually(t, func() bool {
		return srv.Notify(method, params) == nil
	}, 5*time.Second, 10*time.Millisecond)
}

func nextEvent(t *testing.T, s *vscodemcp.Session) vscodemcp.Event {
	t.Helper()
	select {
	case ev, ok := <-s.Events():
		require.True(t, ok, "events channel closed")
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no event arrived")
		return nil
	}
}

// watch opens a Session against a fresh fake server.
func watch(t *testing.T) (*vscodemcptest.Server, *vscodemcp.Session) {
	t.Helper()
	srv := vscodemcptest.Start(t, vscodemcptest.Options{})
	session, err := newClient(t, srv).Watch(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return srv, session
}

func TestSession_SelectionChanged(t *testing.T) {
	t.Parallel()
	srv, session := watch(t)

	notifyEventually(t, srv, "selection_changed", map[string]any{
		"text":     "hi",
		"filePath": "/work/a.go",
		"fileUrl":  "file:///work/a.go",
		"selection": map[string]any{
			"start":   map[string]any{"line": 0, "character": 1},
			"end":     map[string]any{"line": 0, "character": 3},
			"isEmpty": false,
		},
	})

	ev, ok := nextEvent(t, session).(vscodemcp.SelectionChanged)
	require.True(t, ok)
	require.Equal(t, "hi", ev.Text)
	require.Equal(t, "/work/a.go", ev.FilePath)
	require.Equal(t, vscodemcp.Position{Character: 1}, ev.Range.Start)
	require.Equal(t, vscodemcp.Position{Character: 3}, ev.Range.End)
}

func TestSession_DiagnosticsChanged(t *testing.T) {
	t.Parallel()
	srv, session := watch(t)

	notifyEventually(t, srv, "diagnostics_changed", map[string]any{
		"uris": []any{
			map[string]any{
				"uri": "file:///work/a.go",
				"diagnostics": []any{map[string]any{
					"message":  "bad",
					"severity": "warning",
					"range": map[string]any{
						"start": map[string]any{"line": 2, "character": 0},
						"end":   map[string]any{"line": 2, "character": 4},
					},
					"code": 12,
				}},
			},
			map[string]any{"uri": "file:///work/b.go", "diagnostics": []any{}},
		},
	})

	ev, ok := nextEvent(t, session).(vscodemcp.DiagnosticsChanged)
	require.True(t, ok)
	require.Len(t, ev.Files, 2)
	require.Equal(t, "file:///work/a.go", ev.Files[0].URI)
	require.Equal(t, vscodemcp.SeverityWarning, ev.Files[0].Diagnostics[0].Severity)
	require.Equal(t, vscodemcp.DiagnosticCode("12"), ev.Files[0].Diagnostics[0].Code)
	require.Empty(t, ev.Files[1].Diagnostics)
}

func TestSession_FileReferenceAdded(t *testing.T) {
	t.Parallel()

	t.Run("selection", func(t *testing.T) {
		t.Parallel()
		srv, session := watch(t)

		notifyEventually(t, srv, "add_file_reference", map[string]any{
			"filePath": "/work/a.go",
			"fileUrl":  "file:///work/a.go",
			"selection": map[string]any{
				"start": map[string]any{"line": 4, "character": 0},
				"end":   map[string]any{"line": 6, "character": 2},
			},
			"selectedText": "body",
		})

		ev, ok := nextEvent(t, session).(vscodemcp.FileReferenceAdded)
		require.True(t, ok)
		require.Equal(t, "/work/a.go", ev.FilePath)
		require.Equal(t, "body", ev.SelectedText)
		require.NotNil(t, ev.Range)
		require.Equal(t, 6, ev.Range.End.Line)
	})

	t.Run("whole file has no range", func(t *testing.T) {
		t.Parallel()
		srv, session := watch(t)

		notifyEventually(t, srv, "add_file_reference", map[string]any{
			"filePath":     "/work/a.go",
			"fileUrl":      "file:///work/a.go",
			"selection":    nil,
			"selectedText": nil,
		})

		ev, ok := nextEvent(t, session).(vscodemcp.FileReferenceAdded)
		require.True(t, ok)
		require.Nil(t, ev.Range)
		require.Empty(t, ev.SelectedText)
	})
}

func TestSession_UnknownAndMalformedNotificationsAreSkipped(t *testing.T) {
	t.Parallel()
	srv, session := watch(t)

	notifyEventually(t, srv, "something_new", map[string]any{"x": 1})
	require.NoError(t, srv.Notify("selection_changed", "not an object"))
	require.NoError(t, srv.Notify("add_file_reference", map[string]any{"filePath": "/work/after.go"}))

	ev, ok := nextEvent(t, session).(vscodemcp.FileReferenceAdded)
	require.True(t, ok)
	require.Equal(t, "/work/after.go", ev.FilePath)
}

func TestSession_EventsClosedOnClose(t *testing.T) {
	t.Parallel()

	srv := vscodemcptest.Start(t, vscodemcptest.Options{})
	session, err := newClient(t, srv).Watch(t.Context())
	require.NoError(t, err)

	require.NoError(t, session.Close())

	select {
	case _, ok := <-session.Events():
		require.False(t, ok)
	case <-time.After(5 * time.Second):
		t.Fatal("events channel was not closed")
	}
}

func TestSession_SlowReaderLosesOldestEvents(t *testing.T) {
	t.Parallel()

	srv := vscodemcptest.Start(t, vscodemcptest.Options{})
	session, err := newClient(t, srv).Watch(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	const total = 100
	notifyEventually(t, srv, "add_file_reference", map[string]any{"filePath": "0"})
	for i := 1; i < total; i++ {
		require.NoError(t, srv.Notify("add_file_reference", map[string]any{"filePath": strconv.Itoa(i)}))
	}

	// The last event must survive, however far behind the reader was.
	require.Eventually(t, func() bool {
		for {
			select {
			case ev := <-session.Events():
				if ref, ok := ev.(vscodemcp.FileReferenceAdded); ok && ref.FilePath == strconv.Itoa(total-1) {
					return true
				}
			default:
				return false
			}
		}
	}, 5*time.Second, 10*time.Millisecond)
}

func TestSession_SendsProcessHeaders(t *testing.T) {
	t.Parallel()

	srv := vscodemcptest.Start(t, vscodemcptest.Options{})
	session, err := newClient(t, srv).Watch(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	select {
	case h := <-srv.SessionHeaders:
		require.Equal(t, strconv.Itoa(os.Getpid()), h.Get("X-Copilot-Pid"))
		require.Equal(t, strconv.Itoa(os.Getppid()), h.Get("X-Copilot-Parent-Pid"))
		require.Equal(t, "Nonce "+srv.Nonce, h.Get("Authorization"))
	case <-time.After(5 * time.Second):
		t.Fatal("no session was started")
	}
}

func TestSession_Tools(t *testing.T) {
	t.Parallel()

	renamed := make(chan string, 1)
	srv := vscodemcptest.Start(t, vscodemcptest.Options{
		Tools: map[string]toolHandler{
			"update_session_name": func(_ context.Context, args json.RawMessage) *mcp.CallToolResult {
				var in struct {
					Name string `json:"name"`
				}
				_ = json.Unmarshal(args, &in)
				renamed <- in.Name
				return textResult(`{"success": true}`)
			},
			"get_vscode_info": func(context.Context, json.RawMessage) *mcp.CallToolResult {
				return textResult(`{"version": "1.99.0"}`)
			},
			"get_selection": func(context.Context, json.RawMessage) *mcp.CallToolResult {
				return textResult("null")
			},
			"get_diagnostics": func(context.Context, json.RawMessage) *mcp.CallToolResult {
				return textResult("[]")
			},
		},
	})
	session, err := newClient(t, srv).Watch(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	require.NoError(t, session.SetName(t.Context(), "angela: fix the bug"))
	require.Equal(t, "angela: fix the bug", <-renamed)

	info, err := session.Info(t.Context())
	require.NoError(t, err)
	require.Equal(t, "1.99.0", info.Version)

	sel, err := session.Selection(t.Context())
	require.NoError(t, err)
	require.Nil(t, sel)

	files, err := session.Diagnostics(t.Context(), "")
	require.NoError(t, err)
	require.Empty(t, files)
}

func TestSession_SetName_Failure(t *testing.T) {
	t.Parallel()

	srv := serverWithTool(t, "update_session_name", func(context.Context, json.RawMessage) *mcp.CallToolResult {
		return textResult(`{"success": false}`)
	})
	session, err := newClient(t, srv).Watch(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	require.Error(t, session.SetName(t.Context(), "x"))
}
