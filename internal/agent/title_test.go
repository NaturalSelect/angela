package agent

import (
	"context"
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
