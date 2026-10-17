package chat

import (
	"encoding/json"
	"testing"

	agenttools "github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

const testBackgroundAgentID = "agt_0123abcd"

func launchReceipt(t *testing.T) *message.ToolResult {
	t.Helper()
	meta, err := json.Marshal(agenttools.AgentResponseMetadata{Background: true, AgentID: testBackgroundAgentID})
	require.NoError(t, err)
	return &message.ToolResult{
		ToolCallID: "agent-1",
		Content:    agenttools.BackgroundAgentLaunchReceipt(testBackgroundAgentID),
		Metadata:   string(meta),
	}
}

func newBackgroundAgentItem(t *testing.T, canceled bool) *AgentToolMessageItem {
	t.Helper()
	sty := styles.CharmtonePantera()
	return NewAgentToolMessageItem(&sty, message.ToolCall{
		ID:       "agent-1",
		Name:     toolnames.Agent,
		Input:    `{"subagent_type":"explore","description":"map the repo","run_in_background":true}`,
		Finished: true,
	}, launchReceipt(t), canceled)
}

func TestAgentItemReadsTheLaunchReceipt(t *testing.T) {
	t.Parallel()

	item := newBackgroundAgentItem(t, false)

	require.True(t, item.IsBackground())
	require.Equal(t, testBackgroundAgentID, item.AgentID())
	require.True(t, item.BackgroundRunning())
	require.Empty(t, item.BackgroundOutcome())
}

// The receipt arrives after the call was created, so a result set later
// must mark the item just as one passed to the constructor does.
func TestAgentItemReadsALaterLaunchReceipt(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	item := NewAgentToolMessageItem(&sty, message.ToolCall{
		ID: "agent-1", Name: toolnames.Agent, Input: `{}`, Finished: true,
	}, nil, false)
	require.False(t, item.IsBackground())

	item.SetResult(launchReceipt(t))

	require.True(t, item.IsBackground())
	require.Equal(t, testBackgroundAgentID, item.AgentID())
}

func TestAgentItemWithoutAReceiptIsNotBackground(t *testing.T) {
	t.Parallel()

	item := newAgentItem(t, true)
	item.SetBackgroundOutcome(agenttools.BackgroundAgentCompleted)

	require.False(t, item.IsBackground())
	require.False(t, item.BackgroundRunning())
	require.Empty(t, item.BackgroundOutcome(), "an ordinary call has no outcome to record")
}

// The call has its result at once, yet the agent is still working: the
// spinner and the status must say so until the outcome arrives.
func TestBackgroundAgentKeepsSpinningPastItsLaunchResult(t *testing.T) {
	t.Parallel()

	item := newBackgroundAgentItem(t, false)

	require.True(t, item.isSpinning())
	require.Equal(t, ToolStatusRunning, item.Status())
	require.False(t, item.Finished(), "a running agent must not be frozen in the list")
}

func TestBackgroundAgentOutcomeSettlesStatusAndSpinner(t *testing.T) {
	t.Parallel()

	tests := []struct {
		outcome agenttools.BackgroundAgentStatus
		want    ToolStatus
	}{
		{agenttools.BackgroundAgentCompleted, ToolStatusSuccess},
		{agenttools.BackgroundAgentFailed, ToolStatusError},
		{agenttools.BackgroundAgentStopped, ToolStatusCanceled},
	}
	for _, tt := range tests {
		t.Run(string(tt.outcome), func(t *testing.T) {
			t.Parallel()
			item := newBackgroundAgentItem(t, false)

			item.SetBackgroundOutcome(tt.outcome)

			require.Equal(t, tt.outcome, item.BackgroundOutcome())
			require.False(t, item.BackgroundRunning())
			require.False(t, item.isSpinning())
			require.Equal(t, tt.want, item.Status())
			require.True(t, item.Finished())
		})
	}
}

// Cancelling the parent's turn marks the call canceled but leaves the agent
// running, so the mark must not stop the spinner or change the status.
func TestParentTurnCancelDoesNotStopABackgroundAgentSpinner(t *testing.T) {
	t.Parallel()

	item := newBackgroundAgentItem(t, true)

	require.True(t, item.isSpinning())
	require.Equal(t, ToolStatusRunning, item.Status())
}

// A guess made while loading a transcript gives way to the notification.
func TestLaterBackgroundOutcomeReplacesAnEarlierGuess(t *testing.T) {
	t.Parallel()
	item := newBackgroundAgentItem(t, false)

	item.SetBackgroundOutcome(agenttools.BackgroundAgentFailed)
	item.SetBackgroundOutcome(agenttools.BackgroundAgentCompleted)

	require.Equal(t, agenttools.BackgroundAgentCompleted, item.BackgroundOutcome())
	require.Equal(t, ToolStatusSuccess, item.Status())
}

func TestSettingTheSameBackgroundOutcomeDoesNotBumpTheVersion(t *testing.T) {
	t.Parallel()
	item := newBackgroundAgentItem(t, false)
	item.SetBackgroundOutcome(agenttools.BackgroundAgentCompleted)

	before := item.Version()
	item.SetBackgroundOutcome(agenttools.BackgroundAgentCompleted)

	require.Equal(t, before, item.Version())
}

func TestSettingABackgroundOutcomeRedrawsTheItem(t *testing.T) {
	t.Parallel()
	item := newBackgroundAgentItem(t, false)

	before := item.Version()
	item.SetBackgroundOutcome(agenttools.BackgroundAgentFailed)

	require.NotEqual(t, before, item.Version())
}

func TestBackgroundAgentHeaderIsTaggedAndNamesABadEnding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		outcome agenttools.BackgroundAgentStatus
		want    string
		notWant string
	}{
		{"running", "", "· background", "stopped"},
		{"completed", agenttools.BackgroundAgentCompleted, "· background", "completed"},
		{"failed", agenttools.BackgroundAgentFailed, "· background failed", ""},
		{"stopped", agenttools.BackgroundAgentStopped, "· background stopped", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			item := newBackgroundAgentItem(t, false)
			if tt.outcome != "" {
				item.SetBackgroundOutcome(tt.outcome)
			}

			out := ansi.Strip(item.Render(100))

			require.Contains(t, out, tt.want)
			if tt.notWant != "" {
				require.NotContains(t, out, tt.notWant)
			}
		})
	}
}

func TestOrdinaryAgentHeaderCarriesNoBackgroundTag(t *testing.T) {
	t.Parallel()

	item := newAgentItem(t, true)

	require.NotContains(t, ansi.Strip(item.Render(100)), "background")
}
