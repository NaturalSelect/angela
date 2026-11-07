package editorapproval

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/vscodemcp/vscodemcptest"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// newTestEditor installs a lock file for srv and returns a VSCodeMCP
// wired to find it (plus the $COPILOT_HOME it lives under, for tests
// that need to build their own variant Getenv).
func newTestEditor(t *testing.T, srv *vscodemcptest.Server) (VSCodeMCP, string) {
	t.Helper()
	copilotHome, workingDir := srv.Install(t)
	return VSCodeMCP{
		WorkingDir: workingDir,
		Getenv:     vscodemcptest.Getenv(copilotHome, "vscode"),
	}, copilotHome
}

func TestVSCodeMCP_Name(t *testing.T) {
	require.Equal(t, "vscode-mcp", VSCodeMCP{}.Name())
}

func TestVSCodeMCP_Available_RequiresVSCodeTerminalAndLock(t *testing.T) {
	srv := vscodemcptest.Start(t, vscodemcptest.Options{})
	editor, copilotHome := newTestEditor(t, srv)
	require.True(t, editor.Available())

	editor.Getenv = vscodemcptest.Getenv(copilotHome, "") // not inside VS Code's integrated terminal
	require.False(t, editor.Available())

	editor.Getenv = vscodemcptest.Getenv(t.TempDir(), "vscode") // no lock file under this COPILOT_HOME
	require.False(t, editor.Available())
}

func TestVSCodeMCP_Review_Approve(t *testing.T) {
	srv := vscodemcptest.Start(t, vscodemcptest.Options{
		OpenDiff: func(_ context.Context, args vscodemcptest.OpenDiffArgs) (*mcp.CallToolResult, error) {
			require.Equal(t, "new\n", args.NewFileContents)
			require.NotEmpty(t, args.TabName)
			return textResult(`{"success":true,"result":"SAVED","trigger":"accept_button","message":"applied"}`), nil
		},
	})
	editor, _ := newTestEditor(t, srv)

	got, err := editor.Review(t.Context(), Request{
		FilePath:    "foo.go",
		OldContent:  "old\n",
		NewContent:  "new\n",
		Description: "add a greeting",
	})
	require.NoError(t, err)
	require.Equal(t, Decision{Outcome: OutcomeApprove, Content: "new\n"}, got)
}

func TestVSCodeMCP_Review_Reject(t *testing.T) {
	srv := vscodemcptest.Start(t, vscodemcptest.Options{
		OpenDiff: func(context.Context, vscodemcptest.OpenDiffArgs) (*mcp.CallToolResult, error) {
			return textResult(`{"success":true,"result":"REJECTED","trigger":"reject_button","message":"discarded"}`), nil
		},
	})
	editor, _ := newTestEditor(t, srv)

	got, err := editor.Review(t.Context(), Request{FilePath: "foo.go", OldContent: "old\n", NewContent: "new\n"})
	require.NoError(t, err)
	require.Equal(t, Decision{Outcome: OutcomeDeny}, got)
}

func TestVSCodeMCP_Review_ServerReportedError(t *testing.T) {
	srv := vscodemcptest.Start(t, vscodemcptest.Options{
		OpenDiff: func(context.Context, vscodemcptest.OpenDiffArgs) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "boom"}}}, nil
		},
	})
	editor, _ := newTestEditor(t, srv)

	_, err := editor.Review(t.Context(), Request{FilePath: "foo.go"})
	require.Error(t, err)
}

func TestVSCodeMCP_Review_NoLockReturnsError(t *testing.T) {
	editor := VSCodeMCP{WorkingDir: t.TempDir(), Getenv: vscodemcptest.Getenv(t.TempDir(), "vscode")}

	_, err := editor.Review(t.Context(), Request{FilePath: "foo.go"})
	require.Error(t, err)
}

func TestVSCodeMCP_Review_CancelCallsCloseDiff(t *testing.T) {
	started := make(chan struct{})
	// resolved is closed by OnCloseDiff below, mirroring how VS Code's
	// real diff-state tracking resolves a still-pending open_diff call
	// once close_diff names its tab — not by tearing down the
	// connection open_diff's call is running on, which Review
	// deliberately avoids doing.
	resolved := make(chan struct{})
	srv := vscodemcptest.Start(t, vscodemcptest.Options{
		OpenDiff: func(context.Context, vscodemcptest.OpenDiffArgs) (*mcp.CallToolResult, error) {
			close(started)
			<-resolved
			return textResult(`{"success":true,"result":"REJECTED","trigger":"closed_via_tool","message":"closed"}`), nil
		},
		OnCloseDiff: func(string) { close(resolved) },
	})
	editor, _ := newTestEditor(t, srv)

	reviewCtx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := editor.Review(reviewCtx, Request{FilePath: "foo.go"})
		done <- err
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("open_diff was never called")
	}

	cancel()

	select {
	case tab := <-srv.CloseDiffCalls:
		require.NotEmpty(t, tab)
	case <-time.After(5 * time.Second):
		t.Fatal("close_diff was not called after Review's context was cancelled")
	}

	err := <-done
	require.ErrorIs(t, err, context.Canceled)
}

func TestReviewTabName(t *testing.T) {
	t.Parallel()

	workingDir := filepath.Join(t.TempDir(), "project")
	inside := filepath.Join(workingDir, "internal", "foo.go")
	outside := filepath.Join(t.TempDir(), "bar.go")

	tests := []struct {
		name string
		req  Request
		want string
	}{
		{
			name: "no description falls back to the base name",
			req:  Request{FilePath: inside},
			want: "foo.go",
		},
		{
			name: "a description path inside the working dir is shortened",
			req:  Request{FilePath: inside, Description: "Replace content in file " + inside},
			want: "Replace content in file " + filepath.Join("internal", "foo.go"),
		},
		{
			name: "a description path outside the working dir stays absolute",
			req:  Request{FilePath: outside, Description: "Replace content in file " + outside},
			want: "Replace content in file " + outside,
		},
		{
			name: "a description without a real path is left untouched",
			req:  Request{FilePath: "PROPOSAL.md", Description: "Merge PROPOSAL.md"},
			want: "Merge PROPOSAL.md",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := reviewTabName(tt.req, workingDir)
			require.True(t, strings.HasPrefix(got, "angela: "+tt.want+" ("), "got %q", got)
		})
	}
}
