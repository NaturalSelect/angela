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
			name: "qualifies: output tokens, valid gen duration",
			msg: &message.Message{
				Parts: []message.ContentPart{
					message.Finish{GenDurationMs: 5000, OutputTokens: 100},
				},
			},
			wantTPS: 20,
			wantOK:  true,
		},
		{
			name: "qualifies: tool calls no longer excluded, GenDurationMs already excludes tool time",
			msg: &message.Message{
				Parts: []message.ContentPart{
					message.ToolCall{ID: "tc1"},
					message.Finish{GenDurationMs: 5000, OutputTokens: 100},
				},
			},
			wantTPS: 20,
			wantOK:  true,
		},
		{
			name: "excluded: no output tokens",
			msg: &message.Message{
				Parts: []message.ContentPart{
					message.Finish{GenDurationMs: 5000, OutputTokens: 0},
				},
			},
			wantOK: false,
		},
		{
			name: "qualifies: sub-second duration, no minimum duration floor anymore",
			msg: &message.Message{
				Parts: []message.ContentPart{
					message.Finish{GenDurationMs: 500, OutputTokens: 5},
				},
			},
			wantTPS: 10,
			wantOK:  true,
		},
		{
			name: "excluded: GenDurationMs unset (unknown duration)",
			msg: &message.Message{
				Parts: []message.ContentPart{
					message.Finish{OutputTokens: 100},
				},
			},
			wantOK: false,
		},
		{
			name: "excluded: no finish part yet",
			msg: &message.Message{
				Parts: []message.ContentPart{message.TextContent{Text: "partial"}},
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
				Parts: []message.ContentPart{
					message.Finish{GenDurationMs: 3000, OutputTokens: 10}, // 3.33.. -> 3
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
		Parts: []message.ContentPart{
			message.Finish{GenDurationMs: 3000, OutputTokens: 10},
		},
	}
	rate, ok := StepTPSRate(msg)
	require.True(t, ok)
	require.InDelta(t, 10.0/3.0, rate, 0.0001)

	_, ok = StepTPSRate(nil)
	require.False(t, ok)

	rate, ok = StepTPSRate(&message.Message{
		Parts: []message.ContentPart{
			message.ToolCall{ID: "tc1"},
			message.Finish{GenDurationMs: 10000, OutputTokens: 500},
		},
	})
	require.True(t, ok, "a step with a tool call now reports a rate: GenDurationMs already excludes tool execution time")
	require.InDelta(t, 50.0, rate, 0.0001)
}

func TestAverageTPS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		tokens     int64
		durationMs int64
		wantTPS    int64
		wantOK     bool
	}{
		{
			name:       "qualifies: normal case",
			tokens:     1000,
			durationMs: 4000,
			wantTPS:    250,
			wantOK:     true,
		},
		{
			name:       "rounds down to the nearest token/sec",
			tokens:     10,
			durationMs: 3000, // 10 / 3 = 3.33.. -> 3
			wantTPS:    3,
			wantOK:     true,
		},
		{
			name:       "rounds up to the nearest token/sec",
			tokens:     10,
			durationMs: 2800, // 10 / 2.8 = 3.57.. -> 4
			wantTPS:    4,
			wantOK:     true,
		},
		{
			name:       "excluded: zero tokens",
			tokens:     0,
			durationMs: 4000,
			wantOK:     false,
		},
		{
			name:       "excluded: negative tokens",
			tokens:     -5,
			durationMs: 4000,
			wantOK:     false,
		},
		{
			name:       "excluded: zero duration",
			tokens:     1000,
			durationMs: 0,
			wantOK:     false,
		},
		{
			name:       "excluded: negative duration",
			tokens:     1000,
			durationMs: -1,
			wantOK:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tps, ok := AverageTPS(tt.tokens, tt.durationMs)
			require.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				require.Equal(t, tt.wantTPS, tps)
			}
		})
	}
}
