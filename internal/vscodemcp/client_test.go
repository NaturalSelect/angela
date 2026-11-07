package vscodemcp_test

import (
	"context"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/vscodemcp"
	"github.com/NaturalSelect/angela/internal/vscodemcp/vscodemcptest"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func newClient(t *testing.T, srv *vscodemcptest.Server) *vscodemcp.Client {
	t.Helper()
	copilotHome, workingDir := srv.Install(t)
	client, err := vscodemcp.Discover(vscodemcptest.Getenv(copilotHome, "vscode"), workingDir)
	require.NoError(t, err)
	return client
}

func TestDiscover(t *testing.T) {
	t.Parallel()

	t.Run("finds the lock file covering the working dir", func(t *testing.T) {
		t.Parallel()
		srv := vscodemcptest.Start(t, vscodemcptest.Options{})
		copilotHome, workingDir := srv.Install(t)

		client, err := vscodemcp.Discover(vscodemcptest.Getenv(copilotHome, "vscode"), workingDir)
		require.NoError(t, err)
		require.NotNil(t, client)
	})

	t.Run("reports ErrNoEndpoint when nothing matches", func(t *testing.T) {
		t.Parallel()
		_, err := vscodemcp.Discover(vscodemcptest.Getenv(t.TempDir(), "vscode"), t.TempDir())
		require.ErrorIs(t, err, vscodemcp.ErrNoEndpoint)
	})
}

func TestInIntegratedTerminal(t *testing.T) {
	t.Parallel()

	require.True(t, vscodemcp.InIntegratedTerminal(vscodemcptest.Getenv("", "vscode")))
	require.True(t, vscodemcp.InIntegratedTerminal(vscodemcptest.Getenv("", "VSCode")))
	require.False(t, vscodemcp.InIntegratedTerminal(vscodemcptest.Getenv("", "")))
	require.False(t, vscodemcp.InIntegratedTerminal(vscodemcptest.Getenv("", "iTerm.app")))
}

func TestClient_OpenDiff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		want    vscodemcp.OpenDiffResult
	}{
		{
			name:    "saved",
			payload: `{"success":true,"result":"SAVED","trigger":"accept_button","message":"applied"}`,
			want:    vscodemcp.OpenDiffResult{Status: vscodemcp.DiffSaved, Trigger: "accept_button", Message: "applied"},
		},
		{
			name:    "rejected",
			payload: `{"success":true,"result":"REJECTED","trigger":"reject_button","message":"discarded"}`,
			want:    vscodemcp.OpenDiffResult{Status: vscodemcp.DiffRejected, Trigger: "reject_button", Message: "discarded"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := vscodemcptest.Start(t, vscodemcptest.Options{
				OpenDiff: func(_ context.Context, args vscodemcptest.OpenDiffArgs) (*mcp.CallToolResult, error) {
					require.Equal(t, "foo.go", args.OriginalFilePath)
					require.Equal(t, "new\n", args.NewFileContents)
					require.Equal(t, "my tab", args.TabName)
					return textResult(tt.payload), nil
				},
			})

			got, err := newClient(t, srv).OpenDiff(t.Context(), vscodemcp.OpenDiffRequest{
				FilePath:    "foo.go",
				NewContents: "new\n",
				TabName:     "my tab",
			})
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestClient_OpenDiff_Errors(t *testing.T) {
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
		{name: "success false", result: textResult(`{"success":false,"message":"nope"}`)},
		{name: "unknown result", result: textResult(`{"success":true,"result":"MAYBE"}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := vscodemcptest.Start(t, vscodemcptest.Options{
				OpenDiff: func(context.Context, vscodemcptest.OpenDiffArgs) (*mcp.CallToolResult, error) {
					return tt.result, nil
				},
			})

			_, err := newClient(t, srv).OpenDiff(t.Context(), vscodemcp.OpenDiffRequest{FilePath: "foo.go"})
			require.Error(t, err)
		})
	}
}

func TestClient_OpenDiff_CancelClosesDiff(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	// resolved is closed by OnCloseDiff below, mirroring how VS Code's
	// real diff-state tracking resolves a still-pending open_diff call
	// once close_diff names its tab, not by tearing down the connection
	// open_diff's call is running on, which OpenDiff deliberately avoids
	// doing.
	resolved := make(chan struct{})
	srv := vscodemcptest.Start(t, vscodemcptest.Options{
		OpenDiff: func(context.Context, vscodemcptest.OpenDiffArgs) (*mcp.CallToolResult, error) {
			close(started)
			<-resolved
			return textResult(`{"success":true,"result":"REJECTED","trigger":"closed_via_tool","message":"closed"}`), nil
		},
		OnCloseDiff: func(string) { close(resolved) },
	})
	client := newClient(t, srv)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := client.OpenDiff(ctx, vscodemcp.OpenDiffRequest{FilePath: "foo.go", TabName: "cancel me"})
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
		require.Equal(t, "cancel me", tab)
	case <-time.After(5 * time.Second):
		t.Fatal("close_diff was not called after the context was cancelled")
	}

	require.ErrorIs(t, <-done, context.Canceled)
}

func TestClient_CloseDiff(t *testing.T) {
	t.Parallel()

	srv := vscodemcptest.Start(t, vscodemcptest.Options{})

	require.NoError(t, newClient(t, srv).CloseDiff(t.Context(), "some tab"))
	require.Equal(t, "some tab", <-srv.CloseDiffCalls)
}
