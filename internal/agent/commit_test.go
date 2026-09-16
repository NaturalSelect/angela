package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/stretchr/testify/require"
)

// TestGenerateCommitMessageRejectsEmptyDiff pins that a blank staged
// diff never reaches agent resolution at all: there is nothing to
// describe, so the zero-value coordinator below (no config, no
// sessions) is enough to prove the check runs first.
func TestGenerateCommitMessageRejectsEmptyDiff(t *testing.T) {
	t.Parallel()

	coord := &coordinator{}
	msg, err := coord.GenerateCommitMessage(t.Context(), "sess", "   \n\t")
	require.Empty(t, msg)
	require.ErrorContains(t, err, "no staged changes to describe")
}

// TestGenerateCommitMessageFailsWhenCommitAgentIsNotConfigured pins
// that an unresolvable commit agent surfaces as a wrapped error
// instead of panicking or silently falling back, mirroring
// generateSessionTitle's equivalent failure mode.
func TestGenerateCommitMessageFailsWhenCommitAgentIsNotConfigured(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	coord := &coordinator{sessions: env.sessions, cfg: config.NewTestStore(&config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
		Options:   &config.Options{},
	})}

	msg, err := coord.GenerateCommitMessage(t.Context(), "sess", "diff --git a/x b/x\n+added line")
	require.Empty(t, msg)
	require.ErrorContains(t, err, "failed to resolve the commit agent")
}

// TestGenerateCommitMessageReturnsProviderErrorDirectly pins that once
// the commit agent resolves cleanly, a transport failure comes back
// to the caller as-is: unlike titling, there is no deferred fallback
// here to paper over it, so the /commit command can report the real
// error.
func TestGenerateCommitMessageReturnsProviderErrorDirectly(t *testing.T) {
	t.Parallel()

	coord := newModelPrefTestCoordinator(t, nil)

	// The mock provider points at an unreachable address; bound the
	// call so the SDK's connection-retry backoff doesn't stall the
	// test.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := coord.GenerateCommitMessage(ctx, "", "diff --git a/x b/x\n+added line")
	require.Empty(t, msg)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "failed to resolve the commit agent",
		"a resolvable agent's transport failure must not be re-wrapped as a resolution error")
}

// setMockProviderBaseURL points newModelPrefTestCoordinator's shared
// "mock" provider at url, letting a test serve both the main and
// chore slots from a local httptest.Server.
func setMockProviderBaseURL(t *testing.T, coord *coordinator, url string) {
	t.Helper()
	p, ok := coord.cfg.Config().Providers.Get("mock")
	require.True(t, ok)
	p.BaseURL = url + "/v1"
	coord.cfg.Config().Providers.Set("mock", p)
}

// commitCompletionServer serves a single chat-completions SSE
// response carrying content, and — when capturedBody is non-nil —
// records the raw request body so a test can inspect what the model
// actually received.
func commitCompletionServer(t *testing.T, content string, capturedBody *[]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capturedBody != nil {
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			*capturedBody = body
		}
		encoded, err := json.Marshal(content)
		require.NoError(t, err)

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: "+`{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"}}]}`+"\n\n")
		fmt.Fprintf(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":%s}}]}\n\n", encoded)
		fmt.Fprint(w, "data: "+`{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

// TestGenerateCommitMessageRejectsEmptyModelResponse pins that a model
// reply with nothing but whitespace is treated as a failure rather
// than handed to the caller as an empty commit message.
func TestGenerateCommitMessageRejectsEmptyModelResponse(t *testing.T) {
	t.Parallel()

	server := commitCompletionServer(t, "   ", nil)
	defer server.Close()

	coord := newModelPrefTestCoordinator(t, nil)
	setMockProviderBaseURL(t, coord, server.URL)

	msg, err := coord.GenerateCommitMessage(t.Context(), "", "diff --git a/x b/x\n+added line")
	require.Empty(t, msg)
	require.ErrorContains(t, err, "commit message generation returned an empty message")
}

// TestGenerateCommitMessageTruncatesLongDiffsAndStripsBackticks pins
// two independent behaviors of a successful generation: an
// over-limit diff is truncated before it ever reaches the model (so a
// huge staged changeset cannot blow past context limits), and a
// response the model wrapped in backticks comes back clean.
func TestGenerateCommitMessageTruncatesLongDiffsAndStripsBackticks(t *testing.T) {
	t.Parallel()

	var gotBody []byte
	server := commitCompletionServer(t, "`fix: prevent crash on empty input`", &gotBody)
	defer server.Close()

	coord := newModelPrefTestCoordinator(t, nil)
	setMockProviderBaseURL(t, coord, server.URL)

	diff := "diff --git a/x b/x\n" + strings.Repeat("+added line\n", 2000)
	require.Greater(t, len(diff), maxCommitDiffChars, "precondition: the diff must exceed the truncation limit")

	msg, err := coord.GenerateCommitMessage(t.Context(), "", diff)
	require.NoError(t, err)
	require.Equal(t, "fix: prevent crash on empty input\n\nGenerated with Angela\n\nAssisted-by: Angela:Small", msg,
		"surrounding backticks must be stripped from the model's response, and the default attribution trailer must be appended")

	require.Contains(t, string(gotBody), "(diff truncated)",
		"an over-limit diff must be truncated before being sent to the model")
	require.NotContains(t, string(gotBody), diff,
		"the full untruncated diff must never reach the model")
}

// TestGenerateCommitMessageAttributionCreditsTheHostAgent pins that
// the commit trailer names the model that actually wrote the staged
// changes (sessionID's own agent), not the cheap internal agent that
// only drafted the message text — the same bug bash_attribution_test.go
// pins for the bash tool's identical trailer.
func TestGenerateCommitMessageAttributionCreditsTheHostAgent(t *testing.T) {
	server := commitCompletionServer(t, "fix: add y", nil)
	defer server.Close()

	coord := newModelPrefTestCoordinator(t, nil)
	setMockProviderBaseURL(t, coord, server.URL)

	// newModelPrefTestCoordinator pins "coder" to the chore slot for
	// its own model-preference tests; put it back on the main slot so
	// the session's host agent (large-model) disagrees with the
	// commit agent's own chore-slot model (small-model).
	agentCfg := coord.cfg.Config().Agents[config.AgentCoder]
	agentCfg.Slot = config.SlotMain
	coord.cfg.Config().Agents[config.AgentCoder] = agentCfg

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	msg, err := coord.GenerateCommitMessage(t.Context(), sess.ID, "diff --git a/x b/x\n+y")
	require.NoError(t, err)
	require.Contains(t, msg, "Assisted-by: Angela:Large",
		"the trailer must credit the model that wrote the staged changes")
	require.NotContains(t, msg, "Assisted-by: Angela:Small",
		"the commit-drafting agent's own model must not be credited for code it did not write")
}

// TestGenerateCommitMessageAppliesProviderSystemPromptPrefix pins
// that a provider configured with a system prompt prefix (some
// OpenAI-compatible gateways require one ahead of every request) has
// it injected via PrepareStep, matching generateSessionTitle's
// identical hook.
func TestGenerateCommitMessageAppliesProviderSystemPromptPrefix(t *testing.T) {
	t.Parallel()

	var gotBody []byte
	server := commitCompletionServer(t, "fix: add y", &gotBody)
	defer server.Close()

	coord := newModelPrefTestCoordinator(t, nil)
	p, ok := coord.cfg.Config().Providers.Get("mock")
	require.True(t, ok)
	p.BaseURL = server.URL + "/v1"
	p.SystemPromptPrefix = "ALWAYS ANSWER IN ENGLISH."
	coord.cfg.Config().Providers.Set("mock", p)

	msg, err := coord.GenerateCommitMessage(t.Context(), "", "diff --git a/x b/x\n+y")
	require.NoError(t, err)
	require.Equal(t, "fix: add y\n\nGenerated with Angela\n\nAssisted-by: Angela:Small", msg)
	require.Contains(t, string(gotBody), "ALWAYS ANSWER IN ENGLISH.",
		"the provider's system prompt prefix must be prepended to the request")
}
