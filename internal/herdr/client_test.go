package herdr

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// shortSocketDir returns a fresh temp directory suitable as the base
// for a Unix socket path. Unlike t.TempDir(), it does not embed the
// test name, so paths built under it stay well below the 104-byte
// macOS sun_path limit regardless of how long the test name is.
func shortSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "herdr-sock")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// newTestClient creates a Client backed by a MockSender that records
// state transitions, in call order, without connecting to a real Unix
// socket.
func newTestClient(t *testing.T) (*Client, *[]string) {
	t.Helper()
	states := &[]string{}
	m := NewMockSender(gomock.NewController(t))
	m.EXPECT().send(gomock.Any()).DoAndReturn(func(req reportRequest) error {
		*states = append(*states, req.Params.State)
		return nil
	}).AnyTimes()
	m.EXPECT().close().AnyTimes()
	return &Client{state: stateIdle, snd: m}, states
}

func TestBasicLifecycle(t *testing.T) {
	t.Parallel()
	c, states := newTestClient(t)

	// Assistant message starts working.
	c.HandleEvent(AssistantMessage{SessionID: "sess-1"})
	assert.Equal(t, []string{stateWorking}, *states)

	// Run complete returns to idle.
	c.HandleEvent(RunComplete{SessionID: "sess-1"})
	assert.Equal(t, []string{stateWorking, stateIdle}, *states)
}

func TestPermissionBlockAndUnblock(t *testing.T) {
	t.Parallel()
	c, states := newTestClient(t)

	// Start working.
	c.HandleEvent(AssistantMessage{SessionID: "sess-1"})

	// Permission request blocks.
	c.HandleEvent(PermissionRequested{})
	assert.Equal(t, []string{stateWorking, stateBlocked}, *states)

	// Permission granted returns to working (run still active).
	c.HandleEvent(PermissionResolved{})
	assert.Equal(t, []string{stateWorking, stateBlocked, stateWorking}, *states)

	// Run complete returns to idle.
	c.HandleEvent(RunComplete{SessionID: "sess-1"})
	assert.Equal(t, []string{stateWorking, stateBlocked, stateWorking, stateIdle}, *states)
}

func TestPermissionBeforeAssistantMessage(t *testing.T) {
	t.Parallel()
	c, states := newTestClient(t)

	// Permission request arrives before any assistant message.
	// This can happen when tool calls fire before text output.
	c.HandleEvent(PermissionRequested{})
	assert.Equal(t, []string{stateBlocked}, *states)

	// Permission resolved should return to working, not idle,
	// because the permission request implied a run was active.
	c.HandleEvent(PermissionResolved{})
	assert.Equal(t, []string{stateBlocked, stateWorking}, *states)
}

func TestSessionIDPropagation(t *testing.T) {
	t.Parallel()
	c, _ := newTestClient(t)

	// SetSessionID before events.
	c.SetSessionID("early-session")
	assert.Equal(t, "early-session", c.sessionID)

	// RunComplete also updates session ID.
	c.HandleEvent(RunComplete{SessionID: "final-session"})
	assert.Equal(t, "final-session", c.sessionID)
}

func TestDedupSkipsRedundantState(t *testing.T) {
	t.Parallel()
	c, states := newTestClient(t)

	// Two assistant messages in a row should only report working once.
	c.HandleEvent(AssistantMessage{SessionID: "s1"})
	c.HandleEvent(AssistantMessage{SessionID: "s1"})
	assert.Equal(t, []string{stateWorking}, *states)
}

func TestSummarizingTriggersWorking(t *testing.T) {
	t.Parallel()
	c, states := newTestClient(t)

	// Summarizing event should trigger working.
	c.HandleEvent(Summarizing{})
	assert.Equal(t, []string{stateWorking}, *states)

	// Second summarizing should not trigger another state change.
	c.HandleEvent(Summarizing{})
	assert.Equal(t, []string{stateWorking}, *states)
}

func TestNilClientSafe(t *testing.T) {
	t.Parallel()
	var c *Client
	// These should not panic on a nil receiver.
	c.SetSessionID("s1")
	c.HandleEvent(AssistantMessage{SessionID: "s1"})
	c.HandleEvent(RunComplete{SessionID: "s1"})
	c.HandleEvent(PermissionRequested{})
	c.HandleEvent(PermissionResolved{})
	c.HandleEvent(Summarizing{})
}

func TestRegisterInitial(t *testing.T) {
	t.Parallel()
	c, states := newTestClient(t)
	c.seq = 100
	c.registerInitial()
	assert.Equal(t, []string{stateIdle}, *states)
	// seq must strictly increase so herdr accepts the report.
	assert.Equal(t, uint64(101), c.seq)
}

// TestInitDisabledUnderTest guards the critical safety property that
// herdr never attaches to a real pane from a test binary. Test
// processes inherit the developer's HERDR_* environment, so a missing
// guard would release the live pane's agent on teardown. Because this
// test itself runs under `go test`, Init must return nil even with a
// complete, valid-looking environment.
func TestInitDisabledUnderTest(t *testing.T) {
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_SOCKET_PATH", "/tmp/does-not-matter.sock")
	t.Setenv("HERDR_PANE_ID", "test:pane")
	assert.Nil(t, newFromEnv())
}

func TestNewFromEnvMissingHerdrEnvReturnsNil(t *testing.T) {
	t.Setenv("HERDR_ENV", "")
	assert.Nil(t, newFromEnv())
}

// TestInitOutsideHerdrPaneReturnsNil exercises the process-wide Init
// singleton. Since flag.Lookup("test.v") is always non-nil under `go
// test`, newFromEnv is guaranteed to return nil regardless of ambient
// HERDR_* env vars, so this is deterministic across environments.
func TestInitOutsideHerdrPaneReturnsNil(t *testing.T) {
	assert.Nil(t, Init())
	// Second call must hit the cached (nil) singleton, not panic.
	assert.Nil(t, Init())
}

func TestRegisterInitialNilSafe(t *testing.T) {
	t.Parallel()
	var c *Client
	c.registerInitial()
}

// TestPermissionResolvedWithoutActiveRunReportsIdle exercises the
// runActive=false branch of onPermissionResolved. The client starts
// idle already, so reportLocked's dedup check swallows the send, but
// the branch itself still runs.
func TestPermissionResolvedWithoutActiveRunReportsIdle(t *testing.T) {
	t.Parallel()
	c, states := newTestClient(t)
	c.HandleEvent(PermissionResolved{})
	assert.Empty(t, *states)
}

func TestCloseNilSafe(t *testing.T) {
	t.Parallel()
	var c *Client
	c.Close()
}

// TestCloseReleasesAgentAndClosesSender points releaseAgent at a
// socket that does not exist. dialSend's error is logged and
// swallowed, so Close must still complete and still close the
// sender.
func TestCloseReleasesAgentAndClosesSender(t *testing.T) {
	t.Parallel()
	m := NewMockSender(gomock.NewController(t))
	m.EXPECT().close()
	c := &Client{
		socketPath: filepath.Join(t.TempDir(), "does-not-exist.sock"),
		paneID:     "pane-1",
		state:      stateIdle,
		snd:        m,
	}
	c.Close()
}

func TestUnixSenderDeliversOverSocket(t *testing.T) {
	t.Parallel()
	sockPath := filepath.Join(shortSocketDir(t), "s.sock")
	ln, err := net.Listen("unix", sockPath) //nolint:noctx
	require.NoError(t, err)
	defer ln.Close()

	received := make(chan reportRequest, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var req reportRequest
		if err := json.NewDecoder(conn).Decode(&req); err == nil {
			received <- req
		}
	}()

	s := newUnixSender(sockPath)
	defer s.close()

	require.NoError(t, s.send(reportRequest{
		ID:     "test-1",
		Method: "pane.report_agent",
		Params: reportParams{State: stateWorking},
	}))

	select {
	case req := <-received:
		assert.Equal(t, "test-1", req.ID)
		assert.Equal(t, stateWorking, req.Params.State)
	case <-time.After(2 * time.Second):
		t.Fatal("herdr mock listener never received the report")
	}
}

// TestUnixSenderSendDropsWhenBufferFull verifies that send never
// blocks: state reports are best-effort, so a full buffer must drop
// the newest request rather than stall the agent.
func TestUnixSenderSendDropsWhenBufferFull(t *testing.T) {
	t.Parallel()
	s := &unixSender{ch: make(chan reportRequest, 1)}
	require.NoError(t, s.send(reportRequest{ID: "1"}))
	require.NoError(t, s.send(reportRequest{ID: "2"}))

	got := <-s.ch
	assert.Equal(t, "1", got.ID)
	select {
	case extra := <-s.ch:
		t.Fatalf("expected buffer to contain only the first request, got %+v", extra)
	default:
	}
}

func TestDialSendConnectionRefused(t *testing.T) {
	t.Parallel()
	err := dialSend(filepath.Join(t.TempDir(), "does-not-exist.sock"), reportRequest{})
	require.Error(t, err)
}
