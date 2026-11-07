package vscodemcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolCaller is the part of an MCP client session callTool needs.
type toolCaller interface {
	CallTool(ctx context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error)
}

// VSCodeInfo describes the VS Code instance behind the endpoint.
type VSCodeInfo struct {
	Version   string `json:"version"`
	AppName   string `json:"appName"`
	AppRoot   string `json:"appRoot"`
	Language  string `json:"language"`
	MachineID string `json:"machineId"`
	SessionID string `json:"sessionId"`
	URIScheme string `json:"uriScheme"`
	Shell     string `json:"shell"`
}

// Position is a zero-based line and character offset in a document, as
// VS Code counts them.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is a span of a document from Start up to End.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// SelectionRange is the range a selection covers. IsEmpty is set for a
// bare cursor, whose Start and End coincide.
type SelectionRange struct {
	Range
	IsEmpty bool `json:"isEmpty"`
}

// Selection is the text selected in a VS Code editor.
type Selection struct {
	Text     string         `json:"text"`
	FilePath string         `json:"filePath"`
	FileURL  string         `json:"fileUrl"`
	Range    SelectionRange `json:"selection"`
	// Current is true when the selection comes from the editor that is
	// active right now, and false when VS Code is replaying the last
	// one it saw because no editor is active. It is only set on results
	// of the Selection method.
	Current bool `json:"current"`
}

// Severity is how serious a Diagnostic is.
type Severity string

const (
	SeverityError       Severity = "error"
	SeverityWarning     Severity = "warning"
	SeverityInformation Severity = "information"
	SeverityHint        Severity = "hint"
)

// DiagnosticCode is a diagnostic's code. Language servers report it as
// either a string or a number; both are kept as text.
type DiagnosticCode string

// UnmarshalJSON implements json.Unmarshaler.
func (c *DiagnosticCode) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*c = DiagnosticCode(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*c = DiagnosticCode(n.String())
	return nil
}

// Diagnostic is one error, warning or hint a language service reported.
type Diagnostic struct {
	Message  string         `json:"message"`
	Severity Severity       `json:"severity"`
	Range    Range          `json:"range"`
	Source   string         `json:"source,omitempty"`
	Code     DiagnosticCode `json:"code,omitempty"`
}

// FileDiagnostics are the diagnostics VS Code holds for one file.
type FileDiagnostics struct {
	URI string `json:"uri"`
	// FilePath is empty in a DiagnosticsChanged event, which names
	// files by URI only.
	FilePath    string       `json:"filePath"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// FileURI returns the file: URI VS Code knows path by. A relative path
// is resolved against the working directory first.
func FileURI(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("file uri for %s: %w", path, err)
	}
	p := filepath.ToSlash(abs)
	// NOTE: A Windows drive path (C:/dir) needs the leading slash a URI
	// path always has.
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String(), nil
}

// Info returns facts about the VS Code instance, such as its version.
func (c *Client) Info(ctx context.Context) (VSCodeInfo, error) {
	return oneShot[VSCodeInfo](ctx, c, "get_vscode_info", nil)
}

// Selection returns the text selected in the active editor, or the last
// selection VS Code saw when no editor is active. It returns nil when
// VS Code has seen no selection at all.
func (c *Client) Selection(ctx context.Context) (*Selection, error) {
	return oneShot[*Selection](ctx, c, "get_selection", nil)
}

// Diagnostics returns the problems VS Code lists for path, or for every
// file when path is empty. Files without problems are left out.
func (c *Client) Diagnostics(ctx context.Context, path string) ([]FileDiagnostics, error) {
	args, err := diagnosticsArgs(path)
	if err != nil {
		return nil, err
	}
	return oneShot[[]FileDiagnostics](ctx, c, "get_diagnostics", args)
}

// Info is like Client.Info, on this session.
func (s *Session) Info(ctx context.Context) (VSCodeInfo, error) {
	return callTool[VSCodeInfo](ctx, s.conn, "get_vscode_info", nil)
}

// Selection is like Client.Selection, on this session.
func (s *Session) Selection(ctx context.Context) (*Selection, error) {
	return callTool[*Selection](ctx, s.conn, "get_selection", nil)
}

// Diagnostics is like Client.Diagnostics, on this session.
func (s *Session) Diagnostics(ctx context.Context, path string) ([]FileDiagnostics, error) {
	args, err := diagnosticsArgs(path)
	if err != nil {
		return nil, err
	}
	return callTool[[]FileDiagnostics](ctx, s.conn, "get_diagnostics", args)
}

// SetName sets the name VS Code shows for this session, e.g. in the
// picker it offers when the user sends a file or selection to a
// session. VS Code forgets the name when the session ends.
func (s *Session) SetName(ctx context.Context, name string) error {
	res, err := callTool[struct {
		Success bool `json:"success"`
	}](ctx, s.conn, "update_session_name", map[string]any{"name": name})
	if err != nil {
		return err
	}
	if !res.Success {
		return fmt.Errorf("update_session_name did not succeed")
	}
	return nil
}

func diagnosticsArgs(path string) (map[string]any, error) {
	if path == "" {
		return nil, nil
	}
	uri, err := FileURI(path)
	if err != nil {
		return nil, err
	}
	return map[string]any{"uri": uri}, nil
}

// oneShot calls a tool on a session opened just for that call.
func oneShot[T any](ctx context.Context, c *Client, name string, args map[string]any) (T, error) {
	session, err := c.connect(ctx, connectOptions{})
	if err != nil {
		var zero T
		return zero, fmt.Errorf("connect to VS Code MCP endpoint: %w", err)
	}
	defer session.Close()
	return callTool[T](ctx, session, name, args)
}

// callTool calls a tool that reports its result as JSON in a text block,
// which is how every tool VS Code offers but open_diff and close_diff
// is shaped, and decodes it into T.
func callTool[T any](ctx context.Context, session toolCaller, name string, args map[string]any) (T, error) {
	var zero T
	params := &mcp.CallToolParams{Name: name}
	if args != nil {
		params.Arguments = args
	}
	res, err := session.CallTool(ctx, params)
	if err != nil {
		return zero, fmt.Errorf("%s: %w", name, err)
	}
	text := contentText(res)
	if res.IsError {
		return zero, fmt.Errorf("%s reported an error: %s", name, text)
	}

	var out T
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return zero, fmt.Errorf("decode %s result %q: %w", name, text, err)
	}
	return out, nil
}
