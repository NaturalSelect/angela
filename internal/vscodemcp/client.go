package vscodemcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
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

// ErrNoEndpoint is returned by Discover when no running VS Code window
// advertises an endpoint for the working directory.
var ErrNoEndpoint = errors.New("no VS Code MCP endpoint found")

// Client talks to the local MCP server that VS Code's GitHub Copilot
// Chat extension exposes for its own "Copilot CLI" chat sessions.
//
// NOTE: This channel is undocumented and private to the Copilot Chat
// extension: there is no compatibility guarantee on the lock file
// format, the auth scheme, or the tools' existence and shape. Callers
// are expected to treat every error as "channel unavailable" and fall
// back to something else rather than surface it to the user directly.
//
// Every call opens its own MCP session. The streamable HTTP transport
// doesn't support layering a second concurrent call on top of one
// already in flight on the same session, and open_diff stays in flight
// until the user acts.
type Client struct {
	// Command is the VS Code CLI invocation Open uses, e.g.
	// []string{"code"}. It defaults to []string{"code"} when empty;
	// tests point it at a stand-in binary.
	Command []string

	lock LockFile
}

// Discover resolves the endpoint of the VS Code window whose workspace
// contains workingDir. It deliberately doesn't dial the socket: a lock
// file can go stale between discovery and the first call regardless, so
// that cost belongs to the calls. A nil getenv means os.Getenv.
func Discover(getenv func(string) string, workingDir string) (*Client, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	lock, ok := findLock(lockDir(getenv), workingDir)
	if !ok {
		return nil, fmt.Errorf("%w for %s", ErrNoEndpoint, workingDir)
	}
	return &Client{lock: lock}, nil
}

// InIntegratedTerminal reports whether the process runs inside VS Code's
// integrated terminal, a cheap signal that a matching window is nearby.
// A nil getenv means os.Getenv.
func InIntegratedTerminal(getenv func(string) string) bool {
	if getenv == nil {
		getenv = os.Getenv
	}
	return strings.EqualFold(getenv("TERM_PROGRAM"), "vscode")
}

// OpenDiffRequest describes the diff open_diff shows. The original side
// is read from FilePath by VS Code itself; NewContents is the proposed
// replacement.
type OpenDiffRequest struct {
	FilePath    string
	NewContents string
	TabName     string
}

// DiffStatus is how the user resolved an open diff.
type DiffStatus string

const (
	DiffSaved    DiffStatus = "SAVED"
	DiffRejected DiffStatus = "REJECTED"
)

// OpenDiffResult is what open_diff reports once the user resolves it.
type OpenDiffResult struct {
	Status  DiffStatus
	Trigger string
	Message string
}

// openDiffPayload is the JSON open_diff's text content decodes to (see
// VS Code's tools/openDiff.ts upstream).
type openDiffPayload struct {
	Success bool   `json:"success"`
	Result  string `json:"result"`
	Trigger string `json:"trigger"`
	Message string `json:"message"`
}

// OpenDiff shows a diff in VS Code and blocks until the user accepts or
// rejects it. When ctx is cancelled first, it closes the diff tab on a
// best-effort basis and returns ctx.Err().
func (c *Client) OpenDiff(ctx context.Context, req OpenDiffRequest) (OpenDiffResult, error) {
	session, err := c.connect(ctx)
	if err != nil {
		return OpenDiffResult{}, fmt.Errorf("connect to VS Code MCP endpoint: %w", err)
	}
	defer session.Close()

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
				"new_file_contents":  req.NewContents,
				"tab_name":           req.TabName,
			},
		})
		resultCh <- callOutcome{res, err}
	}()

	select {
	case <-ctx.Done():
		// NOTE: Best-effort: if this fails, the tab is simply left open
		// in VS Code for the user to close by hand.
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = c.CloseDiff(closeCtx, req.TabName)
		return OpenDiffResult{}, ctx.Err()
	case outcome := <-resultCh:
		if outcome.err != nil {
			return OpenDiffResult{}, fmt.Errorf("open_diff: %w", outcome.err)
		}
		return decodeOpenDiffResult(outcome.res)
	}
}

// CloseDiff asks VS Code to close the diff tab named tabName. It
// connects a fresh session rather than reusing OpenDiff's, since that
// session's open_diff call is still outstanding when this matters.
func (c *Client) CloseDiff(ctx context.Context, tabName string) error {
	session, err := c.connect(ctx)
	if err != nil {
		return fmt.Errorf("connect to VS Code MCP endpoint: %w", err)
	}
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "close_diff",
		Arguments: map[string]any{"tab_name": tabName},
	})
	if err != nil {
		return fmt.Errorf("close_diff: %w", err)
	}
	if res.IsError {
		return fmt.Errorf("close_diff reported an error: %s", contentText(res))
	}
	return nil
}

// connect opens one MCP session against the endpoint the lock file
// advertises.
func (c *Client) connect(ctx context.Context) (*mcp.ClientSession, error) {
	httpClient := &http.Client{
		Transport: &lockRoundTripper{
			base: &http.Transport{
				DialContext: func(dialCtx context.Context, _, _ string) (net.Conn, error) {
					return dialLock(dialCtx, c.lock)
				},
			},
			headers:   c.lock.Headers,
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

func decodeOpenDiffResult(res *mcp.CallToolResult) (OpenDiffResult, error) {
	text := contentText(res)
	if res.IsError {
		return OpenDiffResult{}, fmt.Errorf("open_diff reported an error: %s", text)
	}

	var parsed openDiffPayload
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return OpenDiffResult{}, fmt.Errorf("decode open_diff result %q: %w", text, err)
	}
	if !parsed.Success {
		return OpenDiffResult{}, fmt.Errorf("open_diff did not succeed: %s", parsed.Message)
	}

	status := DiffStatus(parsed.Result)
	if status != DiffSaved && status != DiffRejected {
		return OpenDiffResult{}, fmt.Errorf("unexpected open_diff result %q", parsed.Result)
	}
	return OpenDiffResult{Status: status, Trigger: parsed.Trigger, Message: parsed.Message}, nil
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
