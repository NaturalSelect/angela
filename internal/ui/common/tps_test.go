package common

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/stretchr/testify/require"
)

func TestStepTPS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		msg     *message.Message
		wantTPS int64
		wantOK  bool
	}{
		{
			name: "qualifies: no tool calls, output tokens, duration >= 1s",
			msg: &message.Message{
				CreatedAt: 1000,
				Parts: []message.ContentPart{
					message.Finish{Time: 1005, OutputTokens: 100},
				},
			},
			wantTPS: 20,
			wantOK:  true,
		},
		{
			name: "excluded: has a tool call",
			msg: &message.Message{
				CreatedAt: 1000,
				Parts: []message.ContentPart{
					message.ToolCall{ID: "tc1"},
					message.Finish{Time: 1005, OutputTokens: 100},
				},
			},
			wantOK: false,
		},
		{
			name: "excluded: no output tokens",
			msg: &message.Message{
				CreatedAt: 1000,
				Parts: []message.ContentPart{
					message.Finish{Time: 1005, OutputTokens: 0},
				},
			},
			wantOK: false,
		},
		{
			name: "excluded: duration under a second",
			msg: &message.Message{
				CreatedAt: 1000,
				Parts: []message.ContentPart{
					message.Finish{Time: 1000, OutputTokens: 5},
				},
			},
			wantOK: false,
		},
		{
			name: "excluded: no finish part yet",
			msg: &message.Message{
				CreatedAt: 1000,
				Parts:     []message.ContentPart{message.TextContent{Text: "partial"}},
			},
			wantOK: false,
		},
		{
			name:   "excluded: nil message",
			msg:    nil,
			wantOK: false,
		},
		{
			name: "rounds to the nearest token/sec",
			msg: &message.Message{
				CreatedAt: 1000,
				Parts: []message.ContentPart{
					message.Finish{Time: 1003, OutputTokens: 10}, // 3.33.. -> 3
				},
			},
			wantTPS: 3,
			wantOK:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tps, ok := StepTPS(tt.msg)
			require.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				require.Equal(t, tt.wantTPS, tps)
			}
		})
	}
}

func TestStepTPSRate(t *testing.T) {
	t.Parallel()

	msg := &message.Message{
		CreatedAt: 1000,
		Parts: []message.ContentPart{
			message.Finish{Time: 1003, OutputTokens: 10},
		},
	}
	rate, ok := StepTPSRate(msg)
	require.True(t, ok)
	require.InDelta(t, 10.0/3.0, rate, 0.0001)

	_, ok = StepTPSRate(nil)
	require.False(t, ok)

	_, ok = StepTPSRate(&message.Message{
		CreatedAt: 1000,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "tc1"},
			message.Finish{Time: 1010, OutputTokens: 500},
		},
	})
	require.False(t, ok, "a step with any tool call must never report a rate")
}
