package herdr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

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
