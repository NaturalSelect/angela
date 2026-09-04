package backend

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/agent"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestBackend_GetAgentInfo(t *testing.T) {
	t.Parallel()

	t.Run("nil coordinator returns zero value", func(t *testing.T) {
		t.Parallel()
		b, _ := newTestBackend(t)
		ws := insertAgentWorkspace(t, b, nil)

		info, err := b.GetAgentInfo(ws.ID)
		require.NoError(t, err)
		require.True(t, info.IsZero())
	})

	t.Run("delegates to coordinator", func(t *testing.T) {
		t.Parallel()
		b, _ := newTestBackend(t)
		coord := &fakeCoordinator{
			busy: true,
			model: agent.Model{
				ModelCfg: config.SelectedModel{Provider: "openai", Model: "gpt-5"},
			},
		}
		ws := insertAgentWorkspace(t, b, coord)

		info, err := b.GetAgentInfo(ws.ID)
		require.NoError(t, err)
		require.True(t, info.IsBusy)
		require.True(t, info.IsReady)
		require.Equal(t, "openai", info.ModelCfg.Provider)
		require.Equal(t, "gpt-5", info.ModelCfg.Model)
	})

	t.Run("workspace not found", func(t *testing.T) {
		t.Parallel()
		b, _ := newTestBackend(t)
		_, err := b.GetAgentInfo("nope")
		require.ErrorIs(t, err, ErrWorkspaceNotFound)
	})
}

func TestBackend_AbandonBranch(t *testing.T) {
	t.Parallel()

	t.Run("nil coordinator is a no-op", func(t *testing.T) {
		t.Parallel()
		b, _ := newTestBackend(t)
		ws := insertAgentWorkspace(t, b, nil)
		require.NoError(t, b.AbandonBranch(ws.ID, "s1"))
	})

	t.Run("delegates to coordinator", func(t *testing.T) {
		t.Parallel()
		b, _ := newTestBackend(t)
		coord := &fakeCoordinator{}
		ws := insertAgentWorkspace(t, b, coord)

		require.NoError(t, b.AbandonBranch(ws.ID, "s1"))
		require.Equal(t, []string{"s1"}, coord.abandoned)
	})

	t.Run("workspace not found", func(t *testing.T) {
		t.Parallel()
		b, _ := newTestBackend(t)
		require.ErrorIs(t, b.AbandonBranch("nope", "s1"), ErrWorkspaceNotFound)
	})
}

func TestBackend_SummarizeSession(t *testing.T) {
	t.Parallel()

	t.Run("nil coordinator", func(t *testing.T) {
		t.Parallel()
		b, _ := newTestBackend(t)
		ws := insertAgentWorkspace(t, b, nil)
		require.ErrorIs(t, b.SummarizeSession(t.Context(), ws.ID, "s1"), ErrAgentNotInitialized)
	})

	t.Run("delegates to coordinator", func(t *testing.T) {
		t.Parallel()
		b, _ := newTestBackend(t)
		coord := &fakeCoordinator{}
		ws := insertAgentWorkspace(t, b, coord)

		require.NoError(t, b.SummarizeSession(t.Context(), ws.ID, "s1"))
		require.Equal(t, []string{"s1"}, coord.summarizeCalls)
	})

	t.Run("propagates coordinator error", func(t *testing.T) {
		t.Parallel()
		b, _ := newTestBackend(t)
		wantErr := agent.ErrAgentNotAvailable
		coord := &fakeCoordinator{summarizeErr: wantErr}
		ws := insertAgentWorkspace(t, b, coord)

		require.ErrorIs(t, b.SummarizeSession(t.Context(), ws.ID, "s1"), wantErr)
	})

	t.Run("workspace not found", func(t *testing.T) {
		t.Parallel()
		b, _ := newTestBackend(t)
		require.ErrorIs(t, b.SummarizeSession(t.Context(), "nope", "s1"), ErrWorkspaceNotFound)
	})
}

func TestBackend_QueueOperations(t *testing.T) {
	t.Parallel()

	t.Run("nil coordinator returns zero values", func(t *testing.T) {
		t.Parallel()
		b, _ := newTestBackend(t)
		ws := insertAgentWorkspace(t, b, nil)

		n, err := b.QueuedPrompts(ws.ID, "s1")
		require.NoError(t, err)
		require.Zero(t, n)

		list, err := b.QueuedPromptsList(ws.ID, "s1")
		require.NoError(t, err)
		require.Nil(t, list)

		require.NoError(t, b.ClearQueue(ws.ID, "s1"))
	})

	t.Run("delegates to coordinator", func(t *testing.T) {
		t.Parallel()
		b, _ := newTestBackend(t)
		coord := &fakeCoordinator{
			queued:     map[string]int{"s1": 3},
			queuedList: map[string][]string{"s1": {"first", "second", "third"}},
		}
		ws := insertAgentWorkspace(t, b, coord)

		n, err := b.QueuedPrompts(ws.ID, "s1")
		require.NoError(t, err)
		require.Equal(t, 3, n)

		list, err := b.QueuedPromptsList(ws.ID, "s1")
		require.NoError(t, err)
		require.Equal(t, []string{"first", "second", "third"}, list)

		require.NoError(t, b.ClearQueue(ws.ID, "s1"))
		require.Equal(t, []string{"s1"}, coord.clearedQueue)
	})

	t.Run("workspace not found", func(t *testing.T) {
		t.Parallel()
		b, _ := newTestBackend(t)

		_, err := b.QueuedPrompts("nope", "s1")
		require.ErrorIs(t, err, ErrWorkspaceNotFound)
		_, err = b.QueuedPromptsList("nope", "s1")
		require.ErrorIs(t, err, ErrWorkspaceNotFound)
		require.ErrorIs(t, b.ClearQueue("nope", "s1"), ErrWorkspaceNotFound)
	})
}

func TestBackend_GetSessionActiveAgent(t *testing.T) {
	t.Parallel()

	t.Run("nil coordinator", func(t *testing.T) {
		t.Parallel()
		b, _ := newTestBackend(t)
		ws := insertAgentWorkspace(t, b, nil)
		_, err := b.GetSessionActiveAgent(t.Context(), ws.ID, "s1")
		require.ErrorIs(t, err, ErrAgentNotInitialized)
	})

	t.Run("delegates to coordinator", func(t *testing.T) {
		t.Parallel()
		b, _ := newTestBackend(t)
		coord := &fakeCoordinator{
			activeResult: config.ActiveAgent{
				Agent: config.Agent{ID: "explore", Name: "Explore", Variant: "fast"},
				Slot:  config.SlotMain,
			},
			activeModel: agent.Model{
				ModelCfg: config.SelectedModel{Provider: "anthropic", Model: "claude"},
				Think:    true,
			},
		}
		ws := insertAgentWorkspace(t, b, coord)

		got, err := b.GetSessionActiveAgent(t.Context(), ws.ID, "s1")
		require.NoError(t, err)
		require.Equal(t, proto.ActiveAgent{
			AgentID:    "explore",
			AgentName:  "Explore",
			Slot:       config.SlotMain,
			ModelCfg:   config.SelectedModel{Provider: "anthropic", Model: "claude"},
			CatwalkCfg: config.ProviderModel{},
			Think:      true,
			Variant:    "fast",
		}, got)
	})

	t.Run("propagates coordinator error", func(t *testing.T) {
		t.Parallel()
		b, _ := newTestBackend(t)
		wantErr := agent.ErrVariantNotAvailable
		coord := &fakeCoordinator{activeErr: wantErr}
		ws := insertAgentWorkspace(t, b, coord)

		_, err := b.GetSessionActiveAgent(t.Context(), ws.ID, "s1")
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("workspace not found", func(t *testing.T) {
		t.Parallel()
		b, _ := newTestBackend(t)
		_, err := b.GetSessionActiveAgent(t.Context(), "nope", "s1")
		require.ErrorIs(t, err, ErrWorkspaceNotFound)
	})
}

// TestBackend_InitAgent drives the real agent.NewCoordinator
// construction path (both interactive and non-interactive) against a
// freshly created, unconfigured workspace. Coordinator construction
// does not validate provider connectivity, so both succeed and leave a
// real, non-nil coordinator installed.
//
// Not parallel: newPublishingWorkspace isolates HOME/XDG_* via
// t.Setenv, which panics if the test tree uses t.Parallel().
// TestBackend_InitAgent drives the real agent.NewCoordinator
// construction path (both interactive and non-interactive) against a
// freshly created, unconfigured workspace. Coordinator construction
// does not validate provider connectivity, so both succeed and leave a
// real, non-nil coordinator installed.
//
// Not parallel: newPublishingWorkspace isolates HOME/XDG_* via
// t.Setenv, which panics if the test tree uses t.Parallel().
func TestBackend_InitAgent(t *testing.T) {
	tests := []struct {
		name        string
		interactive bool
	}{
		{"interactive", true},
		{"non-interactive", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, ws, _ := newPublishingWorkspace(t)
			require.Nil(t, ws.AgentCoordinator)

			require.NoError(t, b.InitAgent(t.Context(), ws.ID, tc.interactive))
			require.NotNil(t, ws.AgentCoordinator, "InitAgent must install a real coordinator")
		})
	}
}

// TestBackend_InitAgent_WorkspaceNotFound is split out from
// TestBackend_InitAgent so it can run in parallel: the sibling
// subtests there use newPublishingWorkspace, which isolates HOME/XDG_*
// via t.Setenv and panics under t.Parallel().
func TestBackend_InitAgent_WorkspaceNotFound(t *testing.T) {
	t.Parallel()
	b, _ := newTestBackend(t)
	require.ErrorIs(t, b.InitAgent(t.Context(), "nope", true), ErrWorkspaceNotFound)
}

// Not parallel: newPublishingWorkspace isolates HOME/XDG_* via
// t.Setenv, which panics if the test tree uses t.Parallel().
// Not parallel: newPublishingWorkspace isolates HOME/XDG_* via
// t.Setenv, which panics if the test tree uses t.Parallel().
func TestBackend_UpdateAgent(t *testing.T) {
	t.Run("no coordinator yet", func(t *testing.T) {
		b, ws, _ := newPublishingWorkspace(t)
		require.ErrorContains(t, b.UpdateAgent(t.Context(), ws.ID), "agent configuration is missing")
	})

	t.Run("after InitAgent succeeds", func(t *testing.T) {
		b, ws, _ := newPublishingWorkspace(t)
		require.NoError(t, b.InitAgent(t.Context(), ws.ID, false))
		require.NoError(t, b.UpdateAgent(t.Context(), ws.ID))
	})
}

// TestBackend_UpdateAgent_WorkspaceNotFound is split out from
// TestBackend_UpdateAgent so it can run in parallel: the sibling
// subtests there use newPublishingWorkspace, which isolates HOME/XDG_*
// via t.Setenv and panics under t.Parallel().
func TestBackend_UpdateAgent_WorkspaceNotFound(t *testing.T) {
	t.Parallel()
	b, _ := newTestBackend(t)
	require.ErrorIs(t, b.UpdateAgent(t.Context(), "nope"), ErrWorkspaceNotFound)
}

func TestBackend_EditSessionActiveAgent(t *testing.T) {
	t.Parallel()

	t.Run("nil coordinator", func(t *testing.T) {
		t.Parallel()
		b, _ := newTestBackend(t)
		ws := insertAgentWorkspace(t, b, nil)
		_, err := b.EditSessionActiveAgent(t.Context(), ws.ID, "s1", config.ActiveAgentEdit{Agent: "explore"})
		require.ErrorIs(t, err, ErrAgentNotInitialized)
	})

	t.Run("edit error is returned without re-reading", func(t *testing.T) {
		t.Parallel()
		b, _ := newTestBackend(t)
		wantErr := agent.ErrModelSlotMismatch
		coord := &fakeCoordinator{editErr: wantErr}
		ws := insertAgentWorkspace(t, b, coord)

		_, err := b.EditSessionActiveAgent(t.Context(), ws.ID, "s1", config.ActiveAgentEdit{Agent: "explore"})
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("success re-reads the folded active agent", func(t *testing.T) {
		t.Parallel()
		b, _ := newTestBackend(t)
		coord := &fakeCoordinator{
			activeResult: config.ActiveAgent{
				Agent: config.Agent{ID: "coder", Name: "Coder"},
				Slot:  config.SlotMain,
			},
			activeModel: agent.Model{ModelCfg: config.SelectedModel{Provider: "openai", Model: "gpt-5"}},
		}
		ws := insertAgentWorkspace(t, b, coord)

		edit := config.ActiveAgentEdit{Agent: "coder"}
		got, err := b.EditSessionActiveAgent(t.Context(), ws.ID, "s1", edit)
		require.NoError(t, err)
		require.Equal(t, edit, coord.editEdit, "the edit must be forwarded to the coordinator unchanged")
		require.Equal(t, "coder", got.AgentID)
		require.Equal(t, "openai", got.ModelCfg.Provider)
	})

	t.Run("workspace not found", func(t *testing.T) {
		t.Parallel()
		b, _ := newTestBackend(t)
		_, err := b.EditSessionActiveAgent(t.Context(), "nope", "s1", config.ActiveAgentEdit{})
		require.ErrorIs(t, err, ErrWorkspaceNotFound)
	})
}
