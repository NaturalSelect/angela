package agent

import (
	"context"
	"errors"
	"io/fs"
	"sync/atomic"
	"syscall"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/notify"
	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// singleToolCallModel streams a single tool call naming toolName on its
// first Stream call, then a normal text finish on its second.
type singleToolCallModel struct {
	toolName  string
	toolInput string
	attempts  atomic.Int32
}

func (m *singleToolCallModel) Provider() string { return "fake" }
func (m *singleToolCallModel) Model() string    { return "fake-model" }

func (m *singleToolCallModel) Generate(context.Context, fantasy.Call) (*fantasy.Response, error) {
	return nil, errors.New("not implemented")
}

func (m *singleToolCallModel) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *singleToolCallModel) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *singleToolCallModel) Stream(context.Context, fantasy.Call) (fantasy.StreamResponse, error) {
	if m.attempts.Add(1) == 1 {
		return func(yield func(fantasy.StreamPart) bool) {
			if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputStart, ID: "1", ToolCallName: m.toolName}) {
				return
			}
			if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputDelta, ID: "1", Delta: m.toolInput}) {
				return
			}
			if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputEnd, ID: "1"}) {
				return
			}
			if !yield(fantasy.StreamPart{
				Type:          fantasy.StreamPartTypeToolCall,
				ID:            "1",
				ToolCallName:  m.toolName,
				ToolCallInput: m.toolInput,
			}) {
				return
			}
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonToolCalls})
		}, nil
	}
	return func(yield func(fantasy.StreamPart) bool) {
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "2"}) {
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "2", Delta: "done"}) {
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "2"}) {
			return
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop})
	}, nil
}

// rawFailingTool implements fantasy.AgentTool directly instead of going
// through tools.NewTool, standing in for an MCP tool or any other
// fantasy.AgentTool implementation Angela does not control. It proves
// safeTool alone — the outermost wrapper coordinator.go installs around
// every tool — is enough to catch a raw Go error even when nothing
// stops the tool code itself from producing one.
type rawFailingTool struct{}

func (rawFailingTool) Info() fantasy.ToolInfo {
	return fantasy.ToolInfo{
		Name:        "failing_raw",
		Description: "always fails with a raw Go error",
		Parameters:  map[string]any{},
		Required:    []string{},
	}
}

func (rawFailingTool) ProviderOptions() fantasy.ProviderOptions   { return nil }
func (rawFailingTool) SetProviderOptions(fantasy.ProviderOptions) {}

func (rawFailingTool) Run(context.Context, fantasy.ToolCall) (fantasy.ToolResponse, error) {
	return fantasy.ToolResponse{}, &fs.PathError{Op: "stat", Path: "/x", Err: syscall.EACCES}
}

// newNotifyTestAgent builds a SessionAgent wired to a notification
// broker, mirroring retry_notify_test.go's setup. A subtest below reads
// events to assert directly on whether fantasy classified a run as a
// retryable provider failure. That is the only reliable signal here:
// a retried step re-invokes the model's Stream too, so counting calls
// cannot tell "no retry happened" apart from "one retry happened and
// the fake model's second reply happens to look like an ordinary
// second turn".
func newNotifyTestAgent(t *testing.T, env fakeEnv) (SessionAgent, <-chan pubsub.Event[notify.Notification]) {
	t.Helper()
	broker := pubsub.NewBroker[notify.Notification]()
	t.Cleanup(broker.Shutdown)
	subCtx, subCancel := context.WithCancel(t.Context())
	t.Cleanup(subCancel)
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
	})
	return sa, events
}

// requireNoRetryNotification drains events, failing the test if any of
// them is a provider-retry notification. A successful run still
// publishes its normal completion notification, so this checks the
// type of each event rather than asserting the channel stays empty.
func requireNoRetryNotification(t *testing.T, events <-chan pubsub.Event[notify.Notification]) {
	t.Helper()
	for {
		select {
		case ev := <-events:
			require.NotEqual(t, notify.TypeAgentRetrying, ev.Payload.Type,
				"a tool error must not be classified as a retryable provider failure: %+v", ev.Payload)
		default:
			return
		}
	}
}

// findToolResult returns the first tool result recorded across msgs,
// failing the test if there is none.
func findToolResult(t *testing.T, msgs []message.Message) message.ToolResult {
	t.Helper()
	for _, msg := range msgs {
		if msg.Role != message.Tool {
			continue
		}
		if results := msg.ToolResults(); len(results) > 0 {
			return results[0]
		}
	}
	t.Fatal("no tool result message found")
	return message.ToolResult{}
}

// findAssistant returns the last assistant message in msgs, failing the
// test if there is none.
func findAssistant(t *testing.T, msgs []message.Message) *message.Message {
	t.Helper()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == message.Assistant {
			return &msgs[i]
		}
	}
	t.Fatal("no assistant message found")
	return nil
}

// TestToolErrorDoesNotRetry pins the fix for a bug where a tool's Go
// error leaked into fantasy's provider-level retry and error
// classification. fantasy treats any error out of AgentTool.Run as a
// critical, step-ending failure: syscall.Errno (e.g. EACCES) happens to
// satisfy net.Error through its Timeout()/Temporary() methods, so a
// plain permission-denied error was retried up to streamMaxRetries times
// with exponential backoff, and anything that did not match ended the
// turn labeled "Provider Error" instead of being narrated to the model
// as an ordinary tool result. Each subtest below pins one leg of the
// fix that closes that gap.
func TestToolErrorDoesNotRetry(t *testing.T) {
	t.Parallel()

	t.Run("tool result union reports a business failure without retrying", func(t *testing.T) {
		t.Parallel()

		env := testEnv(t)
		sa, events := newNotifyTestAgent(t, env)

		sess, err := env.sessions.Create(t.Context(), "session")
		require.NoError(t, err)

		failing := tools.NewTool("failing", "always fails", func(_ context.Context, _ struct{}, _ fantasy.ToolCall) tools.Result {
			return tools.FailErr("error accessing file", &fs.PathError{Op: "stat", Path: "/x", Err: syscall.EACCES})
		})

		model := &singleToolCallModel{toolName: "failing", toolInput: "{}"}
		_, err = sa.Run(t.Context(), SessionAgentCall{
			SessionID: sess.ID,
			Prompt:    "hi",
			Agent: resolvedAgent{
				ID:        config.AgentCoder,
				Model:     Model{Model: model, CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}}},
				Tools:     []fantasy.AgentTool{failing},
				MaxTokens: 10000,
			},
		})
		require.NoError(t, err)
		requireNoRetryNotification(t, events)
		require.Equal(t, int32(2), model.attempts.Load(), "the model must be asked exactly once for the tool call and once for the final reply")

		msgs, err := env.messages.List(t.Context(), sess.ID)
		require.NoError(t, err)
		toolResult := findToolResult(t, msgs)
		require.True(t, toolResult.IsError)
		require.Contains(t, toolResult.Content, "permission denied")

		assistant := findAssistant(t, msgs)
		require.Equal(t, message.FinishReasonEndTurn, assistant.FinishReason(),
			"the turn must end normally instead of falling back to the generic step-error path")
	})

	t.Run("raw AgentTool error is caught by the safety-net wrapper", func(t *testing.T) {
		t.Parallel()

		env := testEnv(t)
		sa, events := newNotifyTestAgent(t, env)

		sess, err := env.sessions.Create(t.Context(), "session")
		require.NoError(t, err)

		wrapped := wrapToolsWithSafety([]fantasy.AgentTool{rawFailingTool{}})
		model := &singleToolCallModel{toolName: "failing_raw", toolInput: "{}"}
		_, err = sa.Run(t.Context(), SessionAgentCall{
			SessionID: sess.ID,
			Prompt:    "hi",
			Agent: resolvedAgent{
				ID:        config.AgentCoder,
				Model:     Model{Model: model, CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}}},
				Tools:     wrapped,
				MaxTokens: 10000,
			},
		})
		require.NoError(t, err)
		requireNoRetryNotification(t, events)
		require.Equal(t, int32(2), model.attempts.Load(), "the model must be asked exactly once for the tool call and once for the final reply")

		msgs, err := env.messages.List(t.Context(), sess.ID)
		require.NoError(t, err)
		toolResult := findToolResult(t, msgs)
		require.True(t, toolResult.IsError)
		require.Contains(t, toolResult.Content, "permission denied")

		assistant := findAssistant(t, msgs)
		require.Equal(t, message.FinishReasonEndTurn, assistant.FinishReason())
	})

	t.Run("tool cancellation still ends the turn as a Go error, not a tool result", func(t *testing.T) {
		t.Parallel()

		env := testEnv(t)
		sa, events := newNotifyTestAgent(t, env)

		sess, err := env.sessions.Create(t.Context(), "session")
		require.NoError(t, err)

		runCtx, cancel := context.WithCancel(t.Context())
		cancelling := tools.NewTool("cancelling", "cancels the run instead of returning normally", func(context.Context, struct{}, fantasy.ToolCall) tools.Result {
			// Canceling here, instead of returning a Fail/Ok Result,
			// triggers the NewTool adapter's own ctx.Err() check (see
			// TestNewToolAdapter in tools/result_test.go for that
			// mechanism in isolation): it reads ctx.Err() once this
			// function returns and reports cancellation as a Go error
			// instead of the Result below, which fantasy then treats as
			// a critical, turn-ending failure rather than a tool result.
			cancel()
			return tools.Ok("unused")
		})

		model := &singleToolCallModel{toolName: "cancelling", toolInput: "{}"}
		_, err = sa.Run(runCtx, SessionAgentCall{
			SessionID: sess.ID,
			Prompt:    "hi",
			Agent: resolvedAgent{
				ID:        config.AgentCoder,
				Model:     Model{Model: model, CatwalkCfg: config.ProviderModel{Model: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}}},
				Tools:     []fantasy.AgentTool{cancelling},
				MaxTokens: 10000,
			},
		})
		require.ErrorIs(t, err, context.Canceled)
		requireNoRetryNotification(t, events)
		require.Equal(t, int32(1), model.attempts.Load(), "a canceled tool call must stop the run instead of asking the model again")

		msgs, err := env.messages.List(t.Context(), sess.ID)
		require.NoError(t, err)
		assistant := findAssistant(t, msgs)
		require.Equal(t, message.FinishReasonCanceled, assistant.FinishReason())
	})
}
