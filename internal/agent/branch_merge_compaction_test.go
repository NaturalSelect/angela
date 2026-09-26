package agent

import (
	"errors"
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// mergeToolCallThenFinish streams a single Merge tool call followed
// immediately by a finish reporting usage. Unlike toolCallThenFinish's
// placeholder "some_tool" (which names no real tool and so is left
// pending, simulating a step cut off before its call ever runs), this
// names a tool that is actually registered on the call's Tools, so it
// executes within the step and the step's own tool results carry
// whatever StopTurn the merge sets.
func mergeToolCallThenFinish(usage fantasy.Usage) fantasy.StreamResponse {
	return func(yield func(fantasy.StreamPart) bool) {
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolCall, ID: "call-merge", ToolCallName: toolnames.Merge, ToolCallInput: "{}"}) {
			return
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonToolCalls, Usage: usage})
	}
}

// branchMergeCompactionEnv wires a real sessionAgent in as a
// coordinator's currentAgent, the way a real branch's executor is
// wired, so a merge tool call routed through that coordinator's
// ClearQueue reaches the very session agent under test instead of a
// stand-in.
func branchMergeCompactionEnv(t *testing.T) (*sessionAgent, *coordinator, fakeEnv) {
	t.Helper()
	sa, env := summarizeGomockEnv(t)
	c := newTestCoordinator(t, env, branchProviderID, config.ProviderConfig{ID: branchProviderID})
	c.currentAgent = sa
	return sa, c, env
}

// TestRun_MergeCrossingCompactionThresholdDoesNotSummarizeOrResume is
// the regression for a reported bug where a branch that merged right
// as its closing turn crossed the auto-compaction threshold got
// summarized and then resumed with a spurious "previous session was
// interrupted" turn, even though the merge had already resolved the
// branch and handed its result back to the parent. The resumed turn
// went on to call Merge a second time, which failed with "No
// conversation is waiting on this branch any more" because the first
// merge had already taken the rendezvous — by then wasted work, since
// the parent had already moved on with the first result.
func TestRun_MergeCrossingCompactionThresholdDoesNotSummarizeOrResume(t *testing.T) {
	t.Parallel()

	sa, c, env := branchMergeCompactionEnv(t)
	sess, err := env.sessions.Create(t.Context(), "branch")
	require.NoError(t, err)

	done := c.branches.Register(sess.ID, "parent-1")
	c.proposals.Set(sess.ID, "THE PROPOSAL")

	// A small context window with heavily-reported usage forces the
	// StopWhen condition to fire on the very step that merges, the
	// same way a real branch's closing turn can happen to land right
	// at the compaction threshold.
	model := newMockLanguageModel(t)
	model.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(mergeToolCallThenFinish(fantasy.Usage{InputTokens: 900}), nil)

	// No Stream expectation is set on the compact model: if the fix
	// regresses, Summarize would call it and gomock fails the test for
	// an unexpected call. Only one expectation is set on the main
	// model too, so a spurious resumed turn would fail the test the
	// same way.
	compactModel := newMockLanguageModel(t)
	catwalkCfg := config.ProviderModel{Model: catwalk.Model{ContextWindow: 1000, DefaultMaxTokens: 500}}
	compact := resolvedAgent{
		Model:        Model{Model: compactModel, CatwalkCfg: catwalkCfg},
		SystemPrompt: "summarize",
	}

	res, err := sa.Run(t.Context(), SessionAgentCall{
		Agent: resolvedAgent{
			ID:        config.AgentCoder,
			Model:     Model{Model: model, CatwalkCfg: catwalkCfg},
			Tools:     []fantasy.AgentTool{c.mergeTool()},
			MaxTokens: catwalkCfg.DefaultMaxTokens,
		},
		Compact:   compact,
		SessionID: sess.ID,
		RunID:     "run-1",
		Prompt:    "wrap it up",
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	out := <-done
	require.True(t, out.Merged)
	require.Equal(t, "THE PROPOSAL", out.Payload)

	updated, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Empty(t, updated.SummaryMessageID,
		"a branch that just merged has no future turn a compacted history could ever help")

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)

	var userPrompts []string
	for _, m := range msgs {
		if m.Role == message.User {
			userPrompts = append(userPrompts, m.Content().Text)
		}
	}
	require.Len(t, userPrompts, 1,
		"a successful merge must not be treated as a turn cut short mid-tool-use and queued for resume")
	require.Equal(t, "wrap it up", userPrompts[0])

	assistant := findAssistant(t, msgs)
	require.Equal(t, message.FinishReasonEndTurn, assistant.FinishReason(),
		"a successful merge must end the turn, not leave it looking cut off")

	toolResult := findToolResult(t, msgs)
	require.False(t, toolResult.IsError)
	require.Contains(t, toolResult.Content, "THE PROPOSAL")

	_, queued := sa.messageQueue.Get(sess.ID)
	require.False(t, queued, "a merged branch must not be queued for a resume that will never come")
}

// TestRun_DeniedMergeCrossingCompactionThresholdStillSummarizes guards
// the fix above against being too broad. A merge the user denies
// leaves the branch alive with its rendezvous still open (see
// TestMergeToolDeniedNeverRuns), so unlike a successful merge it must
// still be compacted like any other ongoing turn: the fix has to key
// off whether the merge actually succeeded, not merely off its tool
// name matching Merge.
func TestRun_DeniedMergeCrossingCompactionThresholdStillSummarizes(t *testing.T) {
	t.Parallel()

	sa, c, env := branchMergeCompactionEnv(t)
	sess, err := env.sessions.Create(t.Context(), "branch")
	require.NoError(t, err)

	done := c.branches.Register(sess.ID, "parent-1")
	c.proposals.Set(sess.ID, "THE PROPOSAL")

	dir := t.TempDir()
	svc := permission.NewPermissionService(dir, permission.ModeManual, nil)
	gated := newPermissionedTool(c.mergeTool(), svc, dir, nil)

	model := newMockLanguageModel(t)
	model.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(mergeToolCallThenFinish(fantasy.Usage{InputTokens: 900}), nil)

	compactModel := newMockLanguageModel(t)
	compactModel.EXPECT().Stream(gomock.Any(), gomock.Any()).
		Return(streamOf([]string{"<summary>summary</summary>"}, fantasy.FinishReasonStop), nil).
		Times(1)

	catwalkCfg := config.ProviderModel{Model: catwalk.Model{ContextWindow: 1000, DefaultMaxTokens: 500}}
	compact := resolvedAgent{
		Model:        Model{Model: compactModel, CatwalkCfg: catwalkCfg},
		SystemPrompt: "summarize",
	}

	events := svc.Subscribe(t.Context())
	type runOutcome struct {
		res *fantasy.AgentResult
		err error
	}
	result := make(chan runOutcome, 1)
	go func() {
		// A gomock assertion failure inside sa.Run (an unexpected
		// Stream call) fails the test via runtime.Goexit, which
		// unwinds only this goroutine and skips straight past the
		// send below. This deferred send still runs during that
		// unwind, carrying the sentinel outcome set beforehand, so
		// the main goroutine is never left blocked on a result that
		// would otherwise never arrive.
		outcome := runOutcome{err: errors.New("sa.Run goroutine exited early without completing, likely a failed mock assertion")}
		defer func() { result <- outcome }()
		outcome.res, outcome.err = sa.Run(t.Context(), SessionAgentCall{
			Agent: resolvedAgent{
				ID:        config.AgentCoder,
				Model:     Model{Model: model, CatwalkCfg: catwalkCfg},
				Tools:     []fantasy.AgentTool{gated},
				MaxTokens: catwalkCfg.DefaultMaxTokens,
			},
			Compact:   compact,
			SessionID: sess.ID,
			RunID:     "run-1",
			Prompt:    "wrap it up",
		})
	}()

	svc.Deny((<-events).Payload)

	var out runOutcome
	select {
	case out = <-result:
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for sa.Run to finish")
	}
	require.NoError(t, out.err)
	require.NotNil(t, out.res)

	require.Empty(t, done, "a denied merge must not reach the parent")
	require.True(t, c.branches.Waiting(sess.ID), "the branch must stay alive so the user can have it try again")

	updated, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.NotEmpty(t, updated.SummaryMessageID,
		"a denied merge leaves the branch alive, so it must still be compacted like any other ongoing turn")

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)

	var userPrompts []string
	for _, m := range msgs {
		if m.Role == message.User {
			userPrompts = append(userPrompts, m.Content().Text)
		}
	}
	require.Len(t, userPrompts, 1,
		"a denied merge must not be treated as a turn cut short mid-tool-use and queued for resume")
}
