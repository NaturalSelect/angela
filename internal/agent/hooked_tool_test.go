package agent

import (
	"context"
	"path/filepath"
	"testing"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/hooks"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/stretchr/testify/require"
)

// fakeTool records the context it was invoked with so tests can assert on
// values stamped onto it by the hookedTool decorator.
type fakeTool struct {
	name   string
	called bool
	gotCtx context.Context
	resp   fantasy.ToolResponse
}

func (f *fakeTool) Info() fantasy.ToolInfo {
	return fantasy.ToolInfo{Name: f.name}
}

func (f *fakeTool) Run(ctx context.Context, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
	f.called = true
	f.gotCtx = ctx
	return f.resp, nil
}

func (f *fakeTool) ProviderOptions() fantasy.ProviderOptions     { return nil }
func (f *fakeTool) SetProviderOptions(_ fantasy.ProviderOptions) {}

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

	inner := &fakeTool{name: "view", resp: fantasy.NewTextResponse("ok")}
	runner := newRunner(t, `echo '{"decision":"allow"}'`)
	tool := newHookedTool(inner, runner)

	_, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-1", Name: "view"})
	require.NoError(t, err)
	require.True(t, inner.called, "inner tool should have run")

	// The inner tool's permission service can now treat call-1 as pre-approved.
	dir := t.TempDir()
	svc := permission.NewPermissionService(dir, false, nil)
	decision := svc.Gate(inner.gotCtx, permission.GateRequest{
		SessionID:  "s1",
		ToolCallID: "call-1",
		Access: permission.Access{
			Tool:   "edit",
			Action: permission.ActionEdit,
			Path:   filepath.Join(dir, "a.go"),
		},
	})
	require.True(t, decision.Allowed(), "hook allow should bypass the permission prompt")
}

func TestHookedTool_SilentDoesNotStampApproval(t *testing.T) {
	t.Parallel()

	inner := &fakeTool{name: "view", resp: fantasy.NewTextResponse("ok")}
	runner := newRunner(t, `exit 0`) // no stdout, no decision
	tool := newHookedTool(inner, runner)

	_, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-2", Name: "view"})
	require.NoError(t, err)
	require.True(t, inner.called)

	// With no hook opinion, a fresh permission request has nothing stamped
	// and must fall through to the normal flow. We verify by checking that
	// the context does not look pre-approved for this call ID: a request
	// that no subscriber resolves blocks until cancelled.
	dir := t.TempDir()
	svc := permission.NewPermissionService(dir, false, nil)
	ctx, cancel := context.WithCancel(inner.gotCtx)
	cancel()
	decision := svc.Gate(ctx, permission.GateRequest{
		SessionID:  "s1",
		ToolCallID: "call-2",
		Access: permission.Access{
			Tool:   "edit",
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

	inner := &fakeTool{name: "bash"}
	runner := newRunner(t, `echo "blocked" >&2; exit 2`)
	tool := newHookedTool(inner, runner)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-3", Name: "bash"})
	require.NoError(t, err)
	require.False(t, inner.called, "denied call must not reach the inner tool")
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "blocked")
}

func TestWrapToolsWithHooks(t *testing.T) {
	t.Parallel()

	runner := newRunner(t, `exit 0`)
	inputs := []fantasy.AgentTool{&fakeTool{name: "a"}, &fakeTool{name: "b"}}

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
