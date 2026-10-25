package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/acp"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/profiling"
	"github.com/NaturalSelect/angela/internal/workspace"
)

const acpTestTimeout = 30 * time.Second

// acpPeer plays the editor: it sends requests over a pipe and checks that
// everything the agent prints is a JSON-RPC message.
type acpPeer struct {
	t     *testing.T
	in    *io.PipeWriter
	lines chan string
	done  chan error
}

func startACP(t *testing.T, base localWorkspaceOptions) *acpPeer {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	p := &acpPeer{t: t, in: inW, lines: make(chan string, 64), done: make(chan error, 1)}

	go func() {
		err := runACP(t.Context(), inR, outW, base)
		_ = outW.Close()
		p.done <- err
	}()
	go func() {
		defer close(p.lines)
		sc := bufio.NewScanner(outR)
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			p.lines <- sc.Text()
		}
	}()
	t.Cleanup(func() { _ = inW.Close() })
	return p
}

func (p *acpPeer) parse(line string) map[string]any {
	p.t.Helper()
	var msg map[string]any
	require.NoError(p.t, json.Unmarshal([]byte(line), &msg), "stdout must carry only JSON, got %q", line)
	require.Equal(p.t, "2.0", msg["jsonrpc"], line)
	return msg
}

// call sends one request and returns its response, skipping notifications.
func (p *acpPeer) call(id int, method string, params any) map[string]any {
	p.t.Helper()
	req, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	require.NoError(p.t, err)
	_, err = p.in.Write(append(req, '\n'))
	require.NoError(p.t, err)

	timeout := time.After(acpTestTimeout)
	for {
		select {
		case line, ok := <-p.lines:
			require.True(p.t, ok, "the agent closed its output before answering %s", method)
			msg := p.parse(line)
			if msg["id"] == float64(id) && msg["method"] == nil {
				return msg
			}
		case <-timeout:
			p.t.Fatalf("no response to %s", method)
		}
	}
}

func (p *acpPeer) result(msg map[string]any) map[string]any {
	p.t.Helper()
	require.Nil(p.t, msg["error"], "unexpected error response: %v", msg["error"])
	res, ok := msg["result"].(map[string]any)
	require.True(p.t, ok, "response has no result: %v", msg)
	return res
}

// finish closes the editor's end and checks the agent stops cleanly and
// printed nothing but JSON-RPC.
func (p *acpPeer) finish() {
	p.t.Helper()
	require.NoError(p.t, p.in.Close())
	select {
	case err := <-p.done:
		require.NoError(p.t, err)
	case <-time.After(acpTestTimeout):
		p.t.Fatal("the agent did not stop after its input closed")
	}
	for line := range p.lines {
		p.parse(line)
	}
}

// acpDataDir returns a data directory whose removal may fail without failing
// the test.
//
// NOTE: The process-wide logger keeps the first workspace's log file open for
// the rest of the run, and Windows refuses to delete an open file, so
// t.TempDir would fail whichever test happens to open that workspace first.
func acpDataDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "angela-acp-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func acpTestBase(t *testing.T) localWorkspaceOptions {
	t.Helper()
	return localWorkspaceOptions{DataDir: acpDataDir(t), PermissionMode: permission.ModeManual}
}

func initializeParams() map[string]any {
	return map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{}}
}

func TestRunACP_Conversation(t *testing.T) {
	isolateSessionEnv(t)
	cwd, err := os.Getwd()
	require.NoError(t, err)
	writeModelsFixtureConfig(t, cwd)

	p := startACP(t, acpTestBase(t))

	init := p.result(p.call(1, "initialize", initializeParams()))
	require.EqualValues(t, 1, init["protocolVersion"])
	caps, ok := init["agentCapabilities"].(map[string]any)
	require.True(t, ok, "initialize must declare capabilities: %v", init)
	require.Equal(t, true, caps["loadSession"])

	created := p.result(p.call(2, "session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}}))
	sessionID, _ := created["sessionId"].(string)
	require.NotEmpty(t, sessionID)
	require.NotEmpty(t, created["configOptions"], "the editor needs the settings it can change")

	listed := p.result(p.call(3, "session/list", map[string]any{"cwd": cwd}))
	sessions, _ := listed["sessions"].([]any)
	require.Len(t, sessions, 1)
	require.Equal(t, sessionID, sessions[0].(map[string]any)["sessionId"])

	p.finish()
}

func TestRunACP_RefusesASecondProject(t *testing.T) {
	isolateSessionEnv(t)
	cwd, err := os.Getwd()
	require.NoError(t, err)
	writeModelsFixtureConfig(t, cwd)

	p := startACP(t, acpTestBase(t))
	p.result(p.call(1, "initialize", initializeParams()))
	p.result(p.call(2, "session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}}))

	refused := p.call(3, "session/new", map[string]any{"cwd": t.TempDir(), "mcpServers": []any{}})
	rpcErr, ok := refused["error"].(map[string]any)
	require.True(t, ok, "a second project must be an error: %v", refused)
	require.EqualValues(t, -32602, rpcErr["code"])

	p.finish()
}

func TestRunACP_RejectsARelativeDirectory(t *testing.T) {
	isolateSessionEnv(t)

	p := startACP(t, acpTestBase(t))
	p.result(p.call(1, "initialize", initializeParams()))

	refused := p.call(2, "session/new", map[string]any{"cwd": "relative/dir", "mcpServers": []any{}})
	rpcErr, ok := refused["error"].(map[string]any)
	require.True(t, ok, "%v", refused)
	require.EqualValues(t, -32602, rpcErr["code"])

	p.finish()
}

// TestOpenACPWorkspace pins what the editor's capabilities and the ACP
// session model change about the workspace.
func TestOpenACPWorkspace(t *testing.T) {
	tests := []struct {
		name               string
		caps               acp.ClientCaps
		wantNonInteractive bool
	}{
		{name: "an editor with forms can be asked questions", caps: acp.ClientCaps{Elicitation: true}, wantNonInteractive: false},
		{name: "an editor without forms cannot", caps: acp.ClientCaps{}, wantNonInteractive: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateSessionEnv(t)
			project := t.TempDir()

			ws, err := openACPWorkspace(t.Context(), acpTestBase(t), project, tt.caps)
			require.NoError(t, err)
			t.Cleanup(ws.Shutdown)

			appWs, ok := ws.(*workspace.AppWorkspace)
			require.True(t, ok)
			overrides := appWs.Store().Overrides()
			require.Equal(t, tt.wantNonInteractive, overrides.NonInteractive)
			require.True(t, overrides.NoBranchAgents, "ACP has no way to hand a conversation to a branch")

			cwd, err := os.Getwd()
			require.NoError(t, err)
			resolvedCwd, err := filepath.EvalSymlinks(cwd)
			require.NoError(t, err)
			resolvedProject, err := filepath.EvalSymlinks(appWs.WorkingDir())
			require.NoError(t, err)
			require.Equal(t, resolvedProject, resolvedCwd, "the process works from the project directory")
		})
	}
}

func TestOpenACPWorkspace_MissingDirectory(t *testing.T) {
	isolateSessionEnv(t)

	ws, err := openACPWorkspace(t.Context(), acpTestBase(t), t.TempDir()+"/missing", acp.ClientCaps{})
	require.Error(t, err)
	require.Nil(t, ws, "a failure must not return a non-nil interface wrapping nil")
}

// TestACPCmd_ProtocolStreamStaysClean runs the real command against the
// process's own stdin and stdout: the protocol goes to the original stdout,
// and os.Stdout is put back afterwards.
// servesProfiles reports whether this process is listed by "angela pprof".
func servesProfiles(t *testing.T) bool {
	t.Helper()
	processes, err := profiling.Running(t.Context())
	require.NoError(t, err)
	return slices.ContainsFunc(processes, func(p profiling.Process) bool { return p.PID == os.Getpid() })
}

func TestACPCmd_ProtocolStreamStaysClean(t *testing.T) {
	isolateSessionEnv(t)
	// NOTE: A short directory keeps the endpoint socket under the platform
	// path limit, which t.TempDir() can exceed on macOS.
	runtimeDir, err := os.MkdirTemp("", "pp")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)

	previousLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	cmd := newSetupWorkspaceTestCmd(t, acpDataDir(t))
	cmd.SetContext(t.Context())

	stdinR, stdinW, err := os.Pipe()
	require.NoError(t, err)
	stdoutR, stdoutW, err := os.Pipe()
	require.NoError(t, err)
	originalIn, originalOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = stdinR, stdoutW
	t.Cleanup(func() { os.Stdin, os.Stdout = originalIn, originalOut })

	done := make(chan error, 1)
	go func() { done <- acpCmd.RunE(cmd, nil) }()

	req, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": initializeParams()})
	require.NoError(t, err)
	_, err = stdinW.Write(append(req, '\n'))
	require.NoError(t, err)

	reader := bufio.NewReader(stdoutR)
	firstLine := make(chan []byte, 1)
	go func() {
		line, _ := reader.ReadBytes('\n')
		firstLine <- line
	}()
	var lines [][]byte
	select {
	case line := <-firstLine:
		lines = append(lines, line)
	case <-time.After(acpTestTimeout):
		t.Fatal("the initialize response never reached stdout")
	}
	require.True(t, servesProfiles(t), "angela pprof must be able to reach a running angela acp")
	require.NoError(t, stdinW.Close())

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(acpTestTimeout):
		t.Fatal("angela acp did not stop after its input closed")
	}
	require.False(t, servesProfiles(t), "the profile endpoint must go away with the process")
	require.Same(t, stdoutW, os.Stdout, "os.Stdout must be restored")
	_ = stdoutW.Close()

	rest, err := io.ReadAll(reader)
	require.NoError(t, err)
	sc := bufio.NewScanner(bytes.NewReader(append(bytes.Join(lines, nil), rest...)))
	sc.Buffer(nil, 1<<20)
	var seen int
	for sc.Scan() {
		var msg map[string]any
		require.NoError(t, json.Unmarshal(sc.Bytes(), &msg), "stdout must carry only JSON, got %q", sc.Text())
		require.Equal(t, "2.0", msg["jsonrpc"])
		seen++
	}
	require.NoError(t, sc.Err())
	require.Positive(t, seen, "the initialize response must reach stdout")
}
