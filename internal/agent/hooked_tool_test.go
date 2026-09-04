package agent

import (
	"context"
	"path/filepath"
	"testing"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/hooks"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// fakeToolCall records the context and completion of a Run invocation so
// tests can assert on values stamped onto it by decorators.
type fakeToolCall struct {
	called bool
	gotCtx context.Context
}

// newFakeTool wires a MockAgentTool so Run reports the call context and
// completion via the returned record, and Info reports name, the way the
// old hand-written fakeTool worked.
func newFakeTool(t *testing.T, name string, resp fantasy.ToolResponse) (*MockAgentTool, *fakeToolCall) {
	t.Helper()
	rec := &fakeToolCall{}
	m := NewMockAgentTool(gomock.NewController(t))
	m.EXPECT().Info().Return(fantasy.ToolInfo{Name: name}).AnyTimes()
	m.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
		rec.called = true
		rec.gotCtx = ctx
		return resp, nil
	}).AnyTimes()
	m.EXPECT().ProviderOptions().Return(nil).AnyTimes()
	m.EXPECT().SetProviderOptions(gomock.Any()).AnyTimes()
	return m, rec
}

// newRunner builds a hooks.Runner from a single HookConfig, running the
// config-loader path that compiles the matcher regex.
func newRunner(t *testing.T, cmd string) *hooks.Runner {
	t.Helper()
	cfg := &config.Config{
		Hooks: map[string][]config.HookConfig{
			hooks.EventPreToolUse: {{Command: cmd}},
		},
	}
	require.NoError(t, cfg.ValidateHooks())
	return hooks.NewRunner(cfg.Hooks[hooks.EventPreToolUse], t.TempDir(), t.TempDir(), hooks.AgentIdentity{ID: "coder"})
}

func TestHookedTool_AllowStampsHookApproval(t *testing.T) {
	t.Parallel()

	inner, rec := newFakeTool(t, toolnames.View, fantasy.NewTextResponse("ok"))
	runner := newRunner(t, `echo '{"decision":"allow"}'`)
	tool := newHookedTool(inner, runner)

	_, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-1", Name: "view"})
	require.NoError(t, err)
	require.True(t, rec.called, "inner tool should have run")

	// The inner tool's permission service can now treat call-1 as pre-approved.
	dir := t.TempDir()
	svc := permission.NewPermissionService(dir, permission.ModeManual, nil)
	decision := svc.Gate(rec.gotCtx, permission.GateRequest{
		SessionID:  "s1",
		ToolCallID: "call-1",
		Access: permission.Access{
			Tool:   toolnames.Edit,
			Action: permission.ActionEdit,
			Path:   filepath.Join(dir, "a.go"),
		},
	})
	require.True(t, decision.Allowed(), "hook allow should bypass the permission prompt")
}

func TestHookedTool_SilentDoesNotStampApproval(t *testing.T) {
	t.Parallel()

	inner, rec := newFakeTool(t, toolnames.View, fantasy.NewTextResponse("ok"))
	runner := newRunner(t, `exit 0`) // no stdout, no decision
	tool := newHookedTool(inner, runner)

	_, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-2", Name: "view"})
	require.NoError(t, err)
	require.True(t, rec.called)

	// With no hook opinion, a fresh permission request has nothing stamped
	// and must fall through to the normal flow. We verify by checking that
	// the context does not look pre-approved for this call ID: a request
	// that no subscriber resolves blocks until cancelled.
	dir := t.TempDir()
	svc := permission.NewPermissionService(dir, permission.ModeManual, nil)
	ctx, cancel := context.WithCancel(rec.gotCtx)
	cancel()
	decision := svc.Gate(ctx, permission.GateRequest{
		SessionID:  "s1",
		ToolCallID: "call-2",
		Access: permission.Access{
			Tool:   toolnames.Edit,
			Action: permission.ActionEdit,
			Path:   filepath.Join(dir, "a.go"),
		},
	})
	require.Equal(t, permission.OutcomeCancelled, decision.Outcome,
		"no approval stamped => request should reach the prompt path")
	require.False(t, decision.Allowed())
}

func TestHookedTool_DenySkipsInnerTool(t *testing.T) {
	t.Parallel()

	inner, rec := newFakeTool(t, toolnames.Bash, fantasy.ToolResponse{})
	runner := newRunner(t, `echo "blocked" >&2; exit 2`)
	tool := newHookedTool(inner, runner)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-3", Name: toolnames.Bash})
	require.NoError(t, err)
	require.False(t, rec.called, "denied call must not reach the inner tool")
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "blocked")
}

// TestHookedTool_ProviderOptionsPassThrough pins that ProviderOptions
// and SetProviderOptions delegate straight to the inner tool rather
// than being swallowed by the decorator.
func TestHookedTool_ProviderOptionsPassThrough(t *testing.T) {
	t.Parallel()

	inner := NewMockAgentTool(gomock.NewController(t))
	want := fantasy.ProviderOptions{anthropic.Name: &anthropic.ProviderOptions{}}
	inner.EXPECT().ProviderOptions().Return(want)
	inner.EXPECT().SetProviderOptions(want)

	tool := newHookedTool(inner, nil)
	require.Equal(t, want, tool.ProviderOptions())
	tool.SetProviderOptions(want)
}

func TestWrapToolsWithHooks(t *testing.T) {
	t.Parallel()

	runner := newRunner(t, `exit 0`)
	a, _ := newFakeTool(t, "a", fantasy.ToolResponse{})
	b, _ := newFakeTool(t, "b", fantasy.ToolResponse{})
	inputs := []fantasy.AgentTool{a, b}

	// Sub-agents used to be exempt from this wrap, which let a delegated
	// write reach the disk without ever facing the user's PreToolUse
	// policy. Every tool a runner guards is wrapped now, whoever calls it.
	t.Run("every tool is wrapped", func(t *testing.T) {
		t.Parallel()
		out := wrapToolsWithHooks(inputs, runner)
		require.Len(t, out, len(inputs))
		for i, tool := range out {
			_, ok := tool.(*hookedTool)
			require.Truef(t, ok, "tool %d should be a *hookedTool", i)
		}
	})

	t.Run("nil runner skips the wrap", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, inputs, wrapToolsWithHooks(inputs, nil))
	})
}
