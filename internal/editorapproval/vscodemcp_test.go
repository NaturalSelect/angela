package editorapproval

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type openDiffArgs struct {
	OriginalFilePath string `json:"original_file_path"`
	NewFileContents  string `json:"new_file_contents"`
	TabName          string `json:"tab_name"`
}

type closeDiffArgs struct {
	TabName string `json:"tab_name"`
}

// startFakeVSCodeServer serves open_diff (driven by openDiff) and
// close_diff (recording its tab name onto the returned channel) over a
// Unix socket, behind session bootstrap logic that mirrors VS Code's
// actual contract rather than the stock SDK server's.
//
// Two things about VS Code's real server (upstream's
// inProcHttpServer.ts _handlePost) differ from both the MCP spec's
// usual server-assigns-the-id flow and the SDK's own reference
// StreamableHTTPHandler, and matter enough to replicate by hand here
// with the lower-level StreamableServerTransport:
//  1. Every request must carry the expected Authorization header.
//  2. Every request must carry Mcp-Session-Id, including the very
//     first (initialize) one — the SDK's stock handler instead treats
//     a session id present before any session exists as a lookup for
//     an existing session and rejects it as not found.
//  3. That client-chosen id is adopted verbatim as the new session's
//     id (sessionIdGenerator: () => sessionId) rather than the server
//     generating its own.
//
// Passing through this is what proves VSCodeMCP's round-tripper
// compensates for that correctly, rather than only working against a
// server that behaves like the SDK's own.
//
// onCloseDiff, if non-nil, runs synchronously inside the close_diff
// handler after recording to closeDiffCh — tests that need close_diff
// to resolve a still-pending openDiff callback (mirroring VS Code's own
// diff-state tracking, which resolves the matching open_diff call when
// close_diff names its tab) use it to do so.
func startFakeVSCodeServer(t *testing.T, openDiff func(ctx context.Context, args openDiffArgs) (*mcp.CallToolResult, error), onCloseDiff func(tabName string)) (scheme, sockPath, nonce string, closeDiffCh chan string) {
	t.Helper()

	nonce = uuid.NewString()
	closeDiffCh = make(chan string, 4)

	server := mcp.NewServer(&mcp.Implementation{Name: "fake-vscode"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "open_diff", Description: "fake open_diff"},
		func(ctx context.Context, _ *mcp.CallToolRequest, args openDiffArgs) (*mcp.CallToolResult, any, error) {
			res, err := openDiff(ctx, args)
			return res, nil, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "close_diff", Description: "fake close_diff"},
		func(_ context.Context, _ *mcp.CallToolRequest, args closeDiffArgs) (*mcp.CallToolResult, any, error) {
			closeDiffCh <- args.TabName
			if onCloseDiff != nil {
				onCloseDiff(args.TabName)
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"success":true}`}}}, nil, nil
		})

	var mu sync.Mutex
	transports := map[string]*mcp.StreamableServerTransport{}

	mimicVSCodeSessions := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Nonce "+nonce {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		sessionID := r.Header.Get("Mcp-Session-Id")
		if sessionID == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		mu.Lock()
		transport, ok := transports[sessionID]
		if !ok {
			transport = &mcp.StreamableServerTransport{SessionID: sessionID}
			if _, err := server.Connect(r.Context(), transport, nil); err != nil {
				mu.Unlock()
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			transports[sessionID] = transport
		}
		mu.Unlock()

		transport.ServeHTTP(w, r)
	})

	l, scheme, sockPath := fakeLockListener(t)

	httpSrv := &http.Server{Handler: mimicVSCodeSessions}
	go func() { _ = httpSrv.Serve(l) }()
	t.Cleanup(func() { _ = httpSrv.Close() })

	return scheme, sockPath, nonce, closeDiffCh
}

// lockGetenv builds a Getenv func for a VSCodeMCP under test.
func lockGetenv(copilotHome, termProgram string) func(string) string {
	return func(key string) string {
		switch key {
		case "COPILOT_HOME":
			return copilotHome
		case "TERM_PROGRAM":
			return termProgram
		}
		return ""
	}
}

// newTestEditor writes a lock file advertising sockPath/nonce for a
// fresh working directory, and returns a VSCodeMCP wired to find it
// (plus the $COPILOT_HOME it lives under, for tests that need to build
// their own variant Getenv).
func newTestEditor(t *testing.T, scheme, sockPath, nonce string) (VSCodeMCP, string) {
	t.Helper()
	copilotHome := t.TempDir()
	ideDir := filepath.Join(copilotHome, "ide")
	require.NoError(t, os.MkdirAll(ideDir, 0o700))
	work := t.TempDir()

	writeLock(t, ideDir, "test.lock", lockFile{
		SocketPath:       sockPath,
		Scheme:           scheme,
		Headers:          map[string]string{"Authorization": "Nonce " + nonce},
		PID:              os.Getpid(),
		WorkspaceFolders: []string{work},
		Timestamp:        time.Now().UnixMilli(),
	})

	return VSCodeMCP{
		WorkingDir: work,
		Getenv:     lockGetenv(copilotHome, "vscode"),
	}, copilotHome
}

func TestVSCodeMCP_Name(t *testing.T) {
	require.Equal(t, "vscode-mcp", VSCodeMCP{}.Name())
}

func TestVSCodeMCP_Available_RequiresVSCodeTerminalAndLock(t *testing.T) {
	scheme, sockPath, nonce, _ := startFakeVSCodeServer(t, nil, nil)
	editor, copilotHome := newTestEditor(t, scheme, sockPath, nonce)
	require.True(t, editor.Available())

	editor.Getenv = lockGetenv(copilotHome, "") // not inside VS Code's integrated terminal
	require.False(t, editor.Available())

	editor.Getenv = lockGetenv(t.TempDir(), "vscode") // no lock file under this COPILOT_HOME
	require.False(t, editor.Available())
}

func TestVSCodeMCP_Review_Approve(t *testing.T) {
	scheme, sockPath, nonce, _ := startFakeVSCodeServer(t, func(_ context.Context, args openDiffArgs) (*mcp.CallToolResult, error) {
		require.Equal(t, "new\n", args.NewFileContents)
		require.NotEmpty(t, args.TabName)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{
			Text: `{"success":true,"result":"SAVED","trigger":"accept_button","message":"applied"}`,
		}}}, nil
	}, nil)
	editor, _ := newTestEditor(t, scheme, sockPath, nonce)

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
	scheme, sockPath, nonce, _ := startFakeVSCodeServer(t, func(context.Context, openDiffArgs) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{
			Text: `{"success":true,"result":"REJECTED","trigger":"reject_button","message":"discarded"}`,
		}}}, nil
	}, nil)
	editor, _ := newTestEditor(t, scheme, sockPath, nonce)

	got, err := editor.Review(t.Context(), Request{FilePath: "foo.go", OldContent: "old\n", NewContent: "new\n"})
	require.NoError(t, err)
	require.Equal(t, Decision{Outcome: OutcomeDeny, Reason: "rejected in VS Code"}, got)
}

func TestVSCodeMCP_Review_ServerReportedError(t *testing.T) {
	scheme, sockPath, nonce, _ := startFakeVSCodeServer(t, func(context.Context, openDiffArgs) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "boom"}}}, nil
	}, nil)
	editor, _ := newTestEditor(t, scheme, sockPath, nonce)

	_, err := editor.Review(t.Context(), Request{FilePath: "foo.go"})
	require.Error(t, err)
}

func TestVSCodeMCP_Review_MalformedResultErrors(t *testing.T) {
	scheme, sockPath, nonce, _ := startFakeVSCodeServer(t, func(context.Context, openDiffArgs) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "not json"}}}, nil
	}, nil)
	editor, _ := newTestEditor(t, scheme, sockPath, nonce)

	_, err := editor.Review(t.Context(), Request{FilePath: "foo.go"})
	require.Error(t, err)
}

func TestVSCodeMCP_Review_NoLockReturnsError(t *testing.T) {
	editor := VSCodeMCP{WorkingDir: t.TempDir(), Getenv: lockGetenv(t.TempDir(), "vscode")}

	_, err := editor.Review(t.Context(), Request{FilePath: "foo.go"})
	require.Error(t, err)
}

func TestVSCodeMCP_Review_CancelCallsCloseDiff(t *testing.T) {
	started := make(chan struct{})
	// resolved is closed by onCloseDiff below, mirroring how VS Code's
	// real diff-state tracking resolves a still-pending open_diff call
	// once close_diff names its tab — not by tearing down the
	// connection open_diff's call is running on, which Review
	// deliberately avoids doing (see closeDiff's doc comment).
	resolved := make(chan struct{})
	scheme, sockPath, nonce, closeDiffCh := startFakeVSCodeServer(t,
		func(_ context.Context, _ openDiffArgs) (*mcp.CallToolResult, error) {
			close(started)
			<-resolved
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{
				Text: `{"success":true,"result":"REJECTED","trigger":"closed_via_tool","message":"closed"}`,
			}}}, nil
		},
		func(string) { close(resolved) },
	)
	editor, _ := newTestEditor(t, scheme, sockPath, nonce)

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
	case tab := <-closeDiffCh:
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
