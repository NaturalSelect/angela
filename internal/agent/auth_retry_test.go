package agent

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/notify"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// authRetryModel records which model instance each attempt ran on, so a
// test can tell a retry that reused the stale instance from one that
// picked up the rebuilt one.
type authRetryModel struct {
	id        string
	rejectAll bool

	mu       *sync.Mutex
	attempts *[]string
}

func (m *authRetryModel) Provider() string { return "fake" }
func (m *authRetryModel) Model() string    { return "fake-model" }

func (m *authRetryModel) Generate(context.Context, fantasy.Call) (*fantasy.Response, error) {
	return nil, errors.New("not implemented")
}

func (m *authRetryModel) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *authRetryModel) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *authRetryModel) Stream(context.Context, fantasy.Call) (fantasy.StreamResponse, error) {
	m.mu.Lock()
	*m.attempts = append(*m.attempts, m.id)
	m.mu.Unlock()

	if m.rejectAll {
		return nil, &fantasy.ProviderError{
			Message:    "token expired",
			StatusCode: http.StatusUnauthorized,
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

// TestAuthRefreshRetriesOnTheRebuiltModel pins that a 401 retry runs on
// the model rebuilt after credentials were refreshed. Refreshing writes
// new credentials into config, but the instance frozen at dispatch
// still holds the rejected ones — retrying with it just replays the
// 401, so every OAuth provider's recovery silently never worked.
func TestAuthRefreshRetriesOnTheRebuiltModel(t *testing.T) {
	t.Parallel()

	var (
		mu       sync.Mutex
		attempts []string
	)
	stale := &authRetryModel{id: "stale", rejectAll: true, mu: &mu, attempts: &attempts}
	fresh := &authRetryModel{id: "rebuilt", mu: &mu, attempts: &attempts}

	env := testEnv(t)
	broker := pubsub.NewBroker[notify.RunComplete]()
	t.Cleanup(broker.Shutdown)

	titles := &coordinator{sessions: env.sessions, cfg: config.NewTestStore(&config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
		Options:   &config.Options{},
	})}
	sa := NewSessionAgent(SessionAgentOptions{
		IsYolo:        true,
		Sessions:      env.sessions,
		Messages:      env.messages,
		RunComplete:   broker,
		GenerateTitle: titles.generateSessionTitle,
	}).(*sessionAgent)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	var refreshed int
	_, err = sa.Run(t.Context(), SessionAgentCall{
		SessionID: sess.ID,
		Prompt:    "hi",
		Agent: resolvedAgent{
			ID:        config.AgentCoder,
			Model:     Model{Model: stale, CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}},
			MaxTokens: 10000,
			RebuildModel: func(context.Context) (fantasy.LanguageModel, error) {
				return fresh, nil
			},
		},
		OnAuthRefresh: func(context.Context, *fantasy.ProviderError) error {
			refreshed++
			return nil
		},
	})
	require.NoError(t, err)

	require.Equal(t, 1, refreshed, "the 401 must trigger exactly one refresh")

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"stale", "rebuilt"}, attempts,
		"the retry must run on the model rebuilt from the refreshed credentials")
}
