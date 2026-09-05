package permission

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// editAccess is an access that no rung of the ladder settles on its
// own, so every test using it reaches the prompt.
func editAccess(path string) Access {
	return Access{Tool: "edit", Action: ActionEdit, Path: path}
}

func gate(ctx context.Context, svc Service, sessionID, callID string, access Access) Decision {
	return svc.Gate(ctx, GateRequest{
		SessionID:  sessionID,
		ToolCallID: callID,
		Access:     access,
	})
}

// gateAsync runs a gate call that is expected to prompt, and returns a
// function that waits for its decision.
func gateAsync(ctx context.Context, svc Service, sessionID, callID string, access Access) func() Decision {
	var (
		wg       sync.WaitGroup
		decision Decision
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		decision = gate(ctx, svc, sessionID, callID, access)
	}()
	return func() Decision {
		wg.Wait()
		return decision
	}
}

func TestSkipRace(t *testing.T) {
	svc := NewPermissionService("/tmp", ModeManual, nil)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		svc.SetMode(ModeYolo)
	}()
	go func() {
		defer wg.Done()
		svc.Mode()
	}()
	wg.Wait()
}

func TestPermissionService_SkipMode(t *testing.T) {
	t.Parallel()

	service := NewPermissionService("/tmp", ModeYolo, nil)

	decision := gate(t.Context(), service, "test-session", "call-1", editAccess("/tmp/test.txt"))
	assert.True(t, decision.Allowed(), "skip mode should grant without prompting")
}

// TestPermissionService_AutoAcceptEditsMode pins that ModeAutoAcceptEdits
// only widens the ladder for edits: an edit is granted without a
// prompt, but every other action still runs the normal ladder.
func TestPermissionService_AutoAcceptEditsMode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outside := t.TempDir()
	service := NewPermissionService(dir, ModeAutoAcceptEdits, nil)

	edit := gate(t.Context(), service, "s1", "call-1", editAccess(filepath.Join(dir, "main.go")))
	assert.True(t, edit.Allowed(), "auto-accept-edits mode should grant an edit without prompting")

	events := service.Subscribe(t.Context())
	wait := gateAsync(t.Context(), service, "s1", "call-2", Access{
		Tool: "view", Action: ActionRead, Path: filepath.Join(outside, "secret"),
	})
	select {
	case ev := <-events:
		service.Deny(ev.Payload)
	case <-time.After(2 * time.Second):
		t.Fatal("a non-edit action must still reach the prompt under auto-accept-edits mode")
	}
	assert.False(t, wait().Allowed())
}

// TestPermissionService_DenyRuleOutranksAutoAcceptEdits pins that a
// deny rule still refuses an edit even when auto-accept-edits mode
// would otherwise grant it without asking.
func TestPermissionService_DenyRuleOutranksAutoAcceptEdits(t *testing.T) {
	t.Parallel()

	policy, err := CompilePolicy([]Rule{
		{Action: RuleDeny, Tool: "edit", Pattern: "**/.env"},
	}, nil, PromptAsk)
	require.NoError(t, err)

	service := NewPermissionService("/work", ModeAutoAcceptEdits, policy)

	decision := gate(t.Context(), service, "s1", "call-1", editAccess("/work/.env"))
	assert.Equal(t, OutcomePolicyDeny, decision.Outcome, "a deny rule must survive auto-accept-edits mode")

	other := gate(t.Context(), service, "s1", "call-2", editAccess("/work/main.go"))
	assert.True(t, other.Allowed())
}

// TestPermissionService_DenyRuleOutranksSkip pins the priority the
// user chose: a deny rule is the configuration's word, and turning off
// prompts must not turn it off too.
func TestPermissionService_DenyRuleOutranksSkip(t *testing.T) {
	t.Parallel()

	policy, err := CompilePolicy([]Rule{
		{Action: RuleDeny, Tool: "edit", Pattern: "**/.env"},
	}, nil, PromptAsk)
	require.NoError(t, err)

	service := NewPermissionService("/work", ModeYolo, policy)

	decision := gate(t.Context(), service, "s1", "call-1", editAccess("/work/.env"))
	assert.Equal(t, OutcomePolicyDeny, decision.Outcome,
		"a deny rule must survive --yolo")
	assert.False(t, decision.Allowed())

	// Anything the rule does not cover still rides the skip switch.
	other := gate(t.Context(), service, "s1", "call-2", editAccess("/work/main.go"))
	assert.True(t, other.Allowed())
}

// TestPermissionService_DangerousCommandAlwaysPrompts pins that no
// stored grant and no session policy can wave a dangerous verb
// through: the user has to see it every time.
func TestPermissionService_DangerousCommandAlwaysPrompts(t *testing.T) {
	t.Parallel()

	service := NewPermissionService("/work", ModeManual, nil)
	service.SetSessionPromptPolicy("s1", PromptAllow)

	access := Access{
		Tool:    "bash",
		Action:  ActionExecute,
		Command: "rm -rf build",
		Path:    "/work",
	}

	events := service.Subscribe(t.Context())
	wait := gateAsync(t.Context(), service, "s1", "call-1", access)

	select {
	case ev := <-events:
		assert.Contains(t, ev.Payload.Description, "rm -rf build")
		// Approving for the session must not mint a reusable grant.
		service.GrantPersistent(ev.Payload)
	case <-time.After(2 * time.Second):
		t.Fatal("a dangerous command must reach the prompt")
	}
	require.True(t, wait().Allowed())

	// The very same command must prompt again.
	wait2 := gateAsync(t.Context(), service, "s1", "call-2", access)
	select {
	case ev := <-events:
		service.Deny(ev.Payload)
	case <-time.After(2 * time.Second):
		t.Fatal("a dangerous command must prompt again despite the grant")
	}
	assert.Equal(t, OutcomeUserDeny, wait2().Outcome)
}

// TestPermissionService_SafeCommandNeedsNoPrompt pins that a read-only
// command confined to the working directory runs without asking.
func TestPermissionService_SafeCommandNeedsNoPrompt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	service := NewPermissionService(dir, ModeManual, nil)

	decision := gate(t.Context(), service, "s1", "call-1", Access{
		Tool:    "bash",
		Action:  ActionExecute,
		Command: "ls -la",
		Path:    dir,
	})
	assert.True(t, decision.Allowed(), "a read-only command should not prompt")
}

// TestPermissionService_ReadScope pins that reading inside the
// working directory is free while reading outside it is not.
func TestPermissionService_ReadScope(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outside := t.TempDir()
	skills := t.TempDir()
	service := NewPermissionService(dir, ModeManual, nil, skills)

	inside := gate(t.Context(), service, "s1", "c1", Access{
		Tool: "view", Action: ActionRead, Path: filepath.Join(dir, "main.go"),
	})
	assert.True(t, inside.Allowed(), "reads inside the working directory are free")

	skill := gate(t.Context(), service, "s1", "c2", Access{
		Tool: "view", Action: ActionRead, Path: filepath.Join(skills, "SKILL.md"),
	})
	assert.True(t, skill.Allowed(), "reads inside a skills directory are free")

	events := service.Subscribe(t.Context())
	wait := gateAsync(t.Context(), service, "s1", "c3", Access{
		Tool: "view", Action: ActionRead, Path: filepath.Join(outside, "secret"),
	})
	select {
	case ev := <-events:
		service.Deny(ev.Payload)
	case <-time.After(2 * time.Second):
		t.Fatal("a read outside the working directory must prompt")
	}
	assert.False(t, wait().Allowed())
}

// TestPermissionService_MergeAlwaysPrompts pins that a merge is never
// waved through by scope. It reaches no file and runs no command, so
// every heuristic that auto-allows harmless-looking access would let it
// past — but approving it is the user's only say over whether a branch
// ends and its result crosses back into the parent conversation.
func TestPermissionService_MergeAlwaysPrompts(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	service := NewPermissionService(dir, ModeManual, nil)

	events := service.Subscribe(t.Context())
	wait := gateAsync(t.Context(), service, "s1", "c1", Access{
		Tool: "merge", Action: ActionMerge,
	})
	select {
	case ev := <-events:
		service.Deny(ev.Payload)
	case <-time.After(2 * time.Second):
		t.Fatal("a merge must reach the user")
	}
	assert.Equal(t, OutcomeUserDeny, wait().Outcome)
}

// A rejected merge must not be remembered: the branch stays alive so the
// user can have it adjust the summary, and the retry has to ask again.
func TestPermissionService_MergeDenialIsNotRemembered(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	service := NewPermissionService(dir, ModeManual, nil)
	merge := Access{Tool: "merge", Action: ActionMerge}

	events := service.Subscribe(t.Context())

	first := gateAsync(t.Context(), service, "s1", "c1", merge)
	service.Deny((<-events).Payload)
	require.False(t, first().Allowed())

	second := gateAsync(t.Context(), service, "s1", "c2", merge)
	select {
	case ev := <-events:
		service.Grant(ev.Payload)
	case <-time.After(2 * time.Second):
		t.Fatal("a second merge must prompt again rather than inherit the refusal")
	}
	assert.True(t, second().Allowed())
}

// TestPermissionService_DenyOutcomesDiffer pins the semantic split: a
// refusal from the configuration is an obstacle the model may route
// around, while a refusal from the user ends the turn.
func TestPermissionService_DenyOutcomesDiffer(t *testing.T) {
	t.Parallel()

	policy, err := CompilePolicy([]Rule{
		{Action: RuleDeny, Tool: "edit", Pattern: "**/vendor/**"},
	}, nil, PromptAsk)
	require.NoError(t, err)
	service := NewPermissionService("/work", ModeManual, policy)

	byPolicy := gate(t.Context(), service, "s1", "c1", editAccess("/work/vendor/x.go"))
	assert.Equal(t, OutcomePolicyDeny, byPolicy.Outcome)
	assert.NotEmpty(t, byPolicy.Reason, "a policy denial must explain itself")

	events := service.Subscribe(t.Context())
	wait := gateAsync(t.Context(), service, "s1", "c2", editAccess("/work/main.go"))
	service.Deny((<-events).Payload)
	assert.Equal(t, OutcomeUserDeny, wait().Outcome)
}

// TestPermissionService_DenyReasonReachesDecision pins that the text a
// user attaches to a denial rides through Deny to the Decision the
// caller gets back, since that's the only path a typed explanation has
// to reach the model's tool response. A plain deny with no reason must
// leave Decision.Reason empty, matching the pre-existing behavior
// DecisionResponse relies on for its generic "User denied permission"
// text.
func TestPermissionService_DenyReasonReachesDecision(t *testing.T) {
	t.Parallel()

	service := NewPermissionService("/work", ModeManual, nil)

	events := service.Subscribe(t.Context())
	wait := gateAsync(t.Context(), service, "s1", "c1", editAccess("/work/a.go"))
	request := (<-events).Payload
	request.DenyReason = "not needed for this task"
	service.Deny(request)
	decision := wait()
	assert.Equal(t, OutcomeUserDeny, decision.Outcome)
	assert.Equal(t, "not needed for this task", decision.Reason)

	events2 := service.Subscribe(t.Context())
	wait2 := gateAsync(t.Context(), service, "s2", "c2", editAccess("/work/b.go"))
	service.Deny((<-events2).Payload)
	decision2 := wait2()
	assert.Equal(t, OutcomeUserDeny, decision2.Outcome)
	assert.Empty(t, decision2.Reason, "a plain deny must not invent a reason")
}

// TestPermissionService_SessionsPromptConcurrently pins that one
// session waiting on the user does not block another. The old service
// held a single global lock for the whole prompt.
func TestPermissionService_SessionsPromptConcurrently(t *testing.T) {
	t.Parallel()

	service := NewPermissionService("/work", ModeManual, nil)
	events := service.Subscribe(t.Context())

	waitA := gateAsync(t.Context(), service, "session-a", "a1", editAccess("/work/a.go"))
	waitB := gateAsync(t.Context(), service, "session-b", "b1", editAccess("/work/b.go"))

	pending := map[string]PermissionRequest{}
	for range 2 {
		select {
		case ev := <-events:
			pending[ev.Payload.SessionID] = ev.Payload
		case <-time.After(2 * time.Second):
			t.Fatal("both sessions should be able to prompt at once")
		}
	}
	require.Len(t, pending, 2, "each session must get its own prompt")

	service.Grant(pending["session-a"])
	service.Deny(pending["session-b"])

	assert.True(t, waitA().Allowed())
	assert.Equal(t, OutcomeUserDeny, waitB().Outcome)
}

// TestPermissionService_CancelledWhileQueued pins that a caller whose
// context ends while it waits for the session's prompt slot gives up
// its place instead of deadlocking behind the request ahead of it.
func TestPermissionService_CancelledWhileQueued(t *testing.T) {
	t.Parallel()

	service := NewPermissionService("/work", ModeManual, nil)
	events := service.Subscribe(t.Context())

	// First request takes the session's slot and holds it.
	holder := gateAsync(t.Context(), service, "s1", "c1", editAccess("/work/a.go"))
	var first PermissionRequest
	select {
	case ev := <-events:
		first = ev.Payload
	case <-time.After(2 * time.Second):
		t.Fatal("first request never prompted")
	}

	// Second request queues behind it, then is cancelled.
	ctx, cancel := context.WithCancel(t.Context())
	queued := gateAsync(ctx, service, "s1", "c2", editAccess("/work/b.go"))
	cancel()
	assert.Equal(t, OutcomeCancelled, queued().Outcome)

	service.Grant(first)
	assert.True(t, holder().Allowed())
}

func TestPermissionService_HookApproval(t *testing.T) {
	t.Parallel()

	t.Run("matching tool call ID short-circuits the prompt", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService("/work", ModeManual, nil)

		ctx := WithHookApproval(t.Context(), "call-42")
		decision := gate(ctx, service, "s1", "call-42", editAccess("/work/a.go"))
		assert.True(t, decision.Allowed(), "hook-approved call should bypass the prompt")
	})

	t.Run("approval is scoped to the stamped tool call ID", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService("/work", ModeManual, nil)

		ctx := WithHookApproval(t.Context(), "call-42")
		events := service.Subscribe(t.Context())
		wait := gateAsync(ctx, service, "s1", "call-other", editAccess("/work/a.go"))

		service.Deny((<-events).Payload)
		assert.False(t, wait().Allowed(),
			"stamped approval must not apply to a different tool call")
	})

	t.Run("a hook cannot approve a dangerous command", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService("/work", ModeManual, nil)

		ctx := WithHookApproval(t.Context(), "call-7")
		events := service.Subscribe(t.Context())
		wait := gateAsync(ctx, service, "s1", "call-7", Access{
			Tool: "bash", Action: ActionExecute, Command: "sudo rm -rf /", Path: "/work",
		})

		select {
		case ev := <-events:
			service.Deny(ev.Payload)
		case <-time.After(2 * time.Second):
			t.Fatal("a dangerous command must prompt even when a hook approved it")
		}
		assert.False(t, wait().Allowed())
	})

	t.Run("notifies subscribers that permission was granted", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService("/work", ModeManual, nil)

		notifications := service.SubscribeNotifications(t.Context())

		ctx := WithHookApproval(t.Context(), "call-99")
		decision := gate(ctx, service, "s1", "call-99", editAccess("/work/a.go"))
		require.True(t, decision.Allowed())

		event := <-notifications
		assert.Equal(t, "call-99", event.Payload.ToolCallID)
		assert.True(t, event.Payload.Granted, "subscribers should see a granted notification")
	})
}

func TestPermissionService_SequentialProperties(t *testing.T) {
	t.Parallel()

	t.Run("persistent grant covers the repeat", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService("/work", ModeManual, nil)
		events := service.Subscribe(t.Context())

		access := editAccess("/work/test.txt")
		wait := gateAsync(t.Context(), service, "session1", "c1", access)
		service.GrantPersistent((<-events).Payload)
		require.True(t, wait().Allowed(), "first request should be granted")

		repeat := gate(t.Context(), service, "session1", "c2", access)
		assert.True(t, repeat.Allowed(), "second request should be auto-approved")
	})

	t.Run("a one-off grant does not cover the repeat", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService("/work", ModeManual, nil)
		events := service.Subscribe(t.Context())

		access := editAccess("/work/test.txt")
		wait := gateAsync(t.Context(), service, "session2", "c1", access)
		service.Grant((<-events).Payload)
		require.True(t, wait().Allowed(), "first request should be granted")

		wait2 := gateAsync(t.Context(), service, "session2", "c2", access)
		service.Deny((<-events).Payload)
		assert.False(t, wait2().Allowed(), "second request should be denied")
	})

	t.Run("a grant does not leak across sessions", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService("/work", ModeManual, nil)
		events := service.Subscribe(t.Context())

		access := editAccess("/work/test.txt")
		wait := gateAsync(t.Context(), service, "session-a", "c1", access)
		service.GrantPersistent((<-events).Payload)
		require.True(t, wait().Allowed())

		wait2 := gateAsync(t.Context(), service, "session-b", "c2", access)
		select {
		case ev := <-events:
			service.Deny(ev.Payload)
		case <-time.After(2 * time.Second):
			t.Fatal("another session must not inherit the grant")
		}
		assert.False(t, wait2().Allowed())
	})
}

// TestPermissionService_ResolveIdempotency covers the multi-subscriber
// resolve guarantees added for client/server mode: exactly one
// notification per resolution, racing callers see "already resolved",
// and stray Grant/Deny calls for unknown IDs are safe no-ops.
func TestPermissionService_ResolveIdempotency(t *testing.T) {
	t.Parallel()

	t.Run("concurrent grants resolve exactly once", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService("/work", ModeManual, nil)

		events := service.Subscribe(t.Context())
		notifications := service.SubscribeNotifications(t.Context())

		wait := gateAsync(t.Context(), service, "race-session", "race-call",
			editAccess("/work/race"))

		var pending PermissionRequest
		select {
		case ev := <-events:
			pending = ev.Payload
		case <-time.After(2 * time.Second):
			t.Fatal("permission request was never published")
		}

		// Drain the initial "request opened" notification (Granted ==
		// false && Denied == false) so the next read is the resolution
		// itself.
		select {
		case ev := <-notifications:
			require.False(t, ev.Payload.Granted, "initial notification must not be granted")
			require.False(t, ev.Payload.Denied, "initial notification must not be denied")
		case <-time.After(2 * time.Second):
			t.Fatal("initial notification was never published")
		}

		// Race two grants from two goroutines.
		var (
			resolvedCount atomic.Int32
			start         = make(chan struct{})
			racers        sync.WaitGroup
		)
		for range 2 {
			racers.Go(func() {
				<-start
				if service.Grant(pending) {
					resolvedCount.Add(1)
				}
			})
		}
		close(start)
		racers.Wait()

		assert.True(t, wait().Allowed(), "request should observe its grant")

		assert.Equal(t, int32(1), resolvedCount.Load(),
			"exactly one Grant should report it resolved the request")

		select {
		case ev := <-notifications:
			assert.True(t, ev.Payload.Granted, "resolution notification should be granted")
			assert.Equal(t, "race-call", ev.Payload.ToolCallID)
		case <-time.After(2 * time.Second):
			t.Fatal("resolution notification was never published")
		}
		select {
		case ev := <-notifications:
			t.Fatalf("unexpected duplicate notification: %+v", ev.Payload)
		case <-time.After(50 * time.Millisecond):
			// good: no duplicate.
		}

		ps := service.(*permissionService)
		assert.Equal(t, 0, ps.pendingRequests.Len(),
			"pendingRequests must be empty after resolution")

		assert.False(t, service.Grant(pending),
			"a third Grant should report already-resolved")
	})

	t.Run("grant after deny is a no-op", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService("/work", ModeManual, nil)

		events := service.Subscribe(t.Context())
		notifications := service.SubscribeNotifications(t.Context())

		wait := gateAsync(t.Context(), service, "deny-first", "df-call",
			editAccess("/work/df"))

		var pending PermissionRequest
		select {
		case ev := <-events:
			pending = ev.Payload
		case <-time.After(2 * time.Second):
			t.Fatal("permission request was never published")
		}

		<-notifications

		assert.True(t, service.Deny(pending), "Deny should resolve the request")
		assert.Equal(t, OutcomeUserDeny, wait().Outcome)

		assert.False(t, service.Grant(pending),
			"Grant after Deny should report already-resolved")

		select {
		case ev := <-notifications:
			require.True(t, ev.Payload.Denied,
				"the only post-initial notification must be the denial")
		case <-time.After(2 * time.Second):
			t.Fatal("denial notification was never published")
		}
		select {
		case ev := <-notifications:
			t.Fatalf("Grant after Deny must not publish: %+v", ev.Payload)
		case <-time.After(50 * time.Millisecond):
			// good.
		}
	})

	t.Run("losing GrantPersistent does not record session permission", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService("/work", ModeManual, nil)

		events := service.Subscribe(t.Context())
		notifications := service.SubscribeNotifications(t.Context())

		access := editAccess("/work/rp")
		wait := gateAsync(t.Context(), service, "race-persist", "rp-call", access)

		var pending PermissionRequest
		select {
		case ev := <-events:
			pending = ev.Payload
		case <-time.After(2 * time.Second):
			t.Fatal("permission request was never published")
		}

		<-notifications

		// Deny wins, then a competing GrantPersistent loses.
		assert.True(t, service.Deny(pending), "Deny should resolve the request")
		assert.False(t, service.GrantPersistent(pending),
			"GrantPersistent after Deny should report already-resolved")
		assert.False(t, wait().Allowed(), "request should observe denial")

		// The losing GrantPersistent must not have inserted an
		// auto-approve entry, so a matching follow-up still prompts.
		wait2 := gateAsync(t.Context(), service, "race-persist", "rp-call-2", access)
		select {
		case ev := <-events:
			service.Deny(ev.Payload)
		case <-time.After(2 * time.Second):
			t.Fatal("follow-up request was auto-approved; persistent grant leaked")
		}
		assert.False(t, wait2().Allowed(),
			"follow-up request should be denied, not auto-approved")
	})

	t.Run("grant for unknown id is a safe no-op", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService("/work", ModeManual, nil)

		notifications := service.SubscribeNotifications(t.Context())

		bogus := PermissionRequest{
			ID:         "does-not-exist",
			ToolCallID: "ghost",
			ToolName:   "tool",
			Action:     "act",
			Path:       "/work/ghost",
		}

		assert.NotPanics(t, func() {
			assert.False(t, service.Grant(bogus),
				"Grant for unknown ID should report already-resolved")
			assert.False(t, service.GrantPersistent(bogus),
				"GrantPersistent for unknown ID should report already-resolved")
			assert.False(t, service.Deny(bogus),
				"Deny for unknown ID should report already-resolved")
		})

		select {
		case ev := <-notifications:
			t.Fatalf("unknown-ID resolution must not publish: %+v", ev.Payload)
		case <-time.After(50 * time.Millisecond):
			// good: no notification.
		}
	})
}

// TestPermissionService_GrantScopeIsPerPath pins that approving one
// file does not approve its neighbours. The scope used to be derived
// with filepath.Dir, and only when the file already existed, so the
// same tool and action silently changed granularity.
func TestPermissionService_GrantScopeIsPerPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	approved := filepath.Join(dir, "approved.go")
	neighbour := filepath.Join(dir, "neighbour.go")

	service := NewPermissionService(dir, ModeManual, nil)
	events := service.Subscribe(t.Context())

	wait := gateAsync(t.Context(), service, "session1", "c1", editAccess(approved))
	service.GrantPersistent((<-events).Payload)
	require.True(t, wait().Allowed())

	// The same file is now covered by the grant.
	repeat := gate(t.Context(), service, "session1", "c2", editAccess(approved))
	require.True(t, repeat.Allowed())

	// A sibling in the same directory is not.
	wait2 := gateAsync(t.Context(), service, "session1", "c3", editAccess(neighbour))
	prompted := <-events
	require.Equal(t, neighbour, prompted.Payload.Path)
	service.Deny(prompted.Payload)
	assert.False(t, wait2().Allowed(), "a sibling file must be asked about separately")
}

// TestPermissionService_CommandGrantScope pins how far a session grant
// for a command reaches: it follows the verb and its flags, and never
// spills onto a different command.
func TestPermissionService_CommandGrantScope(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	service := NewPermissionService(dir, ModeManual, nil).(*permissionService)

	tests := []struct {
		name    string
		command string
		want    string
	}{
		{"safe verb keeps its prefix", "git status --short", "git status"},
		{"ordinary command keeps verb and flags", "cargo -q build", "cargo -q"},
		{"dangerous verb is pinned whole", "rm -rf build", "rm -rf build"},
		{"a command that can carry another is pinned whole", "sudo apt install x", "sudo apt install x"},
		{"a build runner is pinned whole", "make -j4 build", "make -j4 build"},
		{"a chain is pinned whole", "make && ./run", "make && ./run"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := service.grantScope(Access{
				Tool: "bash", Action: ActionExecute, Command: tt.command, Path: dir,
			})
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestDownloadGrantCoversWhereItLands pins that approving one download
// approves where it landed, not the whole host. Without this, allowing
// a file into the workspace would silently allow the next one into any
// directory the process can write.
func TestDownloadGrantCoversWhereItLands(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	outside := t.TempDir()

	download := func(dir, name string) Access {
		return Access{
			Tool:   "download",
			Action: ActionNetwork,
			URL:    "https://example.com/" + name,
			Path:   filepath.Join(dir, name),
		}
	}

	t.Run("a grant does not follow the host to another directory", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService(workDir, ModeManual, nil)
		events := service.Subscribe(t.Context())

		wait := gateAsync(t.Context(), service, "s1", "c1", download(workDir, "a.sh"))
		service.GrantPersistent((<-events).Payload)
		require.True(t, wait().Allowed())

		// Same host, a directory the user never saw: must ask again.
		wait2 := gateAsync(t.Context(), service, "s1", "c2", download(outside, "b.sh"))
		select {
		case ev := <-events:
			service.Deny(ev.Payload)
		case <-t.Context().Done():
			t.Fatal("the second download never prompted; the host grant covered it")
		}
		require.False(t, wait2().Allowed())
	})

	t.Run("a grant still covers the same directory", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService(workDir, ModeManual, nil)
		events := service.Subscribe(t.Context())

		wait := gateAsync(t.Context(), service, "s2", "c1", download(outside, "a.sh"))
		service.GrantPersistent((<-events).Payload)
		require.True(t, wait().Allowed())

		repeat := gate(t.Context(), service, "s2", "c2", download(outside, "b.sh"))
		assert.True(t, repeat.Allowed(),
			"a batch of downloads into one directory must not prompt per file")
	})

	t.Run("a fetch that writes nothing still grants by host", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService(workDir, ModeManual, nil).(*permissionService)

		first := Access{Tool: "web_fetch", Action: ActionNetwork, URL: "https://example.com/a"}
		second := Access{Tool: "web_fetch", Action: ActionNetwork, URL: "https://example.com/b"}
		assert.Equal(t, service.grantScope(first), service.grantScope(second))
	})
}

// TestCommandGrantCoversWhereItRuns pins that approving a command
// approves it where the user watched it run. The same words do
// different things in different places — `go build ./...` builds one
// project in the workspace and another somewhere else — so a grant
// carried by the words alone would run the second on the strength of
// the user having agreed to the first.
func TestCommandGrantCoversWhereItRuns(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	outside := t.TempDir()

	command := func(dir, c string) Access {
		return Access{Tool: "bash", Action: ActionExecute, Command: c, Path: dir}
	}

	t.Run("a grant does not follow the command elsewhere", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService(workDir, ModeManual, nil)
		events := service.Subscribe(t.Context())

		wait := gateAsync(t.Context(), service, "s1", "c1", command(workDir, "go build ./..."))
		service.GrantPersistent((<-events).Payload)
		require.True(t, wait().Allowed())

		// The same words against a directory the user never saw.
		wait2 := gateAsync(t.Context(), service, "s1", "c2", command(outside, "go build ./..."))
		select {
		case ev := <-events:
			service.Deny(ev.Payload)
		case <-t.Context().Done():
			t.Fatal("the second run never prompted; the grant travelled with the words")
		}
		require.False(t, wait2().Allowed())
	})

	t.Run("a grant still covers the directory it was given for", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService(workDir, ModeManual, nil)
		events := service.Subscribe(t.Context())

		wait := gateAsync(t.Context(), service, "s2", "c1", command(workDir, "go build ./..."))
		service.GrantPersistent((<-events).Payload)
		require.True(t, wait().Allowed())

		repeat := gate(t.Context(), service, "s2", "c2", command(workDir, "go build ./cmd/..."))
		assert.True(t, repeat.Allowed(),
			"the same command in the approved directory must not prompt again")
	})

	t.Run("two spellings of one directory share the approval", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService(workDir, ModeManual, nil)
		events := service.Subscribe(t.Context())

		link := filepath.Join(t.TempDir(), "link")
		require.NoError(t, os.Symlink(workDir, link))

		wait := gateAsync(t.Context(), service, "s3", "c1", command(workDir, "go build ./..."))
		service.GrantPersistent((<-events).Payload)
		require.True(t, wait().Allowed())

		repeat := gate(t.Context(), service, "s3", "c2", command(link, "go build ./..."))
		assert.True(t, repeat.Allowed(),
			"a link to the approved directory is the approved directory")
	})
}

// TestChdirInsideTheCommandIsFollowed pins the other half of "where
// does this run": the working_dir parameter is only the starting point,
// and `cd` in the command string moves it. A check that trusted the
// parameter alone would clear `cd /elsewhere && ...` on the strength of
// a directory the command leaves in its first word.
func TestChdirInsideTheCommandIsFollowed(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(workDir, "sub"), 0o755))

	service := NewPermissionService(workDir, ModeManual, nil).(*permissionService)
	scoped := func(command string) bool {
		_, ok := service.withinScope(Access{
			Tool: "bash", Action: ActionExecute, Command: command, Path: workDir,
		})
		return ok
	}

	t.Run("cd within the workspace stays quiet", func(t *testing.T) {
		t.Parallel()
		assert.True(t, scoped("cd sub && git status"))
	})

	t.Run("cd to an absolute path outside is the user's call", func(t *testing.T) {
		t.Parallel()
		assert.False(t, scoped("cd "+outside+" && git status"))
	})

	t.Run("cd out by a relative path is the user's call", func(t *testing.T) {
		t.Parallel()
		assert.False(t, scoped("cd .. && git status"))
	})

	t.Run("operands after cd resolve against the directory it moved to", func(t *testing.T) {
		t.Parallel()
		// From sub/, "../x" is workDir/x — inside. Reading it against
		// the starting directory instead would place it above the
		// workspace and refuse a file the user plainly allowed.
		assert.True(t, scoped("cd sub && cat ../x"))
	})

	t.Run("cd does not buy a way out through relative operands", func(t *testing.T) {
		t.Parallel()
		assert.False(t, scoped("cd sub && cat ../../../etc/passwd"))
	})

	t.Run("a chain is pinned whole, so no prefix of it is granted", func(t *testing.T) {
		t.Parallel()
		chain := "cd " + outside + " && git status"
		assert.Equal(t, chain, service.grantScope(Access{
			Tool: "bash", Action: ActionExecute, Command: chain, Path: workDir,
		}), "approving a chain must not mint a grant that a bare verb can match")
	})
}

// TestReadOnlyCommandsStayInTheWorkspace pins that the quiet path for a
// read-only command is the workspace, not the machine. A command with
// no file operands — `git status`, `ls` with no argument — reads
// whatever directory it runs in, so running it elsewhere reports on a
// project the user never pointed Angela at.
func TestReadOnlyCommandsStayInTheWorkspace(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	outside := t.TempDir()
	service := NewPermissionService(workDir, ModeManual, nil).(*permissionService)

	t.Run("inside the workspace it runs unprompted", func(t *testing.T) {
		t.Parallel()
		_, ok := service.withinScope(Access{
			Tool: "bash", Action: ActionExecute, Command: "git status", Path: workDir,
		})
		assert.True(t, ok, "looking around the workspace is the job")
	})

	t.Run("outside the workspace it is the user's call", func(t *testing.T) {
		t.Parallel()
		_, ok := service.withinScope(Access{
			Tool: "bash", Action: ActionExecute, Command: "git status", Path: outside,
		})
		assert.False(t, ok,
			"git status reports on whatever repository it runs in")
	})
}

// settle runs a gate call that must not need anyone, and fails the test
// if it parks on a prompt instead of deciding. A plain assertion on the
// outcome cannot tell "refused" from "still waiting" — a blocked call
// only surfaces once the context dies, which in a headless run means
// the whole invocation is spent before anyone learns why.
func settle(t *testing.T, svc Service, sessionID string, access Access) Decision {
	t.Helper()
	done := make(chan Decision, 1)
	go func() { done <- gate(t.Context(), svc, sessionID, "call-1", access) }()
	select {
	case decision := <-done:
		return decision
	case <-time.After(5 * time.Second):
		t.Fatal("gate blocked waiting for an approval nothing in this session can give")
		return Decision{}
	}
}

func forcedCommand(command string) Access {
	return Access{Tool: "bash", Action: ActionExecute, Command: command, Path: "/work"}
}

// TestUnattendedSessionRefusesRatherThanWaits covers the headless case:
// `angela run`, CI, anything piping a prompt in. Such a session
// pre-approves itself with PromptAllow, but that only settles requests
// that can be settled without asking — a dangerous or unreadable
// command insists on a prompt regardless. With nothing subscribed to
// answer, reaching that prompt parks the run until its context dies.
func TestUnattendedSessionRefusesRatherThanWaits(t *testing.T) {
	t.Parallel()

	unattendedRun := func(t *testing.T) Service {
		t.Helper()
		service := NewPermissionService("/work", ModeManual, nil)
		// Exactly what App.RunNonInteractive does.
		service.SetSessionPromptPolicy("s1", PromptAllow)
		service.SetSessionUnattended("s1", true)
		return service
	}

	t.Run("a dangerous verb is refused, not awaited", func(t *testing.T) {
		t.Parallel()
		decision := settle(t, unattendedRun(t), "s1", forcedCommand("rm -rf build"))

		assert.Equal(t, OutcomePolicyDeny, decision.Outcome)
		assert.False(t, decision.Allowed())
	})

	t.Run("a command that cannot be read is refused, not awaited", func(t *testing.T) {
		t.Parallel()
		decision := settle(t, unattendedRun(t), "s1", forcedCommand("echo $(whoami)"))

		assert.Equal(t, OutcomePolicyDeny, decision.Outcome)
		assert.False(t, decision.Allowed())
	})

	t.Run("the refusal says why, and keeps the original reason", func(t *testing.T) {
		t.Parallel()
		decision := settle(t, unattendedRun(t), "s1", forcedCommand("rm -rf build"))

		assert.Contains(t, decision.Reason, "rm",
			"the reason must still name what tripped the prompt")
		assert.Contains(t, decision.Reason, "attached",
			"the reason must say nobody could have approved it")
	})

	t.Run("work that needs no prompt still runs", func(t *testing.T) {
		t.Parallel()
		// The point of PromptAllow is that a headless run works at all.
		// Refusing the unanswerable must not refuse the ordinary.
		decision := settle(t, unattendedRun(t), "s1", forcedCommand("git status"))

		assert.True(t, decision.Allowed(),
			"an unattended session must still do the work it can settle alone")
	})

	t.Run("a deny rule still outranks everything", func(t *testing.T) {
		t.Parallel()
		policy, err := CompilePolicy([]Rule{{Action: RuleDeny, Tool: "edit"}}, nil, PromptAsk)
		require.NoError(t, err)
		service := NewPermissionService("/work", ModeManual, policy)
		service.SetSessionPromptPolicy("s1", PromptAllow)
		service.SetSessionUnattended("s1", true)

		decision := settle(t, service, "s1", editAccess("/work/main.go"))
		assert.Equal(t, OutcomePolicyDeny, decision.Outcome)
	})
}

// TestUnattendedIsInheritedByDispatchedWork pins the rule a sub-agent
// dispatch relies on: whether anyone can answer depends on where the
// run started, not on how deep it nested. A child of a headless run is
// itself headless, all the way down; a child of a session someone is
// watching still gets to ask.
func TestUnattendedIsInheritedByDispatchedWork(t *testing.T) {
	t.Parallel()

	// What Coordinator does when it spawns a sub-agent session.
	dispatch := func(svc Service, parent, child string) {
		svc.SetSessionPromptPolicy(child, PromptAllow)
		svc.SetSessionUnattended(child, svc.SessionUnattended(parent))
	}

	t.Run("a child of a headless run refuses too", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService("/work", ModeManual, nil)
		service.SetSessionUnattended("root", true)

		dispatch(service, "root", "child")
		dispatch(service, "child", "grandchild")

		require.True(t, service.SessionUnattended("grandchild"),
			"nesting must not lose track of there being no one to ask")
		decision := settle(t, service, "grandchild", forcedCommand("rm -rf build"))
		assert.Equal(t, OutcomePolicyDeny, decision.Outcome)
	})

	t.Run("a child of a watched session still asks", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService("/work", ModeManual, nil)
		// A TUI session: nothing marks it unattended.
		dispatch(service, "root", "child")

		require.False(t, service.SessionUnattended("child"))

		events := service.Subscribe(t.Context())
		wait := gateAsync(t.Context(), service, "child", "call-1", forcedCommand("rm -rf build"))

		select {
		case ev := <-events:
			assert.Contains(t, ev.Payload.Description, "rm -rf build")
			service.Grant(ev.Payload)
		case <-time.After(5 * time.Second):
			t.Fatal("a dispatched sub-agent must still be able to ask the user")
		}
		assert.True(t, wait().Allowed())
	})
}

// TestCommandsThatLeaveTheMachineReachTheUser pins at the gate what the
// scan pins on its own: reading a cluster or a git remote is not the
// same as reading the workspace, and the scope check must not settle it
// on the user's behalf.
//
// An unattended session makes the answer observable without a
// subscriber: whatever the scope check would have waved through comes
// back allowed, and whatever needs a human comes back refused rather
// than parking on a prompt nobody is there to answer.
func TestCommandsThatLeaveTheMachineReachTheUser(t *testing.T) {
	t.Parallel()

	command := func(c string) Access {
		return Access{Tool: "bash", Action: ActionExecute, Command: c, Path: "/work"}
	}

	t.Run("a remote read is not auto-approved", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService("/work", ModeManual, nil)
		service.SetSessionUnattended("s", true)

		for _, c := range []string{
			"kubectl get secrets -o yaml",
			"kubectl logs some-pod",
			"git ls-remote https://private.example/repo",
		} {
			decision := settle(t, service, "s", command(c))
			assert.False(t, decision.Allowed(),
				"%q leaves the machine and must be the user's call", c)
		}
	})

	t.Run("a local read-only command still runs unprompted", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService("/work", ModeManual, nil)
		service.SetSessionUnattended("s", true)

		for _, c := range []string{"ls -la", "git status", "git log --oneline"} {
			decision := settle(t, service, "s", command(c))
			assert.True(t, decision.Allowed(),
				"%q stays on the machine and looking around is the job", c)
		}
	})

	t.Run("an explicit rule still allows them", func(t *testing.T) {
		t.Parallel()
		policy := mustPolicy(t, []Rule{
			{Action: RuleAllow, Tool: "bash", Pattern: "kubectl get *"},
		}, nil)
		service := NewPermissionService("/work", ModeManual, policy)
		service.SetSessionUnattended("s", true)

		decision := settle(t, service, "s", command("kubectl get pods"))
		assert.True(t, decision.Allowed(),
			"taking it off the safe list must leave it configurable")
	})
}
