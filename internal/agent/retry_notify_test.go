package agent

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/notify"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// retryOnceModel fails its first Stream call with a retryable
// provider error, then succeeds, so a run through it triggers exactly
// one OnRetry callback.
type retryOnceModel struct {
	attempts atomic.Int32
}

func (m *retryOnceModel) Provider() string { return "fake" }
func (m *retryOnceModel) Model() string    { return "fake-model" }

func (m *retryOnceModel) Generate(context.Context, fantasy.Call) (*fantasy.Response, error) {
	return nil, errors.New("not implemented")
}

func (m *retryOnceModel) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *retryOnceModel) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *retryOnceModel) Stream(context.Context, fantasy.Call) (fantasy.StreamResponse, error) {
	if m.attempts.Add(1) == 1 {
		return nil, &fantasy.ProviderError{
			Title:      "Server overloaded",
			Message:    "the upstream provider is busy",
			StatusCode: http.StatusServiceUnavailable,
			ResponseHeaders: map[string]string{
				// A tiny explicit retry-after wins over the multi-second
				// exponential-backoff default, so the retry fires almost
				// immediately instead of slowing the test down.
				"retry-after-ms": "1",
			},
		}
	}
	return func(yield func(fantasy.StreamPart) bool) {
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "1"}) {
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "1", Delta: "ok"}) {
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "1"}) {
			return
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop})
	}, nil
}

// TestSessionAgentPublishesRetryNotification pins that a retried
// stream surfaces a TypeAgentRetrying notification carrying the
// attempt count and a non-empty reason drawn from the provider
// error's title. Without it, a slow-but-recovering connection reads
// as a hang: the whole retry-plus-backoff window passes with no
// visible sign of what is happening.
func TestSessionAgentPublishesRetryNotification(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	broker := pubsub.NewBroker[notify.Notification]()
	t.Cleanup(broker.Shutdown)

	subCtx, subCancel := context.WithCancel(t.Context())
	defer subCancel()
	events := broker.Subscribe(subCtx)

	titles := &coordinator{sessions: env.sessions, cfg: config.NewTestStore(&config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
		Options:   &config.Options{},
	})}
	sa := NewSessionAgent(SessionAgentOptions{
		IsYolo:        true,
		Sessions:      env.sessions,
		Messages:      env.messages,
		Notify:        broker,
		GenerateTitle: titles.generateSessionTitle,
	}).(*sessionAgent)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	model := &retryOnceModel{}
	_, err = sa.Run(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "hi",
		Agent: resolvedAgent{
			ID:        config.AgentCoder,
			Model:     Model{Model: model, CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}}},
			MaxTokens: 10000,
		},
	})
	require.NoError(t, err)
	require.Equal(t, int32(2), model.attempts.Load(), "the stream must run once, then once more on retry")

	notification := recvNotification(t, events)
	require.Equal(t, notify.TypeAgentRetrying, notification.Type)
	require.Equal(t, sess.ID, notification.SessionID)
	require.Equal(t, 1, notification.RetryAttempt)
	require.Equal(t, streamMaxRetries, notification.RetryMaxAttempts)
	require.NotEmpty(t, notification.Message, "a non-empty error title must surface as the retry reason")
}
