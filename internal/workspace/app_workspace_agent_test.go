package workspace

import (
	"context"
	"errors"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// TestAppWorkspace_Agent_NilCoordinator pins the fallback behavior of
// every Agent* method before the coder agent has been initialized
// (e.g. no model configured yet): each must report a safe zero value
// or a descriptive error instead of dereferencing a nil coordinator.
func TestAppWorkspace_Agent_NilCoordinator(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)
	fx.app.AgentCoordinator = nil

	require.Error(t, fx.ws.AgentRun(t.Context(), "sess-1", "hi"))

	fx.ws.AgentCancel("sess-1")
	fx.ws.AgentAbandonBranch("sess-1")

	require.False(t, fx.ws.AgentIsBusy())
	require.False(t, fx.ws.AgentIsSessionBusy("sess-1"))
	require.False(t, fx.ws.AgentIsSessionBranch("sess-1"))
	require.False(t, fx.ws.AgentIsReady())
	require.ErrorIs(t, fx.ws.AgentReadyErr(), ErrAgentNotInitialized)
	require.Equal(t, 0, fx.ws.AgentQueuedPrompts("sess-1"))
	require.Nil(t, fx.ws.AgentQueuedPromptsList("sess-1"))
	fx.ws.AgentClearQueue("sess-1")

	require.Error(t, fx.ws.AgentSummarize(t.Context(), "sess-1"))

	_, err := fx.ws.AgentActive(t.Context(), "sess-1")
	require.Error(t, err)

	_, err = fx.ws.AgentEditActive(t.Context(), "sess-1", config.ActiveAgentEdit{})
	require.Error(t, err)

	require.Error(t, fx.ws.UpdateAgentModel(t.Context()))
}

func TestAppWorkspace_AgentRun(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		fx.coord.EXPECT().Run(gomock.Any(), "sess-1", "hello").Return(&fantasy.AgentResult{}, nil)

		require.NoError(t, fx.ws.AgentRun(t.Context(), "sess-1", "hello"))
	})

	t.Run("propagates coordinator error", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		boom := errors.New("run failed")
		fx.coord.EXPECT().Run(gomock.Any(), "sess-1", "hello").Return(nil, boom)

		require.ErrorIs(t, fx.ws.AgentRun(t.Context(), "sess-1", "hello"), boom)
	})
}

func TestAppWorkspace_AgentCancel(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)
	fx.coord.EXPECT().Cancel("sess-1")

	fx.ws.AgentCancel("sess-1")
}

func TestAppWorkspace_AgentAbandonBranch(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)
	fx.coord.EXPECT().AbandonBranch("sess-1").Return(true)

	fx.ws.AgentAbandonBranch("sess-1")
}

func TestAppWorkspace_AgentIsBusy(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)
	fx.coord.EXPECT().IsBusy().Return(true)

	require.True(t, fx.ws.AgentIsBusy())
}

func TestAppWorkspace_AgentIsSessionBusy(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)
	fx.coord.EXPECT().IsSessionBusy("sess-1").Return(true)

	require.True(t, fx.ws.AgentIsSessionBusy("sess-1"))
}

func TestAppWorkspace_AgentIsSessionBranch(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)
	fx.coord.EXPECT().IsSessionBranch("sess-1").Return(true)

	require.True(t, fx.ws.AgentIsSessionBranch("sess-1"))
}

func TestAppWorkspace_AgentIsReady(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)

	require.True(t, fx.ws.AgentIsReady())
	require.NoError(t, fx.ws.AgentReadyErr())
}

func TestAppWorkspace_AgentQueuedPrompts(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)
	fx.coord.EXPECT().QueuedPrompts("sess-1").Return(3)
	fx.coord.EXPECT().QueuedPromptsList("sess-1").Return([]string{"a", "b", "c"})

	require.Equal(t, 3, fx.ws.AgentQueuedPrompts("sess-1"))
	require.Equal(t, []string{"a", "b", "c"}, fx.ws.AgentQueuedPromptsList("sess-1"))
}

func TestAppWorkspace_AgentClearQueue(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)
	fx.coord.EXPECT().ClearQueue("sess-1")

	fx.ws.AgentClearQueue("sess-1")
}

func TestAppWorkspace_AgentSummarize(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		fx.coord.EXPECT().Summarize(gomock.Any(), "sess-1").Return(nil)

		require.NoError(t, fx.ws.AgentSummarize(t.Context(), "sess-1"))
	})

	t.Run("propagates coordinator error", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		boom := errors.New("summarize failed")
		fx.coord.EXPECT().Summarize(gomock.Any(), "sess-1").Return(boom)

		require.ErrorIs(t, fx.ws.AgentSummarize(t.Context(), "sess-1"), boom)
	})
}

// TestAppWorkspace_AgentActive verifies the ActiveAgent -> ActiveAgent
// field mapping, since a swapped field here would silently show the
// wrong model or agent name in the UI.
func TestAppWorkspace_AgentActive(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		active := config.ActiveAgent{
			Agent:     config.Agent{ID: "coder", Name: "Coder", Variant: "default"},
			ModelName: "main",
		}
		model := agent.Model{
			ModelCfg:   config.SelectedModel{Provider: "anthropic", Model: "claude"},
			CatwalkCfg: catwalk.Model{ID: "claude"},
		}
		fx.coord.EXPECT().ActiveAgent(gomock.Any(), "sess-1").Return(active, model, nil)

		got, err := fx.ws.AgentActive(t.Context(), "sess-1")
		require.NoError(t, err)
		require.Equal(t, ActiveAgent{
			AgentID:    "coder",
			AgentName:  "Coder",
			ModelName:  "main",
			ModelCfg:   model.ModelCfg,
			CatwalkCfg: model.CatwalkCfg,
			Variant:    "default",
		}, got)
	})

	t.Run("propagates coordinator error", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		boom := errors.New("resolve failed")
		fx.coord.EXPECT().ActiveAgent(gomock.Any(), "sess-1").Return(config.ActiveAgent{}, agent.Model{}, boom)

		_, err := fx.ws.AgentActive(t.Context(), "sess-1")
		require.ErrorIs(t, err, boom)
	})
}

// TestAppWorkspace_AgentEditActive verifies that a successful edit
// re-reads state through ActiveAgent (the only path that folds a
// session's preset into the reported model) rather than translating
// EditActiveAgent's own return value, and that a failed edit skips
// that re-read entirely.
func TestAppWorkspace_AgentEditActive(t *testing.T) {
	t.Parallel()

	t.Run("success re-reads through ActiveAgent", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		edit := config.ActiveAgentEdit{Agent: "task"}
		active := config.ActiveAgent{
			Agent:     config.Agent{ID: "task", Name: "Task", Variant: "v1"},
			ModelName: "main",
		}
		model := agent.Model{
			ModelCfg:   config.SelectedModel{Provider: "anthropic", Model: "claude"},
			CatwalkCfg: catwalk.Model{ID: "claude"},
		}
		fx.coord.EXPECT().EditActiveAgent(gomock.Any(), "sess-1", edit).Return(config.ActiveAgent{}, nil)
		fx.coord.EXPECT().ActiveAgent(gomock.Any(), "sess-1").Return(active, model, nil)

		got, err := fx.ws.AgentEditActive(t.Context(), "sess-1", edit)
		require.NoError(t, err)
		require.Equal(t, ActiveAgent{
			AgentID:    "task",
			AgentName:  "Task",
			ModelName:  "main",
			ModelCfg:   model.ModelCfg,
			CatwalkCfg: model.CatwalkCfg,
			Variant:    "v1",
		}, got)
	})

	t.Run("edit error skips the re-read", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		boom := errors.New("edit failed")
		fx.coord.EXPECT().EditActiveAgent(gomock.Any(), "sess-1", gomock.Any()).Return(config.ActiveAgent{}, boom)
		// No ActiveAgent expectation: a failed edit must not re-read.

		_, err := fx.ws.AgentEditActive(t.Context(), "sess-1", config.ActiveAgentEdit{})
		require.ErrorIs(t, err, boom)
	})
}

func TestAppWorkspace_UpdateAgentModel(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		fx.coord.EXPECT().UpdateModels(gomock.Any()).Return(nil)

		require.NoError(t, fx.ws.UpdateAgentModel(t.Context()))
	})

	t.Run("propagates coordinator error", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		boom := errors.New("update failed")
		fx.coord.EXPECT().UpdateModels(gomock.Any()).Return(boom)

		require.ErrorIs(t, fx.ws.UpdateAgentModel(t.Context()), boom)
	})
}

// TestAppWorkspace_AgentRunShellCommand exercises the real
// mvdan.cc/sh interpreter through shell.RunAndPersist /
// RunAndCaptureStream, so subtests share one fixture and one real
// config store (newTestConfigStore calls t.Setenv) and must run
// sequentially: no t.Parallel, since concurrent subtests would race
// both the shared gomock controller and HOME/XDG env vars.
func TestAppWorkspace_AgentRunShellCommand(t *testing.T) {
	fx := newAWFixture(t)
	fx.ws.store = newTestConfigStore(t)

	t.Run("captures output without persisting when sessionID is empty", func(t *testing.T) {
		resp, err := fx.ws.AgentRunShellCommand(t.Context(), "", "echo hello", 80, nil, false)
		require.NoError(t, err)
		require.Contains(t, resp.Output, "hello")
		require.Equal(t, 0, resp.ExitCode)
	})

	t.Run("persists output and generates a title on the first message", func(t *testing.T) {
		fx.messages.EXPECT().Create(gomock.Any(), "sess-1", gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, params message.CreateMessageParams) (message.Message, error) {
				require.Len(t, params.Parts, 1)
				cmd, ok := params.Parts[0].(message.ShellCommand)
				require.True(t, ok, "expected message.ShellCommand, got %T", params.Parts[0])
				require.Equal(t, "echo persisted", cmd.Command)
				require.Contains(t, cmd.Output, "persisted")
				return message.Message{}, nil
			})
		fx.coord.EXPECT().GenerateTitle(gomock.Any(), "sess-1", "$ echo persisted")

		resp, err := fx.ws.AgentRunShellCommand(t.Context(), "sess-1", "echo persisted", 80, nil, true)
		require.NoError(t, err)
		require.Contains(t, resp.Output, "persisted")
	})

	t.Run("streams progress and still persists exactly once after completion", func(t *testing.T) {
		fx.messages.EXPECT().Create(gomock.Any(), "sess-2", gomock.Any()).Return(message.Message{}, nil)

		var chunks []string
		resp, err := fx.ws.AgentRunShellCommand(t.Context(), "sess-2", "echo streamed", 80, func(s string) {
			chunks = append(chunks, s)
		}, false)
		require.NoError(t, err)
		require.NotEmpty(t, chunks, "onProgress should have been invoked with output chunks")
		require.Contains(t, resp.Output, "streamed")
	})

	// shell.RunAndPersist reports command failures as a non-zero exit
	// code rather than a Go error, so a malformed command must not
	// surface as an AgentRunShellCommand error either.
	t.Run("malformed command surfaces as a non-zero exit code, not a Go error", func(t *testing.T) {
		resp, err := fx.ws.AgentRunShellCommand(t.Context(), "", "echo 'unterminated", 80, nil, false)
		require.NoError(t, err)
		require.NotZero(t, resp.ExitCode)
	})
}
