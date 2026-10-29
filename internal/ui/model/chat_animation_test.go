package model

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	agenttools "github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/anim"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/stretchr/testify/require"
)

func animItem(id string) *testAnimatableItem {
	return &testAnimatableItem{testMessageItem: testMessageItem{id: id, text: id}}
}

func newAnimUI(items ...chat.MessageItem) *UI {
	u := newTestUI()
	u.chat.SetMessages(items...)
	u.updateLayoutAndSize()
	return u
}

// offscreenAnimUI lays out a leader at the top of a list too long for the
// viewport, so the leader is scrolled out of view while the tail is visible.
func offscreenAnimUI(leader chat.MessageItem, tail ...chat.MessageItem) *UI {
	u := newTestUI()
	items := []chat.MessageItem{leader}
	for i := range 20 {
		items = append(items, testMessageItem{id: "f" + strconv.Itoa(i), text: "filler"})
	}
	items = append(items, tail...)
	u.chat.SetMessages(items...)
	u.chat.SetSize(80, 5)
	return u
}

func TestChatAnimateSharesOneClock(t *testing.T) {
	t.Parallel()
	a, b, c := animItem("a"), animItem("b"), animItem("c")
	u := newAnimUI(a, b, c)

	require.NotNil(t, u.chat.Animate(anim.StepMsg{ID: "a"}), "the first chain to tick keeps ticking")
	require.Equal(t, "a", u.chat.animLeader)
	require.Nil(t, u.chat.Animate(anim.StepMsg{ID: "b"}), "a second chain must end")
	require.Nil(t, u.chat.Animate(anim.StepMsg{ID: "c"}), "a third chain must end")
	require.Equal(t, 0, b.animateCalls, "a follower is stepped by the leader, not by its own tick")

	require.NotNil(t, u.chat.Animate(anim.StepMsg{ID: "a"}))
	require.Equal(t, 1, b.advanceCalls)
	require.Equal(t, 1, c.advanceCalls)
	require.NotNil(t, u.chat.Animate(anim.StepMsg{ID: "a"}))
	require.Equal(t, 2, b.advanceCalls)
	require.Equal(t, 2, c.advanceCalls)
	require.Equal(t, 0, a.advanceCalls, "the leader advances itself through Animate")
}

func TestChatAnimatePassesTheClockWhenTheLeaderFinishes(t *testing.T) {
	t.Parallel()
	a, b := animItem("a"), animItem("b")
	u := newAnimUI(a, b)
	require.NotNil(t, u.chat.Animate(anim.StepMsg{ID: "a"}))
	require.Nil(t, u.chat.Animate(anim.StepMsg{ID: "b"}))

	a.done = true
	cmd := u.chat.Animate(anim.StepMsg{ID: "a"})

	require.NotNil(t, cmd, "the follower must take over the tick chain")
	require.Equal(t, 1, b.startAnimationCalls)
	require.Equal(t, "b", u.chat.animLeader)
	require.Empty(t, u.chat.animFollowers)
}

func TestChatAnimateStopsTheClockWhenNothingAnimates(t *testing.T) {
	t.Parallel()
	a, b := animItem("a"), animItem("b")
	u := newAnimUI(a, b)
	require.NotNil(t, u.chat.Animate(anim.StepMsg{ID: "a"}))
	require.Nil(t, u.chat.Animate(anim.StepMsg{ID: "b"}))

	a.done, b.done = true, true

	require.Nil(t, u.chat.Animate(anim.StepMsg{ID: "a"}))
	require.Empty(t, u.chat.animLeader)
	require.Empty(t, u.chat.animFollowers)
	require.Equal(t, 0, b.startAnimationCalls)
}

func TestChatAnimateIgnoresATickFromASupersededChain(t *testing.T) {
	t.Parallel()
	a, b := animItem("a"), animItem("b")
	u := newAnimUI(a, b)
	require.NotNil(t, u.chat.Animate(anim.StepMsg{ID: "a"}))
	require.Nil(t, u.chat.Animate(anim.StepMsg{ID: "b"}))
	advanced := b.advanceCalls

	a.stale = true
	cmd := u.chat.Animate(anim.StepMsg{ID: "a"})

	require.Nil(t, cmd)
	require.Equal(t, "a", u.chat.animLeader, "a stale tick must not give up the clock")
	require.Equal(t, advanced, b.advanceCalls, "a stale tick must not step the followers")
	require.Equal(t, 0, b.startAnimationCalls)
}

func TestChatAnimateStaleTickDoesNotClaimAnIdleClock(t *testing.T) {
	t.Parallel()
	a := animItem("a")
	a.stale = true
	u := newAnimUI(a)

	require.Nil(t, u.chat.Animate(anim.StepMsg{ID: "a"}))
	require.Empty(t, u.chat.animLeader)
}

func TestChatAnimateTakesOverFromAQuietLeader(t *testing.T) {
	t.Parallel()
	a, b := animItem("a"), animItem("b")
	u := newAnimUI(a, b)
	require.NotNil(t, u.chat.Animate(anim.StepMsg{ID: "a"}))
	u.chat.animLeaderAt = time.Now().Add(-time.Hour)

	require.NotNil(t, u.chat.Animate(anim.StepMsg{ID: "b"}), "a leader that stopped ticking must not strand the others")
	require.Equal(t, "b", u.chat.animLeader)
}

func TestChatAnimateDropsFollowersThatStoppedAnimating(t *testing.T) {
	t.Parallel()
	a, b := animItem("a"), animItem("b")
	u := newAnimUI(a, b)
	require.NotNil(t, u.chat.Animate(anim.StepMsg{ID: "a"}))
	require.Nil(t, u.chat.Animate(anim.StepMsg{ID: "b"}))

	b.done = true
	require.NotNil(t, u.chat.Animate(anim.StepMsg{ID: "a"}))

	require.Equal(t, 0, b.advanceCalls)
	require.Empty(t, u.chat.animFollowers)
}

func TestChatAnimatePassesTheClockFromAnOffscreenLeader(t *testing.T) {
	t.Parallel()
	leader, follower := animItem("leader"), animItem("follower")
	u := offscreenAnimUI(leader, follower)
	u.chat.animFollowers["follower"] = struct{}{}

	cmd := u.chat.Animate(anim.StepMsg{ID: "leader"})

	require.NotNil(t, cmd, "a visible follower must take over")
	require.Equal(t, 0, leader.animateCalls)
	require.Equal(t, 1, follower.startAnimationCalls)
	require.Equal(t, "follower", u.chat.animLeader)
	require.Contains(t, u.chat.pausedAnimations, "leader")
}

func TestChatAnimateStopsWhenNoAnimatingItemIsVisible(t *testing.T) {
	t.Parallel()
	leader, hidden := animItem("leader"), animItem("hidden")
	u := newTestUI()
	items := []chat.MessageItem{leader, hidden}
	for i := range 20 {
		items = append(items, testMessageItem{id: "f" + strconv.Itoa(i), text: "filler"})
	}
	u.chat.SetMessages(items...)
	u.chat.SetSize(80, 5)
	u.chat.animFollowers["hidden"] = struct{}{}

	require.Nil(t, u.chat.Animate(anim.StepMsg{ID: "leader"}))

	require.Empty(t, u.chat.animLeader)
	require.Empty(t, u.chat.animFollowers)
	require.Contains(t, u.chat.pausedAnimations, "leader")
	require.Contains(t, u.chat.pausedAnimations, "hidden", "an item the clock dropped must restart when it scrolls back")
}

func TestChatAnimatePassesTheClockWhenTheLeaderIsRemoved(t *testing.T) {
	t.Parallel()
	a, b := animItem("a"), animItem("b")
	u := newAnimUI(a, b)
	require.NotNil(t, u.chat.Animate(anim.StepMsg{ID: "a"}))
	require.Nil(t, u.chat.Animate(anim.StepMsg{ID: "b"}))

	u.chat.RemoveMessage("a")
	cmd := u.chat.Animate(anim.StepMsg{ID: "a"})

	require.NotNil(t, cmd)
	require.Equal(t, "b", u.chat.animLeader)
}

func TestChatReplacingMessagesResetsTheClock(t *testing.T) {
	t.Parallel()
	a, b := animItem("a"), animItem("b")
	u := newAnimUI(a, b)
	require.NotNil(t, u.chat.Animate(anim.StepMsg{ID: "a"}))
	require.Nil(t, u.chat.Animate(anim.StepMsg{ID: "b"}))

	u.chat.SetMessages(animItem("c"))
	require.Empty(t, u.chat.animLeader)
	require.Empty(t, u.chat.animFollowers)

	require.NotNil(t, u.chat.Animate(anim.StepMsg{ID: "c"}))
	u.chat.ClearMessages()
	require.Empty(t, u.chat.animLeader)
	require.Empty(t, u.chat.animFollowers)
}

func runningBackgroundAgent(m *UI, i int) *chat.AgentToolMessageItem {
	callID := fmt.Sprintf("call-%d", i)
	item := dispatchedAgent(m, fmt.Sprintf("msg-%d", i), callID,
		`{"subagent_type":"explore","description":"map the repo"}`, true, false)
	meta, _ := json.Marshal(agenttools.AgentResponseMetadata{Background: true, AgentID: fmt.Sprintf("agt_%d", i)})
	item.SetResult(&message.ToolResult{ToolCallID: callID, Content: "launched", Metadata: string(meta)})
	return item
}

// Every running background agent starts its own tick chain, and each tick
// costs a full frame. They must collapse into a single chain.
func TestBackgroundAgentsShareOneAnimationChain(t *testing.T) {
	pinTTLs(t)
	m := drawableUI(t, 140, 45)
	warmCaches(m, false)

	const n = 4
	var agents []*chat.AgentToolMessageItem
	var items []chat.MessageItem
	for i := range n {
		agent := runningBackgroundAgent(m, i)
		agents = append(agents, agent)
		items = append(items, agent)
	}
	m.chat.SetMessages(items...)
	m.updateLayoutAndSize()

	ticks := make([]anim.StepMsg, n)
	for i, agent := range agents {
		cmd := agent.StartAnimation()
		require.NotNil(t, cmd)
		step, ok := cmd().(anim.StepMsg)
		require.True(t, ok)
		ticks[i] = step
	}

	live := 0
	for _, step := range ticks {
		if m.chat.Animate(step) != nil {
			live++
		}
	}
	require.Equal(t, 1, live, "only one tick chain may survive the first round")
	require.Equal(t, agents[0].ID(), m.chat.animLeader)

	before := make([]uint64, n)
	for i, agent := range agents {
		before[i] = agent.Version()
	}
	require.NotNil(t, m.chat.Animate(ticks[0]))
	for i, agent := range agents {
		require.Greater(t, agent.Version(), before[i], "agent %d must have advanced its frame", i)
	}

	for _, agent := range agents {
		agent.SetBackgroundOutcome(agenttools.BackgroundAgentCompleted)
	}
	require.Nil(t, m.chat.Animate(ticks[0]), "the chain must end once no agent runs")
	require.Empty(t, m.chat.animLeader)
	require.Empty(t, m.chat.animFollowers)
}
