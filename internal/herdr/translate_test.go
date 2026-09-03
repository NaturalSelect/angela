package herdr

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/agent/notify"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// Domain type translation.

func TestTranslateDomainAssistantMessage(t *testing.T) {
	t.Parallel()
	ev := pubsub.Event[message.Message]{
		Payload: message.Message{Role: message.Assistant, SessionID: "s1"},
	}
	assert.Equal(t, AssistantMessage{SessionID: "s1"}, Translate(ev))
}

func TestTranslateDomainSummaryMessage(t *testing.T) {
	t.Parallel()
	ev := pubsub.Event[message.Message]{
		Payload: message.Message{
			Role:             message.Assistant,
			SessionID:        "s1",
			IsSummaryMessage: true,
		},
	}
	assert.Equal(t, Summarizing{}, Translate(ev))
}

func TestTranslateDomainNonAssistantIgnored(t *testing.T) {
	t.Parallel()
	ev := pubsub.Event[message.Message]{
		Payload: message.Message{Role: message.System},
	}
	assert.Nil(t, Translate(ev))
}

func TestTranslateDomainRunComplete(t *testing.T) {
	t.Parallel()
	ev := pubsub.Event[notify.RunComplete]{
		Payload: notify.RunComplete{SessionID: "s1"},
	}
	assert.Equal(t, RunComplete{SessionID: "s1"}, Translate(ev))
}

func TestTranslateDomainPermissionRequest(t *testing.T) {
	t.Parallel()
	ev := pubsub.Event[permission.PermissionRequest]{
		Payload: permission.PermissionRequest{ToolName: "bash"},
	}
	assert.Equal(t, PermissionRequested{}, Translate(ev))
}

func TestTranslateDomainPermissionNotification(t *testing.T) {
	t.Parallel()
	ev := pubsub.Event[permission.PermissionNotification]{
		Payload: permission.PermissionNotification{Granted: true},
	}
	assert.Equal(t, PermissionResolved{}, Translate(ev))
}

// Proto type translation.

func TestTranslateProtoAssistantMessage(t *testing.T) {
	t.Parallel()
	ev := pubsub.Event[proto.Message]{
		Payload: proto.Message{Role: proto.Assistant, SessionID: "s1"},
	}
	assert.Equal(t, AssistantMessage{SessionID: "s1"}, Translate(ev))
}

func TestTranslateProtoNonAssistantIgnored(t *testing.T) {
	t.Parallel()
	ev := pubsub.Event[proto.Message]{
		Payload: proto.Message{Role: proto.User, SessionID: "s1"},
	}
	assert.Nil(t, Translate(ev))
}

func TestTranslateProtoRunComplete(t *testing.T) {
	t.Parallel()
	ev := pubsub.Event[proto.RunComplete]{
		Payload: proto.RunComplete{SessionID: "s1"},
	}
	assert.Equal(t, RunComplete{SessionID: "s1"}, Translate(ev))
}

func TestTranslateProtoPermissionRequest(t *testing.T) {
	t.Parallel()
	ev := pubsub.Event[proto.PermissionRequest]{
		Payload: proto.PermissionRequest{ToolName: "bash"},
	}
	assert.Equal(t, PermissionRequested{}, Translate(ev))
}

func TestTranslateProtoPermissionNotification(t *testing.T) {
	t.Parallel()
	ev := pubsub.Event[proto.PermissionNotification]{
		Payload: proto.PermissionNotification{Granted: true},
	}
	assert.Equal(t, PermissionResolved{}, Translate(ev))
}

func TestTranslateProtoSummarizing(t *testing.T) {
	t.Parallel()
	ev := pubsub.Event[proto.AgentEvent]{
		Payload: proto.AgentEvent{
			Type: proto.AgentEventTypeSummarize,
			Done: false,
		},
	}
	assert.Equal(t, Summarizing{}, Translate(ev))
}

func TestTranslateProtoSummarizeDoneIgnored(t *testing.T) {
	t.Parallel()
	ev := pubsub.Event[proto.AgentEvent]{
		Payload: proto.AgentEvent{
			Type: proto.AgentEventTypeSummarize,
			Done: true,
		},
	}
	assert.Nil(t, Translate(ev))
}

func TestTranslateProtoSessionIgnored(t *testing.T) {
	t.Parallel()
	ev := pubsub.Event[proto.Session]{
		Payload: proto.Session{ID: "s1"},
	}
	assert.Nil(t, Translate(ev))
}

// Unknown types.

func TestTranslateUnknownReturnsNil(t *testing.T) {
	t.Parallel()
	assert.Nil(t, Translate("not an event"))
}

// Bridge wiring.

func TestBridgeLocalNilClientNoop(t *testing.T) {
	t.Parallel()
	BridgeLocal(t.Context(), nil, BridgeSources{})
}

// fakePermNotifier adapts a permission-notification broker to the
// SubscribeNotifications shape BridgeLocal expects, mirroring the
// method permission.Service exposes in production.
type fakePermNotifier struct {
	broker *pubsub.Broker[permission.PermissionNotification]
}

func (f fakePermNotifier) SubscribeNotifications(ctx context.Context) <-chan pubsub.Event[permission.PermissionNotification] {
	return f.broker.Subscribe(ctx)
}

// newConcurrentTestClient is like newTestClient but safe for
// concurrent access: BridgeLocal and forward record state
// transitions from background goroutines while the test polls via
// require.Eventually on a separate goroutine.
func newConcurrentTestClient(t *testing.T) (*Client, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var states []string
	m := NewMockSender(gomock.NewController(t))
	m.EXPECT().send(gomock.Any()).DoAndReturn(func(req reportRequest) error {
		mu.Lock()
		defer mu.Unlock()
		states = append(states, req.Params.State)
		return nil
	}).AnyTimes()
	m.EXPECT().close().AnyTimes()
	snapshot := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), states...)
	}
	return &Client{state: stateIdle, snd: m}, snapshot
}

// TestBridgeLocalForwardsAllSources exercises BridgeLocal end to end
// with real brokers, verifying every one of the four sources is wired
// through Translate to the client's state machine.
func TestBridgeLocalForwardsAllSources(t *testing.T) {
	t.Parallel()
	c, states := newConcurrentTestClient(t)

	permReqBroker := pubsub.NewBroker[permission.PermissionRequest]()
	permNotifBroker := pubsub.NewBroker[permission.PermissionNotification]()
	runCompleteBroker := pubsub.NewBroker[notify.RunComplete]()
	messageBroker := pubsub.NewBroker[message.Message]()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	BridgeLocal(ctx, c, BridgeSources{
		PermRequests:      permReqBroker,
		PermNotifications: fakePermNotifier{broker: permNotifBroker},
		RunCompletions:    runCompleteBroker,
		Messages:          messageBroker,
	})

	// forward's subscribe call happens in its own goroutine, so there
	// is a brief window after BridgeLocal returns where the broker has
	// no subscriber yet. Re-publish on every poll until the client
	// observes the resulting state; once subscribed, further
	// publishes are no-ops thanks to the state machine's own dedup.
	waitForState := func(publish func(), want string) {
		t.Helper()
		require.Eventually(t, func() bool {
			publish()
			cur := states()
			return len(cur) > 0 && cur[len(cur)-1] == want
		}, time.Second, 5*time.Millisecond)
	}

	waitForState(func() {
		messageBroker.Publish(pubsub.CreatedEvent, message.Message{Role: message.Assistant, SessionID: "s1"})
	}, stateWorking)

	waitForState(func() {
		permReqBroker.Publish(pubsub.CreatedEvent, permission.PermissionRequest{})
	}, stateBlocked)

	waitForState(func() {
		permNotifBroker.Publish(pubsub.CreatedEvent, permission.PermissionNotification{})
	}, stateWorking)

	waitForState(func() {
		runCompleteBroker.Publish(pubsub.CreatedEvent, notify.RunComplete{SessionID: "s1"})
	}, stateIdle)
}

// TestForwardResubscribesOnClosedChannel drives forward's re-subscribe
// loop directly: the first subscription delivers one event and then
// closes, which must trigger a re-subscribe rather than forward
// exiting early.
func TestForwardResubscribesOnClosedChannel(t *testing.T) {
	t.Parallel()
	c, states := newConcurrentTestClient(t)

	var calls atomic.Int32
	subscribe := func(context.Context) <-chan pubsub.Event[permission.PermissionRequest] {
		n := calls.Add(1)
		ch := make(chan pubsub.Event[permission.PermissionRequest], 1)
		if n == 1 {
			ch <- pubsub.Event[permission.PermissionRequest]{Payload: permission.PermissionRequest{}}
			close(ch)
		}
		return ch
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		forward(ctx, c, subscribe)
		close(done)
	}()

	require.Eventually(t, func() bool {
		return calls.Load() >= 2
	}, time.Second, 5*time.Millisecond, "expected forward to resubscribe after the channel closed")

	require.Eventually(t, func() bool {
		cur := states()
		return len(cur) == 1 && cur[0] == stateBlocked
	}, time.Second, 5*time.Millisecond)

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("forward did not exit after context cancellation")
	}
}
