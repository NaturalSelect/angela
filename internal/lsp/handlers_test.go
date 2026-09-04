package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	powernap "github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/stretchr/testify/require"
)

func TestHandleWorkspaceConfiguration(t *testing.T) {
	t.Parallel()

	result, err := HandleWorkspaceConfiguration(context.Background(), "workspace/configuration", json.RawMessage(`{"items":[{}]}`))
	require.NoError(t, err)
	require.Equal(t, []map[string]any{{}}, result)
}

func TestHandleWorkDoneProgressCreate(t *testing.T) {
	t.Parallel()

	result, err := HandleWorkDoneProgressCreate(context.Background(), "window/workDoneProgress/create", json.RawMessage(`{"token":"1"}`))
	require.NoError(t, err)
	require.Nil(t, result)
}

// TestHandleRegisterCapability mutates the package-level fileWatchHandler,
// so it cannot run in parallel with itself or other tests touching it.
func TestHandleRegisterCapability(t *testing.T) {
	t.Cleanup(func() { RegisterFileWatchHandler(nil) })

	t.Run("invalid json returns error", func(t *testing.T) {
		RegisterFileWatchHandler(nil)
		result, err := HandleRegisterCapability(context.Background(), "client/registerCapability", json.RawMessage(`{invalid`))
		require.Error(t, err)
		require.Nil(t, result)
	})

	t.Run("unrelated method does not notify handler", func(t *testing.T) {
		var called bool
		RegisterFileWatchHandler(func(string, []protocol.FileSystemWatcher) {
			called = true
		})
		params := json.RawMessage(`{"registrations":[{"id":"r1","method":"textDocument/somethingElse"}]}`)
		result, err := HandleRegisterCapability(context.Background(), "client/registerCapability", params)
		require.NoError(t, err)
		require.Nil(t, result)
		require.False(t, called)
	})

	t.Run("didChangeWatchedFiles registers watchers", func(t *testing.T) {
		var gotID string
		var gotWatchers []protocol.FileSystemWatcher
		RegisterFileWatchHandler(func(id string, watchers []protocol.FileSystemWatcher) {
			gotID = id
			gotWatchers = watchers
		})
		params := json.RawMessage(`{
			"registrations": [
				{
					"id": "watch-1",
					"method": "workspace/didChangeWatchedFiles",
					"registerOptions": {
						"watchers": [
							{"globPattern": "**/*.go"}
						]
					}
				}
			]
		}`)
		result, err := HandleRegisterCapability(context.Background(), "client/registerCapability", params)
		require.NoError(t, err)
		require.Nil(t, result)
		require.Equal(t, "watch-1", gotID)
		require.Len(t, gotWatchers, 1)
	})

	t.Run("malformed register options are skipped without notifying", func(t *testing.T) {
		var called bool
		RegisterFileWatchHandler(func(string, []protocol.FileSystemWatcher) {
			called = true
		})
		// registerOptions.watchers is a string instead of an array, so
		// unmarshaling into DidChangeWatchedFilesRegistrationOptions fails
		// and the registration is skipped.
		params := json.RawMessage(`{
			"registrations": [
				{
					"id": "watch-2",
					"method": "workspace/didChangeWatchedFiles",
					"registerOptions": {"watchers": "not-an-array"}
				}
			]
		}`)
		result, err := HandleRegisterCapability(context.Background(), "client/registerCapability", params)
		require.NoError(t, err)
		require.Nil(t, result)
		require.False(t, called)
	})
}

// TestNotifyFileWatchRegistration_NilHandler pins that notifying without a
// registered handler is a safe no-op.
func TestNotifyFileWatchRegistration_NilHandler(t *testing.T) {
	RegisterFileWatchHandler(nil)
	t.Cleanup(func() { RegisterFileWatchHandler(nil) })

	notifyFileWatchRegistration("some-id", []protocol.FileSystemWatcher{})
}

func TestHandleApplyEdit(t *testing.T) {
	t.Parallel()

	handler := HandleApplyEdit(powernap.UTF16)

	t.Run("invalid json returns error", func(t *testing.T) {
		t.Parallel()
		result, err := handler(context.Background(), "workspace/applyEdit", json.RawMessage(`{invalid`))
		require.Error(t, err)
		require.Nil(t, result)
	})

	t.Run("edit application failure reports Applied false", func(t *testing.T) {
		t.Parallel()
		missing := filepath.Join(t.TempDir(), "does-not-exist.go")
		uri := protocol.URIFromPath(missing)
		params, err := json.Marshal(protocol.ApplyWorkspaceEditParams{
			Edit: protocol.WorkspaceEdit{
				Changes: map[protocol.DocumentURI][]protocol.TextEdit{
					uri: {{
						Range:   protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 0}},
						NewText: "x",
					}},
				},
			},
		})
		require.NoError(t, err)

		result, err := handler(context.Background(), "workspace/applyEdit", params)
		require.NoError(t, err)
		applyResult, ok := result.(protocol.ApplyWorkspaceEditResult)
		require.True(t, ok)
		require.False(t, applyResult.Applied)
		require.NotEmpty(t, applyResult.FailureReason)
	})

	t.Run("edit application succeeds and writes file", func(t *testing.T) {
		t.Parallel()
		file := filepath.Join(t.TempDir(), "sample.go")
		require.NoError(t, os.WriteFile(file, []byte("package main\n"), 0o644))
		uri := protocol.URIFromPath(file)

		params, err := json.Marshal(protocol.ApplyWorkspaceEditParams{
			Edit: protocol.WorkspaceEdit{
				Changes: map[protocol.DocumentURI][]protocol.TextEdit{
					uri: {{
						Range:   protocol.Range{Start: protocol.Position{Line: 0, Character: 8}, End: protocol.Position{Line: 0, Character: 12}},
						NewText: "other",
					}},
				},
			},
		})
		require.NoError(t, err)

		result, err := handler(context.Background(), "workspace/applyEdit", params)
		require.NoError(t, err)
		applyResult, ok := result.(protocol.ApplyWorkspaceEditResult)
		require.True(t, ok)
		require.True(t, applyResult.Applied)
		require.Empty(t, applyResult.FailureReason)

		content, err := os.ReadFile(file)
		require.NoError(t, err)
		require.Equal(t, "package other\n", string(content))
	})
}

// TestHandleServerMessage mutates the process-global slog default logger to
// capture output, so it cannot run in parallel.
func TestHandleServerMessage(t *testing.T) {
	tests := []struct {
		name      string
		params    json.RawMessage
		wantLevel string
		wantMsg   string
	}{
		{
			name:      "error message",
			params:    json.RawMessage(`{"type":1,"message":"boom"}`),
			wantLevel: "ERROR",
			wantMsg:   "boom",
		},
		{
			name:      "warning message",
			params:    json.RawMessage(`{"type":2,"message":"careful"}`),
			wantLevel: "WARN",
			wantMsg:   "careful",
		},
		{
			name:      "info message",
			params:    json.RawMessage(`{"type":3,"message":"fyi"}`),
			wantLevel: "INFO",
			wantMsg:   "fyi",
		},
		{
			name:      "log message",
			params:    json.RawMessage(`{"type":4,"message":"trace"}`),
			wantLevel: "DEBUG",
			wantMsg:   "trace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
			t.Cleanup(func() { slog.SetDefault(prev) })

			HandleServerMessage(context.Background(), "window/showMessage", tt.params)

			out := buf.String()
			require.Contains(t, out, tt.wantLevel)
			require.Contains(t, out, tt.wantMsg)
		})
	}
}

func TestHandleServerMessage_InvalidJSON(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	HandleServerMessage(context.Background(), "window/showMessage", json.RawMessage(`{invalid`))

	require.Contains(t, buf.String(), "Error unmarshal server message")
}

func TestHandleDiagnostics(t *testing.T) {
	t.Parallel()

	t.Run("invalid json is ignored", func(t *testing.T) {
		t.Parallel()
		c := newTestClient()
		HandleDiagnostics(c, json.RawMessage(`{invalid`))
		require.Empty(t, c.GetDiagnostics())
	})

	t.Run("valid diagnostics are stored and callback fires with total count", func(t *testing.T) {
		t.Parallel()
		c := newTestClient()

		var gotName string
		var gotCount int
		c.SetDiagnosticsCallback(func(name string, count int) {
			gotName = name
			gotCount = count
		})

		uri := protocol.DocumentURI("file:///a.go")
		params, err := json.Marshal(protocol.PublishDiagnosticsParams{
			URI: uri,
			Diagnostics: []protocol.Diagnostic{
				{Message: "unused variable", Severity: protocol.SeverityWarning},
				{Message: "syntax error", Severity: protocol.SeverityError},
			},
		})
		require.NoError(t, err)

		HandleDiagnostics(c, params)

		require.Equal(t, "test", gotName)
		require.Equal(t, 2, gotCount)
		require.Len(t, c.GetFileDiagnostics(uri), 2)
	})

	t.Run("aggregates total across multiple files", func(t *testing.T) {
		t.Parallel()
		c := newTestClient()

		var gotCount int
		c.SetDiagnosticsCallback(func(_ string, count int) { gotCount = count })

		params1, err := json.Marshal(protocol.PublishDiagnosticsParams{
			URI:         protocol.DocumentURI("file:///a.go"),
			Diagnostics: []protocol.Diagnostic{{Message: "one"}},
		})
		require.NoError(t, err)
		params2, err := json.Marshal(protocol.PublishDiagnosticsParams{
			URI:         protocol.DocumentURI("file:///b.go"),
			Diagnostics: []protocol.Diagnostic{{Message: "two"}, {Message: "three"}},
		})
		require.NoError(t, err)

		HandleDiagnostics(c, params1)
		HandleDiagnostics(c, params2)

		require.Equal(t, 3, gotCount)
	})

	t.Run("no callback set does not panic", func(t *testing.T) {
		t.Parallel()
		c := newTestClient()
		params, err := json.Marshal(protocol.PublishDiagnosticsParams{
			URI:         protocol.DocumentURI("file:///c.go"),
			Diagnostics: []protocol.Diagnostic{{Message: "one"}},
		})
		require.NoError(t, err)

		HandleDiagnostics(c, params)

		require.Len(t, c.GetFileDiagnostics(protocol.DocumentURI("file:///c.go")), 1)
	})
}
