package editorapproval

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NaturalSelect/angela/internal/version"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// sessionIDHeader is the MCP streamable-HTTP session header name. The
// SDK keeps its own copy unexported (mcp.streamable_headers.go), so it's
// duplicated here rather than imported.
const sessionIDHeader = "Mcp-Session-Id"

// VSCodeMCP reviews diffs through the local MCP server that VS Code's
// GitHub Copilot Chat extension exposes for its own "Copilot CLI" chat
// sessions. Unlike plain `code --wait --diff` (see VSCode in this
// package), that server's `open_diff` tool gives a real Accept/Reject
// decision instead of one inferred from whatever content is left in a
// file when a tab closes.
//
// This channel is undocumented and private to the Copilot Chat
// extension: there is no compatibility guarantee on the lock file
// format, the auth scheme, or the tool's existence and shape. Every
// failure here — no lock file, dial error, protocol error — is meant to
// be handled by the caller falling back to the terminal permission
// prompt, not surfaced to the user directly.
type VSCodeMCP struct {
	// WorkingDir is compared against each lock file's advertised
	// workspace folders to find the right VS Code window.
	WorkingDir string
	// Getenv defaults to os.Getenv; tests override it.
	Getenv func(string) string
}

// Name implements Editor.
func (v VSCodeMCP) Name() string { return "vscode-mcp" }

func (v VSCodeMCP) getenv() func(string) string {
	if v.Getenv != nil {
		return v.Getenv
	}
	return os.Getenv
}

// Available implements Editor. It requires running inside VS Code's
// integrated terminal — a cheap signal that a matching window is
// nearby — and a lock file matching WorkingDir. It deliberately doesn't
// dial the socket: that cost belongs in Review, and a lock file can go
// stale between this check and the call regardless.
func (v VSCodeMCP) Available() bool {
	getenv := v.getenv()
	if !strings.EqualFold(getenv("TERM_PROGRAM"), "vscode") {
		return false
	}
	_, ok := findLock(lockDir(getenv), v.WorkingDir)
	return ok
}

// Review implements Editor.
func (v VSCodeMCP) Review(ctx context.Context, req Request) (Decision, error) {
	getenv := v.getenv()
	lock, ok := findLock(lockDir(getenv), v.WorkingDir)
	if !ok {
		return Decision{}, fmt.Errorf("no VS Code MCP endpoint found for %s", v.WorkingDir)
	}

	session, err := connectVSCodeMCP(ctx, lock)
	if err != nil {
		return Decision{}, fmt.Errorf("connect to VS Code MCP endpoint: %w", err)
	}
	defer session.Close()

	tabName := reviewTabName(req)

	type callOutcome struct {
		res *mcp.CallToolResult
		err error
	}
	resultCh := make(chan callOutcome, 1)
	go func() {
		// Uses a context that survives ctx's cancellation: open_diff
		// only resolves on an explicit user action or a close_diff
		// call below, never on the caller giving up, so tearing down
		// the request early would just orphan the diff tab in VS
		// Code without telling the user anything happened.
		res, err := session.CallTool(context.WithoutCancel(ctx), &mcp.CallToolParams{
			Name: "open_diff",
			Arguments: map[string]any{
				"original_file_path": req.FilePath,
				"new_file_contents":  req.NewContent,
				"tab_name":           tabName,
			},
		})
		resultCh <- callOutcome{res, err}
	}()

	select {
	case <-ctx.Done():
		v.closeDiff(lock, tabName)
		return Decision{}, ctx.Err()
	case outcome := <-resultCh:
		if outcome.err != nil {
			return Decision{}, fmt.Errorf("open_diff: %w", outcome.err)
		}
		return decodeOpenDiffResult(outcome.res, req)
	}
}

// closeDiff asks VS Code to close a diff tab that Review has stopped
// waiting on. It connects a fresh, independent MCP session rather than
// reusing Review's: that session's open_diff call is still outstanding
// at this point, and the streamable HTTP transport doesn't support
// layering a second concurrent call on top of one already in flight on
// the same session. Best-effort: if this fails, the tab is simply left
// open in VS Code for the user to close by hand.
func (v VSCodeMCP) closeDiff(lock lockFile, tabName string) {
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	session, err := connectVSCodeMCP(closeCtx, lock)
	if err != nil {
		return
	}
	defer session.Close()

	_, _ = session.CallTool(closeCtx, &mcp.CallToolParams{
		Name:      "close_diff",
		Arguments: map[string]any{"tab_name": tabName},
	})
}

// connectVSCodeMCP opens one MCP session against the endpoint a lock
// file advertises.
func connectVSCodeMCP(ctx context.Context, lock lockFile) (*mcp.ClientSession, error) {
	httpClient := &http.Client{
		Transport: &lockRoundTripper{
			base: &http.Transport{
				DialContext: func(dialCtx context.Context, _, _ string) (net.Conn, error) {
					return dialLock(dialCtx, lock)
				},
			},
			headers:   lock.Headers,
			sessionID: uuid.NewString(),
		},
	}

	transport := &mcp.StreamableClientTransport{
		Endpoint:             "http://localhost/mcp",
		HTTPClient:           httpClient,
		DisableStandaloneSSE: true,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "angela", Version: version.Version}, nil)
	return client.Connect(ctx, transport, nil)
}

// reviewTabName names the VS Code diff tab. It's suffixed with a short
// unique id because the server tracks open diffs by tab name, and
// Angela can have more than one review in flight at once.
func reviewTabName(req Request) string {
	name := req.Description
	if name == "" {
		name = filepath.Base(req.FilePath)
	}
	return fmt.Sprintf("angela: %s (%s)", name, uuid.NewString()[:8])
}

// openDiffResult is the JSON payload open_diff's text content decodes
// to (see VS Code's tools/openDiff.ts upstream).
type openDiffResult struct {
	Success bool   `json:"success"`
	Result  string `json:"result"`
	Message string `json:"message"`
}

func decodeOpenDiffResult(res *mcp.CallToolResult, req Request) (Decision, error) {
	text := contentText(res)
	if res.IsError {
		return Decision{}, fmt.Errorf("open_diff reported an error: %s", text)
	}

	var parsed openDiffResult
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return Decision{}, fmt.Errorf("decode open_diff result %q: %w", text, err)
	}
	if !parsed.Success {
		return Decision{}, fmt.Errorf("open_diff did not succeed: %s", parsed.Message)
	}

	switch parsed.Result {
	case "SAVED":
		return Decision{Outcome: OutcomeApprove, Content: req.NewContent}, nil
	case "REJECTED":
		return Decision{Outcome: OutcomeDeny, Reason: "rejected in VS Code"}, nil
	default:
		return Decision{}, fmt.Errorf("unexpected open_diff result %q", parsed.Result)
	}
}

func contentText(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// lockRoundTripper adds the auth headers and session id a lock file's
// endpoint requires to every request. VS Code's server (unlike the
// go-sdk's own, and unlike the MCP spec's usual server-assigns-the-id
// flow) rejects the very first POST unless the client already supplies
// Mcp-Session-Id, using that value verbatim as the session id rather
// than generating its own — so this sets it unconditionally rather
// than only once the SDK has learned a session id from a response.
type lockRoundTripper struct {
	base      http.RoundTripper
	headers   map[string]string
	sessionID string
}

func (rt *lockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	for k, v := range rt.headers {
		req.Header.Set(k, v)
	}
	req.Header.Set(sessionIDHeader, rt.sessionID)
	return rt.base.RoundTrip(req)
}

var _ Editor = VSCodeMCP{}
