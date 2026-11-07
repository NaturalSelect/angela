package vscodemcp_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/vscodemcp"
	"github.com/NaturalSelect/angela/internal/vscodemcp/vscodemcptest"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type toolHandler = func(ctx context.Context, args json.RawMessage) *mcp.CallToolResult

func serverWithTool(t *testing.T, name string, handler toolHandler) *vscodemcptest.Server {
	t.Helper()
	return vscodemcptest.Start(t, vscodemcptest.Options{
		Tools: map[string]toolHandler{name: handler},
	})
}

func TestClient_Info(t *testing.T) {
	t.Parallel()

	srv := serverWithTool(t, "get_vscode_info", func(context.Context, json.RawMessage) *mcp.CallToolResult {
		return textResult(`{
			"version": "1.99.0",
			"appName": "Visual Studio Code",
			"appRoot": "/usr/share/code",
			"language": "en",
			"machineId": "m1",
			"sessionId": "s1",
			"uriScheme": "vscode",
			"shell": "/bin/zsh"
		}`)
	})

	got, err := newClient(t, srv).Info(t.Context())
	require.NoError(t, err)
	require.Equal(t, vscodemcp.VSCodeInfo{
		Version:   "1.99.0",
		AppName:   "Visual Studio Code",
		AppRoot:   "/usr/share/code",
		Language:  "en",
		MachineID: "m1",
		SessionID: "s1",
		URIScheme: "vscode",
		Shell:     "/bin/zsh",
	}, got)
}

func TestClient_Selection(t *testing.T) {
	t.Parallel()

	t.Run("current selection", func(t *testing.T) {
		t.Parallel()
		srv := serverWithTool(t, "get_selection", func(context.Context, json.RawMessage) *mcp.CallToolResult {
			return textResult(`{
				"text": "foo",
				"filePath": "/work/a.go",
				"fileUrl": "file:///work/a.go",
				"selection": {
					"start": {"line": 1, "character": 2},
					"end": {"line": 1, "character": 5},
					"isEmpty": false
				},
				"current": true
			}`)
		})

		got, err := newClient(t, srv).Selection(t.Context())
		require.NoError(t, err)
		require.Equal(t, &vscodemcp.Selection{
			Text:     "foo",
			FilePath: "/work/a.go",
			FileURL:  "file:///work/a.go",
			Range: vscodemcp.SelectionRange{
				Range: vscodemcp.Range{
					Start: vscodemcp.Position{Line: 1, Character: 2},
					End:   vscodemcp.Position{Line: 1, Character: 5},
				},
			},
			Current: true,
		}, got)
	})

	t.Run("no selection is nil", func(t *testing.T) {
		t.Parallel()
		srv := serverWithTool(t, "get_selection", func(context.Context, json.RawMessage) *mcp.CallToolResult {
			return textResult("null")
		})

		got, err := newClient(t, srv).Selection(t.Context())
		require.NoError(t, err)
		require.Nil(t, got)
	})
}

func TestClient_Diagnostics(t *testing.T) {
	t.Parallel()

	const payload = `[{
		"uri": "file:///work/a.go",
		"filePath": "/work/a.go",
		"diagnostics": [
			{
				"message": "undefined: x",
				"severity": "error",
				"range": {"start": {"line": 3, "character": 0}, "end": {"line": 3, "character": 1}},
				"source": "compiler",
				"code": "UndeclaredName"
			},
			{
				"message": "unused",
				"severity": "hint",
				"range": {"start": {"line": 9, "character": 4}, "end": {"line": 9, "character": 8}},
				"code": 6133
			}
		]
	}]`

	want := []vscodemcp.FileDiagnostics{{
		URI:      "file:///work/a.go",
		FilePath: "/work/a.go",
		Diagnostics: []vscodemcp.Diagnostic{
			{
				Message:  "undefined: x",
				Severity: vscodemcp.SeverityError,
				Range: vscodemcp.Range{
					Start: vscodemcp.Position{Line: 3},
					End:   vscodemcp.Position{Line: 3, Character: 1},
				},
				Source: "compiler",
				Code:   "UndeclaredName",
			},
			{
				Message:  "unused",
				Severity: vscodemcp.SeverityHint,
				Range: vscodemcp.Range{
					Start: vscodemcp.Position{Line: 9, Character: 4},
					End:   vscodemcp.Position{Line: 9, Character: 8},
				},
				Code: "6133",
			},
		},
	}}

	t.Run("all files sends no uri", func(t *testing.T) {
		t.Parallel()
		got := make(chan json.RawMessage, 1)
		srv := serverWithTool(t, "get_diagnostics", func(_ context.Context, args json.RawMessage) *mcp.CallToolResult {
			got <- args
			return textResult(payload)
		})

		files, err := newClient(t, srv).Diagnostics(t.Context(), "")
		require.NoError(t, err)
		require.Equal(t, want, files)

		var args map[string]any
		require.NoError(t, json.Unmarshal(<-got, &args))
		require.NotContains(t, args, "uri")
	})

	t.Run("one file sends its uri", func(t *testing.T) {
		t.Parallel()
		got := make(chan json.RawMessage, 1)
		srv := serverWithTool(t, "get_diagnostics", func(_ context.Context, args json.RawMessage) *mcp.CallToolResult {
			got <- args
			return textResult("[]")
		})

		path := filepath.Join(t.TempDir(), "a b.go")
		files, err := newClient(t, srv).Diagnostics(t.Context(), path)
		require.NoError(t, err)
		require.Empty(t, files)

		wantURI, err := vscodemcp.FileURI(path)
		require.NoError(t, err)
		var args struct {
			URI string `json:"uri"`
		}
		require.NoError(t, json.Unmarshal(<-got, &args))
		require.Equal(t, wantURI, args.URI)
	})
}

func TestClient_Tools_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result *mcp.CallToolResult
	}{
		{
			name:   "server reported error",
			result: &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "boom"}}},
		},
		{name: "not json", result: textResult("not json")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := serverWithTool(t, "get_vscode_info", func(context.Context, json.RawMessage) *mcp.CallToolResult {
				return tt.result
			})

			_, err := newClient(t, srv).Info(t.Context())
			require.Error(t, err)
		})
	}
}

func TestFileURI(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	got, err := vscodemcp.FileURI(filepath.Join(dir, "a b.go"))
	require.NoError(t, err)
	require.Contains(t, got, "file:///")
	require.Contains(t, got, "a%20b.go")

	rel, err := vscodemcp.FileURI("rel.go")
	require.NoError(t, err)
	require.Contains(t, rel, "file:///")
}
