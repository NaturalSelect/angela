package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/stretchr/testify/require"
)

// TestTitleMaxTokens pins the three-way precedence: a reasoning model
// always gets its full default budget (thinking needs the room), a
// non-reasoning model honors an explicit agent override, and otherwise
// falls back to the model's own default.
func TestTitleMaxTokens(t *testing.T) {
	t.Parallel()

	reasoning := Model{CatwalkCfg: config.ProviderModel{Model: catwalk.Model{
		CanReason:        true,
		DefaultMaxTokens: 8000,
	}}}
	require.Equal(t, int64(8000), titleMaxTokens(config.Agent{MaxTokens: ptrTo(int64(40))}, reasoning),
		"a reasoning model must keep its full budget even with an agent override")

	withOverride := Model{CatwalkCfg: config.ProviderModel{Model: catwalk.Model{DefaultMaxTokens: 8000}}}
	require.Equal(t, int64(40), titleMaxTokens(config.Agent{MaxTokens: ptrTo(int64(40))}, withOverride),
		"a non-reasoning model must honor the agent's own cap")

	require.Equal(t, int64(8000), titleMaxTokens(config.Agent{}, withOverride),
		"with no override, the model's own default applies")

	require.Equal(t, int64(8000), titleMaxTokens(config.Agent{MaxTokens: ptrTo(int64(0))}, withOverride),
		"a zero override must not disable the model default")
}

// TestGenerateSessionTitleEmptyPromptIsANoOp pins that an empty user
// prompt never touches the session at all, not even the fallback name.
func TestGenerateSessionTitleEmptyPromptIsANoOp(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	coord := &coordinator{sessions: env.sessions, cfg: config.NewTestStore(&config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
		Options:   &config.Options{},
	})}

	sess, err := env.sessions.Create(t.Context(), "Original Title")
	require.NoError(t, err)

	coord.generateSessionTitle(t.Context(), sess.ID, "")

	after, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "Original Title", after.Title)
}

// TestGenerateSessionTitleFallsBackWhenTitleAgentIsNotConfigured pins
// that a session still gets a usable name when the title agent cannot
// be resolved at all (e.g. no coder agent configured).
func TestGenerateSessionTitleFallsBackWhenTitleAgentIsNotConfigured(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	coord := &coordinator{sessions: env.sessions, cfg: config.NewTestStore(&config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
		Options:   &config.Options{},
	})}

	sess, err := env.sessions.Create(t.Context(), "Original Title")
	require.NoError(t, err)

	coord.generateSessionTitle(t.Context(), sess.ID, "help me fix a bug")

	after, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, DefaultSessionName, after.Title)
}

// TestGenerateSessionTitleFallsBackWhenTheProviderIsUnreachable pins
// the deferred-fallback guarantee end to end: the title agent resolves
// cleanly (model, system prompt, everything), but the completion call
// itself fails, and the session must still land on the default title
// rather than being left titleless.
func TestGenerateSessionTitleFallsBackWhenTheProviderIsUnreachable(t *testing.T) {
	t.Parallel()

	coord := newModelPrefTestCoordinator(t, nil)

	sess, err := coord.sessions.Create(t.Context(), "Original Title")
	require.NoError(t, err)

	// The mock provider points at an unreachable address; bound the call
	// so the SDK's connection-retry backoff doesn't stall the test.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	coord.generateSessionTitle(ctx, sess.ID, "help me fix a bug")

	after, err := coord.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, DefaultSessionName, after.Title)
}

func userMessage(parts ...message.ContentPart) message.Message {
	return message.Message{Role: message.User, Parts: parts}
}

// TestOpeningPrompt pins which text a retried title is derived from: the
// first real prompt wins, a shell command only stands in when no prompt
// exists, and reminders and assistant output are never a source.
func TestOpeningPrompt(t *testing.T) {
	t.Parallel()

	shell := userMessage(message.ShellCommand{Command: "ls -la"})
	prompt := userMessage(message.TextContent{Text: "  help me fix a bug  "})
	reminder := userMessage(message.TextContent{Text: "<system-reminder>\nTodo list\n</system-reminder>"})
	assistant := message.Message{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "sure"}}}

	tests := []struct {
		name string
		msgs []message.Message
		want string
	}{
		{name: "empty session", want: ""},
		{name: "first prompt, trimmed", msgs: []message.Message{prompt}, want: "help me fix a bug"},
		{name: "prompt beats an earlier shell command", msgs: []message.Message{shell, prompt}, want: "help me fix a bug"},
		{name: "shell command when there is no prompt", msgs: []message.Message{shell}, want: "$ ls -la"},
		{name: "skips reminders", msgs: []message.Message{reminder, prompt}, want: "help me fix a bug"},
		{name: "ignores assistant text", msgs: []message.Message{assistant}, want: ""},
		{name: "blank prompt is not a prompt", msgs: []message.Message{userMessage(message.TextContent{Text: "   "})}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, openingPrompt(tt.msgs))
		})
	}
}

func createUserPrompt(t *testing.T, coord *coordinator, sessionID, text string) {
	t.Helper()
	_, err := coord.messages.Create(t.Context(), sessionID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: text}},
	})
	require.NoError(t, err)
}

// TestRegenerateTitleWithoutAPromptKeepsTheTitle pins that a session with
// nothing to title is refused with a typed error and left untouched.
func TestRegenerateTitleWithoutAPromptKeepsTheTitle(t *testing.T) {
	t.Parallel()

	coord := newModelPrefTestCoordinator(t, nil)
	sess, err := coord.sessions.Create(t.Context(), "Original Title")
	require.NoError(t, err)

	require.ErrorIs(t, coord.RegenerateTitle(t.Context(), sess.ID), ErrNoTitlePrompt)

	after, err := coord.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "Original Title", after.Title)
}

// TestRegenerateTitleReportsProviderFailureAndKeepsTheTitle is the point
// of the manual retry: unlike the automatic path, a failed attempt must
// reach the caller and must not overwrite the title with the placeholder.
func TestRegenerateTitleReportsProviderFailureAndKeepsTheTitle(t *testing.T) {
	t.Parallel()

	coord := newModelPrefTestCoordinator(t, nil)
	sess, err := coord.sessions.Create(t.Context(), "Original Title")
	require.NoError(t, err)
	createUserPrompt(t, coord, sess.ID, "help me fix a bug")

	// The mock provider points at an unreachable address; bound the call
	// so the SDK's connection-retry backoff doesn't stall the test.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	require.Error(t, coord.RegenerateTitle(ctx, sess.ID))

	after, err := coord.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "Original Title", after.Title)
}

// TestRegenerateTitleReplacesThePlaceholder covers the recovery the
// command exists for: a session stuck on the placeholder title gets a
// real one, built from the prompt that opened it.
func TestRegenerateTitleReplacesThePlaceholder(t *testing.T) {
	t.Parallel()

	var body []byte
	server := commitCompletionServer(t, "Fix login bug", &body)
	defer server.Close()

	coord := newModelPrefTestCoordinator(t, nil)
	setMockProviderBaseURL(t, coord, server.URL)

	sess, err := coord.sessions.Create(t.Context(), DefaultSessionName)
	require.NoError(t, err)
	createUserPrompt(t, coord, sess.ID, "the login form rejects valid passwords")
	createUserPrompt(t, coord, sess.ID, "a later message that must not be titled")

	require.NoError(t, coord.RegenerateTitle(t.Context(), sess.ID))

	after, err := coord.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "Fix login bug", after.Title)
	require.Contains(t, string(body), "the login form rejects valid passwords")
	require.NotContains(t, string(body), "a later message that must not be titled")
}

// writeTitleCompletion answers a chat-completions request with a streamed
// reply carrying content and a final usage chunk, so the title call has
// tokens to report.
func writeTitleCompletion(w http.ResponseWriter, content string, promptTokens, completionTokens int) {
	encoded, _ := json.Marshal(content)
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "data: "+`{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"}}]}`+"\n\n")
	fmt.Fprintf(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":%s}}]}\n\n", encoded)
	fmt.Fprint(w, "data: "+`{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
	fmt.Fprintf(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[],\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":%d,\"total_tokens\":%d}}\n\n",
		promptTokens, completionTokens, promptTokens+completionTokens)
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// priceMockModels gives every model of the shared mock provider a price,
// so a title call has a cost to report.
func priceMockModels(t *testing.T, coord *coordinator) {
	t.Helper()
	p, ok := coord.cfg.Config().Providers.Get("mock")
	require.True(t, ok)
	for i := range p.Models {
		p.Models[i].CostPer1MIn = 1000
		p.Models[i].CostPer1MOut = 1000
	}
	coord.cfg.Config().Providers.Set("mock", p)
}

// markBusy registers a running turn for sessionID on the coordinator's
// primary agent and returns a func that ends it.
func markBusy(t *testing.T, coord *coordinator, sessionID string) func() {
	t.Helper()
	sa, ok := coord.currentAgent.(*sessionAgent)
	require.True(t, ok)
	ac := &activeCancel{cancel: func() {}}
	sa.activeRequests.Set(sessionID, ac)
	return func() { sa.activeRequests.CompareAndDelete(sessionID, ac) }
}

// TestRegenerateTitleLeavesTheContextCountersAlone pins that the title
// call's tokens are not added to the session's token counters. Those hold
// the current context size, so adding to them on an idle session in the
// middle of its life inflates the context figure until the next turn.
func TestRegenerateTitleLeavesTheContextCountersAlone(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTitleCompletion(w, "Fix login bug", 120, 7)
	}))
	defer server.Close()

	coord := newModelPrefTestCoordinator(t, nil)
	setMockProviderBaseURL(t, coord, server.URL)
	priceMockModels(t, coord)

	sess, err := coord.sessions.Create(t.Context(), DefaultSessionName)
	require.NoError(t, err)
	sess.PromptTokens = 50000
	sess.CompletionTokens = 300
	_, err = coord.sessions.Save(t.Context(), sess)
	require.NoError(t, err)
	createUserPrompt(t, coord, sess.ID, "the login form rejects valid passwords")

	require.NoError(t, coord.RegenerateTitle(t.Context(), sess.ID))

	after, err := coord.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "Fix login bug", after.Title)
	require.Equal(t, int64(50000), after.PromptTokens, "the title call must not grow the context size")
	require.Equal(t, int64(300), after.CompletionTokens, "the title call must not grow the context size")
	require.Positive(t, after.Cost, "the title call is still paid for, so its cost is recorded")
}

// TestRegenerateTitleRefusedWhileTheSessionIsBusy pins the guard at the
// coordinator, so a client that skips the UI check cannot bypass it. The
// refusal must come before the model call, which would be wasted.
func TestRegenerateTitleRefusedWhileTheSessionIsBusy(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writeTitleCompletion(w, "Fix login bug", 1, 1)
	}))
	defer server.Close()

	coord := newModelPrefTestCoordinator(t, nil)
	setMockProviderBaseURL(t, coord, server.URL)

	sess, err := coord.sessions.Create(t.Context(), "Original Title")
	require.NoError(t, err)
	createUserPrompt(t, coord, sess.ID, "the login form rejects valid passwords")
	defer markBusy(t, coord, sess.ID)()

	require.ErrorIs(t, coord.RegenerateTitle(t.Context(), sess.ID), ErrSessionBusy)
	require.Zero(t, requests.Load(), "a refused request must not pay for a model call")

	after, err := coord.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "Original Title", after.Title)
}

// TestRegenerateTitleLosesTheRaceToATurn covers the window the up-front
// check cannot: the session is idle when the request starts and busy by
// the time the model answers. Compaction writes the whole session row
// back when it finishes, so saving the title now would be undone.
func TestRegenerateTitleLosesTheRaceToATurn(t *testing.T) {
	t.Parallel()

	var endTurn func()
	coord := newModelPrefTestCoordinator(t, nil)
	sess, err := coord.sessions.Create(t.Context(), "Original Title")
	require.NoError(t, err)
	createUserPrompt(t, coord, sess.ID, "the login form rejects valid passwords")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		endTurn = markBusy(t, coord, sess.ID)
		writeTitleCompletion(w, "Fix login bug", 1, 1)
	}))
	defer server.Close()
	setMockProviderBaseURL(t, coord, server.URL)

	require.ErrorIs(t, coord.RegenerateTitle(t.Context(), sess.ID), ErrSessionBusy)
	require.NotNil(t, endTurn, "the model must have been called before the session turned busy")
	endTurn()

	after, err := coord.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "Original Title", after.Title)
}

// TestRegenerateTitleWaitsForTheAutomaticTitle pins that a regeneration
// cannot land while the first-prompt title call is still running. That
// call is detached from the turn and renames the session to the
// placeholder if it fails, which would undo the regenerated title.
func TestRegenerateTitleWaitsForTheAutomaticTitle(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseRequest := func() { releaseOnce.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			close(entered)
			<-release
			writeTitleCompletion(w, "Automatic title", 1, 1)
			return
		}
		writeTitleCompletion(w, "Manual title", 1, 1)
	}))
	// NOTE: Deferred after server.Close so it runs first, because Close
	// blocks on the parked request.
	defer server.Close()
	defer releaseRequest()

	coord := newModelPrefTestCoordinator(t, nil)
	setMockProviderBaseURL(t, coord, server.URL)
	sess, err := coord.sessions.Create(t.Context(), DefaultSessionName)
	require.NoError(t, err)
	createUserPrompt(t, coord, sess.ID, "the login form rejects valid passwords")

	done := make(chan struct{})
	go func() {
		defer close(done)
		coord.generateSessionTitle(t.Context(), sess.ID, "the login form rejects valid passwords")
	}()
	<-entered

	require.ErrorIs(t, coord.RegenerateTitle(t.Context(), sess.ID), ErrSessionBusy)
	require.Equal(t, int32(1), requests.Load(), "a refused request must not pay for a model call")

	releaseRequest()
	<-done

	require.NoError(t, coord.RegenerateTitle(t.Context(), sess.ID), "the automatic title is finished, so the session is free again")
	after, err := coord.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "Manual title", after.Title)
}

// TestAutomaticTitleWaitsForARegeneration pins that the first-prompt title
// is not dropped because a manual regeneration is running. That
// regeneration can still fail, here because a turn starts, and then
// nothing else would ever name the session.
func TestAutomaticTitleWaitsForARegeneration(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseRequest := func() { releaseOnce.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			close(entered)
			<-release
			writeTitleCompletion(w, "Manual title", 1, 1)
			return
		}
		writeTitleCompletion(w, "Automatic title", 1, 1)
	}))
	// NOTE: Deferred after server.Close so it runs first, because Close
	// blocks on the parked request.
	defer server.Close()
	defer releaseRequest()

	coord := newModelPrefTestCoordinator(t, nil)
	setMockProviderBaseURL(t, coord, server.URL)
	sess, err := coord.sessions.Create(t.Context(), DefaultSessionName)
	require.NoError(t, err)
	createUserPrompt(t, coord, sess.ID, "the login form rejects valid passwords")

	regenerated := make(chan error, 1)
	go func() { regenerated <- coord.RegenerateTitle(t.Context(), sess.ID) }()
	<-entered
	defer markBusy(t, coord, sess.ID)()

	automatic := make(chan struct{})
	go func() {
		defer close(automatic)
		coord.generateSessionTitle(t.Context(), sess.ID, "the login form rejects valid passwords")
	}()
	select {
	case <-automatic:
		require.FailNow(t, "the automatic title must wait for the regeneration instead of giving up")
	case <-time.After(100 * time.Millisecond):
	}

	releaseRequest()
	require.ErrorIs(t, <-regenerated, ErrSessionBusy, "the turn that started meanwhile makes the regeneration lose")
	<-automatic

	after, err := coord.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "Automatic title", after.Title)
}

// TestAutomaticTitleKeepsARegeneratedTitleWhenItFails pins that an
// automatic title which had to wait for a regeneration does not undo it.
// The regeneration succeeds, then the automatic call fails, and its
// placeholder fallback would rename the session back to the default.
func TestAutomaticTitleKeepsARegeneratedTitleWhenItFails(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseRequest := func() { releaseOnce.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			close(entered)
			<-release
			writeTitleCompletion(w, "Manual title", 1, 1)
			return
		}
		http.Error(w, "boom", http.StatusBadRequest)
	}))
	// NOTE: Deferred after server.Close so it runs first, because Close
	// blocks on the parked request.
	defer server.Close()
	defer releaseRequest()

	coord := newModelPrefTestCoordinator(t, nil)
	setMockProviderBaseURL(t, coord, server.URL)
	sess, err := coord.sessions.Create(t.Context(), DefaultSessionName)
	require.NoError(t, err)
	createUserPrompt(t, coord, sess.ID, "the login form rejects valid passwords")

	regenerated := make(chan error, 1)
	go func() { regenerated <- coord.RegenerateTitle(t.Context(), sess.ID) }()
	<-entered

	automatic := make(chan struct{})
	go func() {
		defer close(automatic)
		coord.generateSessionTitle(t.Context(), sess.ID, "the login form rejects valid passwords")
	}()
	select {
	case <-automatic:
		require.FailNow(t, "the automatic title must wait for the regeneration")
	case <-time.After(100 * time.Millisecond):
	}

	releaseRequest()
	require.NoError(t, <-regenerated)
	<-automatic
	require.GreaterOrEqual(t, requests.Load(), int32(2), "the automatic title must have tried and failed")

	after, err := coord.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "Manual title", after.Title)
}

// TestTitleClaimsAwaitGivesUpWhenTheContextEnds pins that waiting for a
// session's title never outlives the caller.
func TestTitleClaimsAwaitGivesUpWhenTheContextEnds(t *testing.T) {
	t.Parallel()

	var claims titleClaims
	require.True(t, claims.claim("s1"))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	waited, err := claims.await(ctx, "s1")
	require.ErrorIs(t, err, context.Canceled)
	require.True(t, waited, "the session was held, so the caller did have to wait")

	claims.release("s1")
	waited, err = claims.await(t.Context(), "s1")
	require.NoError(t, err, "a released session can be claimed again")
	require.False(t, waited, "nobody held it, so there was nothing to wait for")
	require.False(t, claims.claim("s1"), "the second owner holds it now")
}
