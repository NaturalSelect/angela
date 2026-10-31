package acp

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/acp/protocol"
	"github.com/NaturalSelect/angela/internal/agent"
	"github.com/NaturalSelect/angela/internal/agent/notify"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/pubsub"
)

func (h *harness) traceHas(step string) bool {
	var found bool
	h.ws.snapshot(func() {
		for _, got := range h.ws.trace {
			found = found || got == step
		}
	})
	return found
}

// replies returns the text of every agent message sent for a session.
func (h *harness) replies(sessionID string) []string {
	var out []string
	for _, u := range h.client.updates(sessionID) {
		if chunk, ok := u.(protocol.AgentMessageChunk); ok {
			out = append(out, chunk.Content.(protocol.TextBlock).Text)
		}
	}
	return out
}

func TestBuiltinCommandsAnswerWithoutTheModel(t *testing.T) {
	t.Parallel()

	t.Run("compact summarizes the session and says so", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sessionID := h.newSession(t)

		resp, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/compact"))

		require.NoError(t, err)
		require.Equal(t, protocol.StopReasonEndTurn, resp.StopReason)
		require.True(t, h.traceHas("summarize"))
		require.False(t, h.ran())
		require.Equal(t, []string{"Session compacted."}, h.replies(sessionID))
	})

	t.Run("compact names the session it was asked about", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sessionID := h.newSession(t)
		var asked string
		h.ws.summarize = func(_ context.Context, id string) error {
			asked = id
			return nil
		}

		_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/compact"))

		require.NoError(t, err)
		require.Equal(t, sessionID, asked)
	})

	t.Run("a busy session is reported in plain words", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sessionID := h.newSession(t)
		h.ws.summarize = func(context.Context, string) error { return fmt.Errorf("compact: %w", agent.ErrSessionBusy) }

		_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/compact"))

		requireCode(t, err, protocol.CodeInternal)
		require.ErrorContains(t, err, "/compact: the agent is busy")
		require.Empty(t, h.replies(sessionID))
	})

	t.Run("title regenerates the title and says so", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sessionID := h.newSession(t)

		_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/title"))

		require.NoError(t, err)
		require.True(t, h.traceHas("title"))
		require.False(t, h.ran())
		require.Equal(t, []string{"Session title updated."}, h.replies(sessionID))
	})

	t.Run("title on a session with nothing to title is explained", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sessionID := h.newSession(t)
		h.ws.regenerateTitle = func(context.Context, string) error { return agent.ErrNoTitlePrompt }

		_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/title"))

		requireCode(t, err, protocol.CodeInternal)
		require.ErrorContains(t, err, "/title: the session has no prompt to title yet")
	})

	t.Run("any other failure is passed on", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sessionID := h.newSession(t)
		h.ws.regenerateTitle = func(context.Context, string) error { return errors.New("provider unreachable") }

		_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/title"))

		requireCode(t, err, protocol.CodeInternal)
		require.ErrorContains(t, err, "/title: provider unreachable")
	})

	t.Run("btw sends the question and shows the answer", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sessionID := h.newSession(t)
		var asked struct{ session, question string }
		h.ws.sideQuestion = func(_ context.Context, id, question string) (string, error) {
			asked.session, asked.question = id, question
			return "Because it is cached.", nil
		}

		_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/btw why is the build fast?"))

		require.NoError(t, err)
		require.Equal(t, sessionID, asked.session)
		require.Equal(t, "why is the build fast?", asked.question)
		require.False(t, h.ran())
		require.Equal(t, []string{"Because it is cached."}, h.replies(sessionID))
	})

	t.Run("btw without a question is refused", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sessionID := h.newSession(t)

		_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/btw"))

		requireCode(t, err, protocol.CodeInvalidParams)
		require.ErrorContains(t, err, "Usage: /btw question")
		require.False(t, h.traceHas("btw"))
	})

	t.Run("reload reloads from disk and shows the summary", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sessionID := h.newSession(t)
		h.ws.reload = func(context.Context) (string, error) { return "Reloaded from disk.", nil }

		resp, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/reload"))

		require.NoError(t, err)
		require.Equal(t, protocol.StopReasonEndTurn, resp.StopReason)
		require.True(t, h.traceHas("reload"))
		require.False(t, h.ran())
		require.Equal(t, []string{"Reloaded from disk."}, h.replies(sessionID))
	})

	t.Run("a failed reload is passed on", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sessionID := h.newSession(t)
		h.ws.reload = func(context.Context) (string, error) { return "", errors.New("invalid angela.json") }

		_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/reload"))

		requireCode(t, err, protocol.CodeInternal)
		require.ErrorContains(t, err, "/reload: invalid angela.json")
		require.Empty(t, h.replies(sessionID))
	})

	t.Run("reload takes no input", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sessionID := h.newSession(t)

		_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/reload now"))

		requireCode(t, err, protocol.CodeInvalidParams)
		require.ErrorContains(t, err, "/reload takes no input")
		require.False(t, h.traceHas("reload"))
	})

	t.Run("a command that takes no input refuses some", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sessionID := h.newSession(t)

		_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/compact now please"))

		requireCode(t, err, protocol.CodeInvalidParams)
		require.ErrorContains(t, err, "/compact takes no input")
		require.False(t, h.traceHas("summarize"))
	})

	t.Run("an empty reply sends no message", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sessionID := h.newSession(t)

		_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/btw anything"))

		require.NoError(t, err)
		require.Empty(t, h.replies(sessionID))
	})
}

func TestInitRunsTheInitializePromptAsATurn(t *testing.T) {
	t.Parallel()

	t.Run("the prompt goes to the model and the project is marked", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.initPrompt = "Examine the project."
		sessionID := h.newSession(t)
		got := h.capturePrompt()

		_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/init"))

		require.NoError(t, err)
		require.Equal(t, "Examine the project.", got.text)
		require.Equal(t, 1, h.ws.initMarked)
	})

	t.Run("a prompt that cannot be built is reported without starting a run", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.initErr = errors.New("no template")
		sessionID := h.newSession(t)
		h.capturePrompt()

		_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/init"))

		requireCode(t, err, protocol.CodeInternal)
		require.ErrorContains(t, err, "/init: no template")
		require.False(t, h.ran())
		require.Zero(t, h.ws.initMarked)
	})
}

func TestBuiltinCommandsOnlyCountAtTheStartOfAPrompt(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	sessionID := h.newSession(t)
	got := h.capturePrompt()

	_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "please /compact this"))

	require.NoError(t, err)
	require.Equal(t, "please /compact this", got.text)
	require.False(t, h.traceHas("summarize"))
}

func TestFailedBuiltinCommandAndTheQueueBehindIt(t *testing.T) {
	t.Parallel()

	t.Run("a prompt queued behind a failed compaction is ended, not left waiting", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sessionID := h.newSession(t)

		releaseCompact := make(chan struct{})
		h.ws.summarize = func(context.Context, string) error {
			<-releaseCompact
			return errors.New("summarization hit the output token limit")
		}
		// NOTE: A busy session queues the prompt and returns at once, with
		// no result and no completion event of its own.
		queuedRunID := make(chan string, 1)
		h.ws.run = func(ctx context.Context, _, _ string, _ []message.Attachment) error {
			queuedRunID <- agent.RunIDFromContext(ctx)
			return nil
		}
		// NOTE: The agent reports every prompt it drops as cancelled.
		h.ws.onClearQueue = func(id string) {
			h.ws.publish(pubsub.Event[notify.RunComplete]{
				Type:    pubsub.UpdatedEvent,
				Payload: notify.RunComplete{SessionID: id, RunID: <-queuedRunID, Cancelled: true},
			})
		}

		compact := make(chan promptResult, 1)
		go func() {
			resp, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/compact"))
			compact <- promptResult{resp: resp, err: err}
		}()
		eventually(t, func() bool { return h.traceHas("summarize") })
		queued := h.promptAsync(t, sessionID)
		eventually(t, func() bool { return len(queuedRunID) == 1 })
		close(releaseCompact)

		failed := await(t, compact)
		requireCode(t, failed.err, protocol.CodeInternal)
		require.ErrorContains(t, failed.err, "/compact: summarization hit the output token limit")

		ended := await(t, queued)
		require.NoError(t, ended.err)
		require.Equal(t, protocol.StopReasonCancelled, ended.resp.StopReason)
		h.ws.snapshot(func() { require.Equal(t, []string{sessionID}, h.ws.queueCleared) })
	})

	t.Run("a command refused because a turn is running leaves that turn's queue alone", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sessionID := h.newSession(t)
		h.ws.busy[sessionID] = true
		h.ws.summarize = func(context.Context, string) error { return agent.ErrSessionBusy }

		_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/compact"))

		requireCode(t, err, protocol.CodeInternal)
		h.ws.snapshot(func() { require.Empty(t, h.ws.queueCleared) })
	})

	t.Run("a command that succeeds leaves the queue alone", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sessionID := h.newSession(t)

		_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/compact"))

		require.NoError(t, err)
		h.ws.snapshot(func() { require.Empty(t, h.ws.queueCleared) })
	})
}

func TestCancellingABuiltinCommand(t *testing.T) {
	t.Parallel()

	t.Run("a cancel while it works stops it", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sessionID := h.newSession(t)
		h.ws.summarize = func(ctx context.Context, _ string) error {
			<-ctx.Done()
			return ctx.Err()
		}

		done := make(chan promptResult, 1)
		go func() {
			resp, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/compact"))
			done <- promptResult{resp: resp, err: err}
		}()
		eventually(t, func() bool { return h.traceHas("summarize") })

		require.NoError(t, h.agent.Cancel(t.Context(), protocol.CancelRequest{SessionID: sessionID}))

		res := await(t, done)
		require.NoError(t, res.err)
		require.Equal(t, protocol.StopReasonCancelled, res.resp.StopReason)
		require.Empty(t, h.replies(sessionID))
	})

	t.Run("a cancel that arrived before it registered keeps it from starting", func(t *testing.T) {
		t.Parallel()
		s := newLiveSession("s1", nil, nil)
		before := s.cancelCount()
		s.signalCancelled()
		ran := false
		exp := expansion{command: "compact", act: func(context.Context) (string, error) {
			ran = true
			return "done", nil
		}}

		resp, err := runAction(t.Context(), &fakeClient{}, s, exp, before)

		require.NoError(t, err)
		require.Equal(t, protocol.StopReasonCancelled, resp.StopReason)
		require.False(t, ran)
	})

	t.Run("a cancel from before the prompt does not stop it", func(t *testing.T) {
		t.Parallel()
		s := newLiveSession("s1", nil, nil)
		s.signalCancelled()
		before := s.cancelCount()
		exp := expansion{command: "compact", act: func(context.Context) (string, error) { return "done", nil }}

		resp, err := runAction(t.Context(), &fakeClient{}, s, exp, before)

		require.NoError(t, err)
		require.Equal(t, protocol.StopReasonEndTurn, resp.StopReason)
	})
}
