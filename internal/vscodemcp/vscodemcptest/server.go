// Package vscodemcptest provides an in-process fake of the MCP server
// that VS Code's Copilot Chat extension exposes, plus the lock file and
// environment plumbing needed to point a vscodemcp.Client at it.
package vscodemcptest

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/vscodemcp"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// OpenDiffArgs are the arguments the fake open_diff tool receives.
type OpenDiffArgs struct {
	OriginalFilePath string `json:"original_file_path"`
	NewFileContents  string `json:"new_file_contents"`
	TabName          string `json:"tab_name"`
}

type closeDiffArgs struct {
	TabName string `json:"tab_name"`
}

// Options configures the behavior of a fake server.
type Options struct {
	// OpenDiff handles open_diff calls. When nil, they fail with an
	// error result.
	OpenDiff func(ctx context.Context, args OpenDiffArgs) (*mcp.CallToolResult, error)
	// OnCloseDiff, if non-nil, runs synchronously inside the close_diff
	// handler after the tab name is recorded on Server.CloseDiffCalls.
	// Tests that need close_diff to resolve a still-pending open_diff
	// call (mirroring VS Code's own diff-state tracking, which resolves
	// the matching open_diff call when close_diff names its tab) use it
	// to do so.
	OnCloseDiff func(tabName string)
	// Tools adds tools besides open_diff and close_diff, keyed by name.
	// Their arguments arrive as the raw JSON the client sent, and are not
	// validated against any schema.
	Tools map[string]func(ctx context.Context, args json.RawMessage) *mcp.CallToolResult
}

// Server is a running fake VS Code MCP server.
type Server struct {
	// Scheme and SocketPath are what a lock file should advertise.
	Scheme     string
	SocketPath string
	// Nonce is the secret the server expects in the Authorization header.
	Nonce string
	// CloseDiffCalls receives the tab name of every close_diff call.
	CloseDiffCalls <-chan string
	// SessionHeaders receives the headers of the request that started
	// each session.
	SessionHeaders <-chan http.Header

	mu    sync.Mutex
	conns map[string]mcp.Connection
}

// Notify pushes a notification to every connected session, the way VS
// Code pushes selection_changed and its siblings. It fails if some
// session cannot take it right now, notably one that hasn't opened its
// standalone event stream yet, so tests wait for it with a retry.
func (s *Server) Notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}

	s.mu.Lock()
	conns := slices.Collect(maps.Values(s.conns))
	s.mu.Unlock()
	if len(conns) == 0 {
		return errors.New("no session connected")
	}

	var errs []error
	for _, conn := range conns {
		errs = append(errs, conn.Write(context.Background(), &jsonrpc.Request{Method: method, Params: raw}))
	}
	return errors.Join(errs...)
}

// recordingTransport hands the connection it creates to onConnect, which
// is how Server.Notify gets at a session's write side: the SDK server
// has no API for sending a notification outside the MCP spec.
type recordingTransport struct {
	*mcp.StreamableServerTransport
	onConnect func(mcp.Connection)
}

func (t *recordingTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := t.StreamableServerTransport.Connect(ctx)
	if err == nil {
		t.onConnect(conn)
	}
	return conn, err
}

// Start serves open_diff and close_diff over this platform's lock file
// transport, behind session bootstrap logic that mirrors VS Code's
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
// Passing through this is what proves vscodemcp.Client's round-tripper
// compensates for that correctly, rather than only working against a
// server that behaves like the SDK's own.
func Start(t testing.TB, opts Options) *Server {
	t.Helper()

	nonce := uuid.NewString()
	closeDiffCh := make(chan string, 4)
	sessionHeadersCh := make(chan http.Header, 8)

	server := mcp.NewServer(&mcp.Implementation{Name: "fake-vscode"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "open_diff", Description: "fake open_diff"},
		func(ctx context.Context, _ *mcp.CallToolRequest, args OpenDiffArgs) (*mcp.CallToolResult, any, error) {
			if opts.OpenDiff == nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "open_diff not configured"}}}, nil, nil
			}
			res, err := opts.OpenDiff(ctx, args)
			return res, nil, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "close_diff", Description: "fake close_diff"},
		func(_ context.Context, _ *mcp.CallToolRequest, args closeDiffArgs) (*mcp.CallToolResult, any, error) {
			closeDiffCh <- args.TabName
			if opts.OnCloseDiff != nil {
				opts.OnCloseDiff(args.TabName)
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"success":true}`}}}, nil, nil
		})

	for name, handler := range opts.Tools {
		server.AddTool(
			&mcp.Tool{Name: name, Description: "fake " + name, InputSchema: map[string]any{"type": "object"}},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return handler(ctx, req.Params.Arguments), nil
			})
	}

	srv := &Server{
		Nonce:          nonce,
		CloseDiffCalls: closeDiffCh,
		SessionHeaders: sessionHeadersCh,
		conns:          map[string]mcp.Connection{},
	}

	var mu sync.Mutex
	transports := map[string]*recordingTransport{}

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
			transport = &recordingTransport{
				StreamableServerTransport: &mcp.StreamableServerTransport{SessionID: sessionID},
				onConnect: func(conn mcp.Connection) {
					srv.mu.Lock()
					defer srv.mu.Unlock()
					srv.conns[sessionID] = conn
				},
			}
			if _, err := server.Connect(r.Context(), transport, nil); err != nil {
				mu.Unlock()
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			transports[sessionID] = transport
			select {
			case sessionHeadersCh <- r.Header.Clone():
			default:
			}
		}
		mu.Unlock()

		transport.ServeHTTP(w, r)
	})

	l, scheme, sockPath := listen(t)

	httpSrv := &http.Server{Handler: mimicVSCodeSessions}
	go func() { _ = httpSrv.Serve(l) }()
	t.Cleanup(func() { _ = httpSrv.Close() })

	srv.Scheme, srv.SocketPath = scheme, sockPath
	return srv
}

// Lock returns a lock file advertising s for a VS Code window that has
// workspace open, owned by the current process so it counts as alive.
func (s *Server) Lock(workspace string) vscodemcp.LockFile {
	return vscodemcp.LockFile{
		SocketPath:       s.SocketPath,
		Scheme:           s.Scheme,
		Headers:          map[string]string{"Authorization": "Nonce " + s.Nonce},
		PID:              os.Getpid(),
		WorkspaceFolders: []string{workspace},
		Timestamp:        time.Now().UnixMilli(),
	}
}

// Install writes a lock file for s under a fresh $COPILOT_HOME and
// returns that directory together with a fresh working directory the
// lock file covers.
func (s *Server) Install(t testing.TB) (copilotHome, workingDir string) {
	t.Helper()
	copilotHome = t.TempDir()
	ideDir := filepath.Join(copilotHome, "ide")
	require.NoError(t, os.MkdirAll(ideDir, 0o700))
	workingDir = t.TempDir()

	WriteLock(t, ideDir, "test.lock", s.Lock(workingDir))
	return copilotHome, workingDir
}

// WriteLock writes lock as dir/name.
func WriteLock(t testing.TB, dir, name string, lock vscodemcp.LockFile) {
	t.Helper()
	data, err := json.Marshal(lock)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), data, 0o600))
}

// Getenv builds an environment lookup for code under test.
func Getenv(copilotHome, termProgram string) func(string) string {
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
