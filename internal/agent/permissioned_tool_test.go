package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
)

func bashCall(t *testing.T, command string) fantasy.ToolCall {
	t.Helper()
	input, err := json.Marshal(map[string]string{"command": command})
	require.NoError(t, err)
	return fantasy.ToolCall{ID: "call-1", Name: toolnames.Bash, Input: string(input)}
}

func sessionCtx(ctx context.Context) context.Context {
	return context.WithValue(ctx, tools.SessionIDContextKey, "s1")
}

// runGated wires a tool behind the permission decorator and runs one
// call, answering any prompt with the given verdict.
func runGated(t *testing.T, svc permission.Service, dir string, call fantasy.ToolCall, approve bool) fantasy.ToolResponse {
	t.Helper()

	inner, _ := newFakeTool(t, call.Name, fantasy.NewTextResponse("ran"))
	gated := newPermissionedTool(inner, svc, dir)

	events := svc.Subscribe(t.Context())
	done := make(chan fantasy.ToolResponse, 1)
	go func() {
		resp, err := gated.Run(sessionCtx(t.Context()), call)
		require.NoError(t, err)
		done <- resp
	}()

	for {
		select {
		case ev := <-events:
			if approve {
				svc.Grant(ev.Payload)
			} else {
				svc.Deny(ev.Payload)
			}
		case resp := <-done:
			return resp
		}
	}
}

// TestPermissionedTool_KnownBypassesAsk pins the escape routes that the
// former prefix-and-metacharacter check let through without asking.
// The check now lives in the decorator, so this is where they belong.
func TestPermissionedTool_KnownBypassesAsk(t *testing.T) {
	t.Parallel()

	// The outside file is a real directory in slash form rather than a
	// literal like /etc/hostname: on Windows a leading slash carries no
	// volume, so such a path is not absolute and would resolve back
	// inside the working directory, making the read look local.
	outside := filepath.ToSlash(filepath.Join(t.TempDir(), "hostname"))

	commands := map[string]string{
		"output redirection":    "echo evil > escaped.txt",
		"background chaining":   "echo x & touch evil.txt",
		"newline chaining":      "echo hi\ntouch evil.txt",
		"kill":                  "kill -9 12345",
		"timeout wrapper":       "timeout 5 touch evil.txt",
		"env hijack":            "env LD_PRELOAD=/x ls",
		"command substitution":  "ls $(whoami)",
		"subshell":              "(ls)",
		"read outside work dir": "cat " + outside,
	}

	for name, command := range commands {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			svc := permission.NewPermissionService(dir, permission.ModeManual, nil)

			resp := runGated(t, svc, dir, bashCall(t, command), false)
			require.Contains(t, resp.Content, "User denied permission",
				"%q must reach the prompt", command)
		})
	}
}

// TestPermissionedTool_SafeCommandsRunSilently pins the other side: a
// read-only command inside the working directory never prompts, so the
// gate does not make the agent unusable.
func TestPermissionedTool_SafeCommandsRunSilently(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"ls -la", "ls && echo done", "git status"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			svc := permission.NewPermissionService(dir, permission.ModeManual, nil)

			inner, rec := newFakeTool(t, toolnames.Bash, fantasy.NewTextResponse("ran"))
			gated := newPermissionedTool(inner, svc, dir)

			resp, err := gated.Run(sessionCtx(t.Context()), bashCall(t, command))
			require.NoError(t, err)
			require.False(t, resp.IsError)
			require.True(t, rec.called, "a safe command should reach the tool")
		})
	}
}

// TestPermissionedTool_UnknownToolIsDenied pins the fail-closed rule:
// a tool the access mapping does not describe cannot be approved, so
// adding a tool without mapping it denies it rather than exempting it.
func TestPermissionedTool_UnknownToolIsDenied(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	svc := permission.NewPermissionService(dir, permission.ModeManual, nil)
	inner, rec := newFakeTool(t, "brand_new_tool", fantasy.NewTextResponse("ran"))
	gated := newPermissionedTool(inner, svc, dir)

	resp, err := gated.Run(sessionCtx(t.Context()), fantasy.ToolCall{
		ID: "c1", Name: "brand_new_tool", Input: "{}",
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.False(t, rec.called, "an undescribed tool must not run")
}

// TestPermissionedTool_DenyOutcomesDiffer pins that the model is told
// the difference: the configuration refusing is an obstacle it may
// route around, while the user refusing ends the turn.
func TestPermissionedTool_DenyOutcomesDiffer(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	policy, err := permission.CompilePolicy([]permission.Rule{
		{Action: permission.RuleDeny, Tool: toolnames.Bash, Pattern: "curl*"},
	}, nil, permission.PromptAsk)
	require.NoError(t, err)
	svc := permission.NewPermissionService(dir, permission.ModeManual, policy)

	inner, _ := newFakeTool(t, toolnames.Bash, fantasy.NewTextResponse("ran"))
	gated := newPermissionedTool(inner, svc, dir)

	byPolicy, err := gated.Run(sessionCtx(t.Context()), bashCall(t, "curl http://evil"))
	require.NoError(t, err)
	require.True(t, byPolicy.IsError)
	require.False(t, byPolicy.StopTurn, "a policy refusal must let the turn continue")
	require.Contains(t, byPolicy.Content, "denied by configuration")

	byUser := runGated(t, svc, dir, bashCall(t, "touch out.txt"), false)
	require.True(t, byUser.StopTurn, "a user refusal ends the turn")
}

// TestPermissionedTool_DenyReasonReachesToolResponse pins the full
// path a typed denial reason travels: from the value the UI would set
// on PermissionRequest.DenyReason before calling Deny, through the
// permission service's Decision, to the text the model actually sees
// in the tool's error response.
func TestPermissionedTool_DenyReasonReachesToolResponse(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	svc := permission.NewPermissionService(dir, permission.ModeManual, nil)

	inner, _ := newFakeTool(t, toolnames.Bash, fantasy.NewTextResponse("ran"))
	gated := newPermissionedTool(inner, svc, dir)

	events := svc.Subscribe(t.Context())
	done := make(chan fantasy.ToolResponse, 1)
	go func() {
		resp, err := gated.Run(sessionCtx(t.Context()), bashCall(t, "touch out.txt"))
		require.NoError(t, err)
		done <- resp
	}()

	var resp fantasy.ToolResponse
loop:
	for {
		select {
		case ev := <-events:
			perm := ev.Payload
			perm.DenyReason = "not needed for this task"
			svc.Deny(perm)
		case resp = <-done:
			break loop
		}
	}

	require.True(t, resp.IsError)
	require.True(t, resp.StopTurn, "a user refusal ends the turn")
	require.Contains(t, resp.Content, "not needed for this task")
}

// TestPermissionedTool_PolicyDenyBeatsSkip pins the priority the user
// chose: turning off prompts must not turn off a deny rule.
func TestPermissionedTool_PolicyDenyBeatsSkip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	policy, err := permission.CompilePolicy([]permission.Rule{
		{Action: permission.RuleDeny, Tool: permission.ActionEdit.String(), Pattern: "**/.env"},
	}, nil, permission.PromptAsk)
	require.NoError(t, err)
	svc := permission.NewPermissionService(dir, permission.ModeYolo, policy)

	input, err := json.Marshal(map[string]string{
		"file_path": filepath.Join(dir, ".env"),
		"content":   "SECRET=1",
	})
	require.NoError(t, err)

	inner, rec := newFakeTool(t, toolnames.Write, fantasy.NewTextResponse("wrote"))
	gated := newPermissionedTool(inner, svc, dir)

	resp, err := gated.Run(sessionCtx(t.Context()), fantasy.ToolCall{
		ID: "c1", Name: toolnames.Write, Input: string(input),
	})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.False(t, rec.called, "a deny rule must survive --yolo")
}

// TestPermissionedTool_HookAllowReachesTheGate pins the wrapper order:
// hooks wrap permissions, so a hook's allow is already on the context
// when the gate looks for it.
func TestPermissionedTool_HookAllowReachesTheGate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	svc := permission.NewPermissionService(dir, permission.ModeManual, nil)

	inner, rec := newFakeTool(t, toolnames.Bash, fantasy.NewTextResponse("ran"))
	gated := newPermissionedTool(inner, svc, dir)
	hooked := newHookedTool(gated, newRunner(t, `echo '{"decision":"allow"}'`))

	resp, err := hooked.Run(sessionCtx(t.Context()), bashCall(t, "touch out.txt"))
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.True(t, rec.called, "a hook allow should carry the call past the gate")
}

// planningTool is a fake Planner that records whether it was asked to
// plan, so tests can tell "the gate refused" apart from "the gate
// refused before anything was read".
type planningTool struct {
	*MockAgentTool
	call    *fakeToolCall
	planned int
	plan    tools.Plan
}

// newPlanningTool wires a planningTool around a MockAgentTool the way
// newFakeTool does, so Plan-aware tests keep the same call/gotCtx
// visibility the old hand-written fakeTool embedding gave them.
func newPlanningTool(t *testing.T, name string, plan tools.Plan) *planningTool {
	t.Helper()
	m, rec := newFakeTool(t, name, fantasy.ToolResponse{})
	return &planningTool{MockAgentTool: m, call: rec, plan: plan}
}

func (p *planningTool) Plan(context.Context, fantasy.ToolCall) (tools.Plan, error) {
	p.planned++
	return p.plan, nil
}

func editCall(t *testing.T, path string) fantasy.ToolCall {
	t.Helper()
	input, err := json.Marshal(map[string]string{
		"file_path": path, "old_string": "a", "new_string": "b",
	})
	require.NoError(t, err)
	return fantasy.ToolCall{ID: "call-1", Name: toolnames.Edit, Input: string(input)}
}

// TestPermissionedTool_SettledPlanSkipsTheGate pins that a plan which
// already has an answer never reaches the user. Planning discovers
// things the model should read rather than the user approve — the file
// does not exist, the old string matched nothing, the edit changes
// nothing — and prompting for those would be noise.
func TestPermissionedTool_SettledPlanSkipsTheGate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	settled := fantasy.NewTextErrorResponse("old_string not found")
	inner := newPlanningTool(t, toolnames.Edit, tools.Plan{Response: &settled})

	svc := permission.NewPermissionService(dir, permission.ModeManual, nil)
	gated := newPermissionedTool(inner, svc, dir)

	events := svc.Subscribe(t.Context())
	resp, err := gated.Run(sessionCtx(t.Context()), editCall(t, filepath.Join(dir, "a.go")))
	require.NoError(t, err)

	require.Equal(t, settled.Content, resp.Content)
	require.Equal(t, 1, inner.planned)
	require.False(t, inner.call.called, "a settled plan must not be applied")
	require.Empty(t, events, "a settled plan must not prompt the user")
}

// TestPermissionedTool_PolicyDenyPrecedesPlanning pins the ordering that
// makes planning safe: planning opens the file it is about to change, so
// a denied path must be refused before any of that happens. Asserting
// only that the tool did not run would miss a plan that already read the
// secret.
func TestPermissionedTool_PolicyDenyPrecedesPlanning(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	policy, err := permission.CompilePolicy([]permission.Rule{
		{Action: permission.RuleDeny, Tool: toolnames.Edit, Pattern: "**/.env"},
	}, nil, permission.PromptAsk)
	require.NoError(t, err)

	applied := false
	inner := newPlanningTool(t, toolnames.Edit, tools.Plan{Apply: func(context.Context) (fantasy.ToolResponse, error) {
		applied = true
		return fantasy.NewTextResponse("wrote"), nil
	}})

	svc := permission.NewPermissionService(dir, permission.ModeManual, policy)
	gated := newPermissionedTool(inner, svc, dir)

	resp, err := gated.Run(sessionCtx(t.Context()), editCall(t, filepath.Join(dir, ".env")))
	require.NoError(t, err)

	require.True(t, resp.IsError)
	require.False(t, resp.StopTurn, "a policy refusal lets the model try another way")
	require.Zero(t, inner.planned, "a denied path must not be read to preview it")
	require.False(t, applied)
}

// TestPermissionedTool_RefusalKeepsPreviewMetadata pins that refusing a
// planned call still carries the tool's metadata back. The chat renders
// a refused edit together with its diff, rebuilt from the old and new
// content in this metadata (internal/ui/chat/file.go), so losing it
// would leave the user with an error and no sight of what they turned
// down.
func TestPermissionedTool_RefusalKeepsPreviewMetadata(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	inner := newPlanningTool(t, toolnames.Edit, tools.Plan{
		Preview: permission.Preview{Description: "Replace content"},
		Refusal: tools.EditResponseMetadata{
			Additions: 1, Removals: 1,
			OldContent: "a\n", NewContent: "b\n",
		},
		Apply: func(context.Context) (fantasy.ToolResponse, error) {
			return fantasy.NewTextResponse("wrote"), nil
		},
	})

	svc := permission.NewPermissionService(dir, permission.ModeManual, nil)
	gated := newPermissionedTool(inner, svc, dir)

	events := svc.Subscribe(t.Context())
	done := make(chan fantasy.ToolResponse, 1)
	go func() {
		resp, err := gated.Run(sessionCtx(t.Context()), editCall(t, filepath.Join(dir, "a.go")))
		require.NoError(t, err)
		done <- resp
	}()

	select {
	case ev := <-events:
		svc.Deny(ev.Payload)
	case <-t.Context().Done():
		t.Fatal("the gate never prompted")
	}

	resp := <-done
	require.True(t, resp.IsError)

	var meta tools.EditResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Equal(t, "a\n", meta.OldContent, "the refused diff must survive the refusal")
	require.Equal(t, "b\n", meta.NewContent)
}
