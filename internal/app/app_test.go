package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/agent/notify"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/lsp"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// TestSetupSubscriber_NormalFlow verifies that events published to the source
// broker are forwarded to the output broker.
func TestSetupSubscriber_NormalFlow(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	src := pubsub.NewBroker[string]()
	defer src.Shutdown()
	out := pubsub.NewBroker[tea.Msg]()
	defer out.Shutdown()

	ch := out.Subscribe(ctx)

	var wg sync.WaitGroup
	setupSubscriber(ctx, &wg, "test", src.Subscribe, out)

	// Yield so the subscriber goroutine can call src.Subscribe before we publish.
	time.Sleep(10 * time.Millisecond)

	src.Publish(pubsub.CreatedEvent, "hello")
	src.Publish(pubsub.CreatedEvent, "world")

	for range 2 {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for forwarded event")
		}
	}

	cancel()
	wg.Wait()
}

// TestSetupSubscriber_ContextCancellation verifies the goroutine exits cleanly
// when the context is cancelled.
func TestSetupSubscriber_ContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	src := pubsub.NewBroker[string]()
	defer src.Shutdown()
	out := pubsub.NewBroker[tea.Msg]()
	defer out.Shutdown()

	var wg sync.WaitGroup
	setupSubscriber(ctx, &wg, "test", src.Subscribe, out)

	src.Publish(pubsub.CreatedEvent, "event")
	cancel()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("setupSubscriber goroutine did not exit after context cancellation")
	}
}

// TestEvents_ZeroConsumers verifies that publishing with no subscribers does
// not block or panic.
func TestEvents_ZeroConsumers(t *testing.T) {
	t.Parallel()

	broker := pubsub.NewBroker[tea.Msg]()
	defer broker.Shutdown()

	require.Equal(t, 0, broker.GetSubscriberCount())

	// Must not block.
	done := make(chan struct{})
	go func() {
		broker.Publish(pubsub.UpdatedEvent, tea.Msg("msg1"))
		broker.Publish(pubsub.UpdatedEvent, tea.Msg("msg2"))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish with zero consumers blocked")
	}
}

// TestEvents_OneConsumer verifies that a single subscriber receives every event
// exactly once.
func TestEvents_OneConsumer(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	broker := pubsub.NewBroker[tea.Msg]()
	defer broker.Shutdown()

	ch := broker.Subscribe(ctx)

	const n = 10
	for i := range n {
		broker.Publish(pubsub.UpdatedEvent, tea.Msg(i))
	}

	for i := range n {
		select {
		case ev := <-ch:
			require.Equal(t, tea.Msg(i), ev.Payload)
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for event %d", i)
		}
	}
}

// TestEvents_NConsumers verifies that every subscriber receives every event
// exactly once, regardless of how many concurrent consumers are attached.
func TestEvents_NConsumers(t *testing.T) {
	t.Parallel()

	for _, n := range []int{2, 5, 10} {
		t.Run(fmt.Sprintf("consumers=%d", n), func(t *testing.T) {
			t.Parallel()
			testNConsumers(t, n)
		})
	}
}

func testNConsumers(t *testing.T, n int) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	broker := pubsub.NewBroker[tea.Msg]()
	defer broker.Shutdown()

	// Subscribe all N consumers before publishing.
	channels := make([]<-chan pubsub.Event[tea.Msg], n)
	for i := range n {
		channels[i] = broker.Subscribe(ctx)
	}
	require.Equal(t, n, broker.GetSubscriberCount())

	const numEvents = 20
	for i := range numEvents {
		broker.Publish(pubsub.UpdatedEvent, tea.Msg(i))
	}

	// Each consumer must receive all numEvents messages.
	var wg sync.WaitGroup
	for i, ch := range channels {
		wg.Go(func() {
			for j := range numEvents {
				select {
				case ev := <-ch:
					require.Equal(t, tea.Msg(j), ev.Payload,
						"consumer %d: wrong payload for event %d", i, j)
				case <-time.After(5 * time.Second):
					t.Errorf("consumer %d: timed out waiting for event %d", i, j)
					return
				}
			}
		})
	}
	wg.Wait()
}

// TestApp_ConfigAndStore verifies the Config and Store accessors return
// exactly what the underlying ConfigStore holds.
func TestApp_ConfigAndStore(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	store := config.NewTestStore(cfg)
	app := &App{config: store}

	require.Same(t, cfg, app.Config())
	require.Same(t, store, app.Store())
}

// TestApp_SendEvent_DeliversToSubscriber verifies SendEvent publishes to
// every subscriber returned by Events, and that ReportCurrentSession is
// safe to call when no herdr client is attached (the common case in
// tests and outside a herdr pane).
func TestApp_SendEvent_DeliversToSubscriber(t *testing.T) {
	t.Parallel()

	app := NewForTest(t.Context())
	defer app.ShutdownForTest()

	app.ReportCurrentSession("session-1")

	ch := app.Events(t.Context())
	app.SendEvent(tea.Msg("hello"))

	select {
	case ev := <-ch:
		require.Equal(t, tea.Msg("hello"), ev.Payload)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for SendEvent to be delivered")
	}
}

// TestApp_AgentNotifications_FansIntoEvents verifies that publishing to
// the broker returned by AgentNotifications is observable through
// Events, exercising the fan-in wiring NewForTest sets up.
func TestApp_AgentNotifications_FansIntoEvents(t *testing.T) {
	t.Parallel()

	app := NewForTest(t.Context())
	defer app.ShutdownForTest()

	require.NotNil(t, app.AgentNotifications())

	ch := app.Events(t.Context())

	// Yield so NewForTest's internal fan-in goroutine can subscribe to
	// agentNotifications before we publish.
	time.Sleep(10 * time.Millisecond)

	app.AgentNotifications().Publish(pubsub.CreatedEvent, notify.Notification{
		SessionID: "sess-1",
		Type:      notify.TypeAgentError,
		Message:   "boom",
	})

	select {
	case ev := <-ch:
		wrapped, ok := ev.Payload.(pubsub.Event[notify.Notification])
		require.True(t, ok, "payload should be a pubsub.Event[notify.Notification], got %T", ev.Payload)
		require.Equal(t, "sess-1", wrapped.Payload.SessionID)
		require.Equal(t, notify.TypeAgentError, wrapped.Payload.Type)
		require.Equal(t, "boom", wrapped.Payload.Message)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for agent notification to fan into events")
	}
}

// TestApp_RunCompletions_FansIntoEvents verifies that publishing to the
// broker returned by RunCompletions is observable through Events.
func TestApp_RunCompletions_FansIntoEvents(t *testing.T) {
	t.Parallel()

	app := NewForTest(t.Context())
	defer app.ShutdownForTest()

	require.NotNil(t, app.RunCompletions())

	ch := app.Events(t.Context())

	// Yield so NewForTest's internal fan-in goroutine can subscribe to
	// runCompletions before we publish.
	time.Sleep(10 * time.Millisecond)

	app.RunCompletions().Publish(pubsub.UpdatedEvent, notify.RunComplete{
		SessionID: "sess-2",
		MessageID: "msg-1",
		Text:      "done",
	})

	select {
	case ev := <-ch:
		wrapped, ok := ev.Payload.(pubsub.Event[notify.RunComplete])
		require.True(t, ok, "payload should be a pubsub.Event[notify.RunComplete], got %T", ev.Payload)
		require.Equal(t, "sess-2", wrapped.Payload.SessionID)
		require.Equal(t, "msg-1", wrapped.Payload.MessageID)
		require.Equal(t, "done", wrapped.Payload.Text)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for run completion to fan into events")
	}
}

// TestApp_ShutdownForTest_ClosesEventsChannelAndIsIdempotent verifies
// that tearing down a NewForTest app closes existing Events
// subscriptions, and that calling it a second time does not panic.
func TestApp_ShutdownForTest_ClosesEventsChannelAndIsIdempotent(t *testing.T) {
	t.Parallel()

	app := NewForTest(t.Context())
	ch := app.Events(t.Context())

	app.ShutdownForTest()

	select {
	case _, ok := <-ch:
		require.False(t, ok, "events channel should be closed after ShutdownForTest")
	case <-time.After(5 * time.Second):
		t.Fatal("events channel was not closed after ShutdownForTest")
	}

	app.ShutdownForTest()
}

func TestApp_UpdateAgentModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		setup   func(t *testing.T) *App
		wantErr string
	}{
		{
			name: "nil coordinator",
			setup: func(t *testing.T) *App {
				return &App{}
			},
			wantErr: "agent configuration is missing",
		},
		{
			name: "coordinator succeeds",
			setup: func(t *testing.T) *App {
				coord := NewMockCoordinator(gomock.NewController(t))
				coord.EXPECT().UpdateModels(gomock.Any()).Return(nil)
				return &App{AgentCoordinator: coord}
			},
		},
		{
			name: "coordinator returns error",
			setup: func(t *testing.T) *App {
				coord := NewMockCoordinator(gomock.NewController(t))
				coord.EXPECT().UpdateModels(gomock.Any()).Return(errors.New("update failed"))
				return &App{AgentCoordinator: coord}
			},
			wantErr: "update failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := tt.setup(t)
			err := app.UpdateAgentModel(t.Context())

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestApp_InitCoderAgent_MissingCoderConfig verifies both the
// interactive and non-interactive coder-agent initializers reject a
// config with no coder agent defined, before ever touching
// agent.NewCoordinator.
func TestApp_InitCoderAgent_MissingCoderConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		init func(app *App, ctx context.Context) error
	}{
		{name: "interactive", init: (*App).InitCoderAgent},
		{name: "non-interactive", init: (*App).InitCoderAgentNonInteractive},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := &App{config: config.NewTestStore(&config.Config{})}
			err := tt.init(app, t.Context())
			require.ErrorContains(t, err, "coder agent configuration is missing")
		})
	}
}

// TestApp_Shutdown_CancelsCoordinatorAndRunsCleanup verifies that
// Shutdown cancels the agent coordinator, tolerates a nil herdr client
// and nil Messages service, tears down an LSP manager with no clients,
// and runs every registered cleanup function.
func TestApp_Shutdown_CancelsCoordinatorAndRunsCleanup(t *testing.T) {
	t.Parallel()

	coord := NewMockCoordinator(gomock.NewController(t))
	coord.EXPECT().CancelAll()

	store := config.NewTestStore(&config.Config{})
	app := &App{
		AgentCoordinator: coord,
		LSPManager:       lsp.NewManager(store),
	}

	var cleaned bool
	app.cleanupFuncs = []func(context.Context) error{
		func(context.Context) error {
			cleaned = true
			return nil
		},
	}

	app.Shutdown()

	require.True(t, cleaned, "registered cleanup function should have run")
}

func newProviderConfigStore(providers map[string]config.ProviderConfig) *config.ConfigStore {
	m := csync.NewMap[string, config.ProviderConfig]()
	for name, p := range providers {
		m.Set(name, p)
	}
	return config.NewTestStore(&config.Config{Providers: m})
}

// TestApp_ApplyModelOverrides covers the branches of applyModelOverrides:
// resolving only the large model, only the small (chore) model, both,
// and the error paths for an unknown model or provider. It also checks
// that a failed large-model resolution never leaves a partially applied
// chore override, per the "resolve both before applying either"
// invariant documented on the function.
func TestApp_ApplyModelOverrides(t *testing.T) {
	t.Parallel()

	providers := map[string]config.ProviderConfig{
		"openai": {
			ID:     "openai",
			Models: []config.ProviderModel{{Model: catwalk.Model{ID: "gpt-4o"}}},
		},
		"anthropic": {
			ID:     "anthropic",
			Models: []config.ProviderModel{{Model: catwalk.Model{ID: "claude-3-opus"}}},
		},
	}

	tests := []struct {
		name         string
		large, small string
		wantOverride *config.SelectedModel
		wantErr      string
		wantChoreSet bool
		wantChore    config.SelectedModel
	}{
		{
			name:         "large only",
			large:        "openai/gpt-4o",
			wantOverride: &config.SelectedModel{Provider: "openai", Model: "gpt-4o"},
		},
		{
			name:         "small only applies chore override and returns nil",
			small:        "anthropic/claude-3-opus",
			wantChoreSet: true,
			wantChore:    config.SelectedModel{Provider: "anthropic", Model: "claude-3-opus"},
		},
		{
			name:         "both large and small",
			large:        "openai/gpt-4o",
			small:        "anthropic/claude-3-opus",
			wantOverride: &config.SelectedModel{Provider: "openai", Model: "gpt-4o"},
			wantChoreSet: true,
			wantChore:    config.SelectedModel{Provider: "anthropic", Model: "claude-3-opus"},
		},
		{
			name:    "large not found leaves chore untouched",
			large:   "nonexistent-model",
			small:   "anthropic/claude-3-opus",
			wantErr: "not found",
		},
		{
			name:    "unknown provider prefix",
			large:   "bogus-provider/gpt-4o",
			wantErr: "provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newProviderConfigStore(providers)
			app := &App{config: store}

			got, err := app.applyModelOverrides(tt.large, tt.small)

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.wantOverride, got)

			chore, ok := store.Config().ModelForSlot(config.SlotChore)
			require.Equal(t, tt.wantChoreSet, ok)
			if tt.wantChoreSet {
				require.Equal(t, tt.wantChore, chore)
			}
		})
	}
}
