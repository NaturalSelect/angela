package chat

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

func TestAgentToolItemSubagentNameAndTask(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		toolCall message.ToolCall
		wantName string
		wantTask string
	}{
		{
			name: "explicit type and description",
			toolCall: message.ToolCall{
				Input:    `{"subagent_type":"plan","description":"design the API","prompt":"ignored"}`,
				Finished: true,
			},
			wantName: "plan",
			wantTask: "design the API",
		},
		{
			name: "task falls back to first prompt line",
			toolCall: message.ToolCall{
				Input:    `{"subagent_type":"explore","prompt":"find the login flow\nthen report"}`,
				Finished: true,
			},
			wantName: "explore",
			wantTask: "find the login flow",
		},
		{
			name: "omitted type defaults to explore once finished",
			toolCall: message.ToolCall{
				Input:    `{"description":"look around"}`,
				Finished: true,
			},
			wantName: config.AgentExplore,
			wantTask: "look around",
		},
		{
			name: "omitted type stays unknown while streaming",
			toolCall: message.ToolCall{
				Input: `{"description":"look around"}`,
			},
			wantName: "",
			wantTask: "look around",
		},
		{
			name:     "unparsable input yields neither",
			toolCall: message.ToolCall{Input: `{"subagent_ty`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sty := styles.CharmtonePantera()

			tt.toolCall.Name = toolnames.Agent
			item := NewAgentToolMessageItem(&sty, tt.toolCall, nil, false)

			require.Equal(t, tt.wantName, item.SubagentName())
			require.Equal(t, tt.wantTask, item.Task())
		})
	}
}

// The header and the accessor must agree on the name, since both describe
// the same sub-agent to the user.
func TestAgentToolTitleUsesSubagentName(t *testing.T) {
	t.Parallel()

	finished := message.ToolCall{Input: `{}`, Finished: true}
	require.Equal(t, toolnames.Agent+"("+config.AgentExplore+")", agentToolTitle(finished))

	streaming := message.ToolCall{Input: `{}`}
	require.Equal(t, toolnames.Agent, agentToolTitle(streaming))
}
