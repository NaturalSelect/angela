package proto_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestAgentEventTypeTextMarshaling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		typ  proto.AgentEventType
	}{
		{"error", proto.AgentEventTypeError},
		{"response", proto.AgentEventTypeResponse},
		{"summarize", proto.AgentEventTypeSummarize},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			text, err := tc.typ.MarshalText()
			require.NoError(t, err)
			require.Equal(t, string(tc.typ), string(text))

			var got proto.AgentEventType
			require.NoError(t, got.UnmarshalText(text))
			require.Equal(t, tc.typ, got)
		})
	}
}

func TestAgentEventMarshalUnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		evt  proto.AgentEvent
	}{
		{
			name: "error event",
			evt: proto.AgentEvent{
				Type: proto.AgentEventTypeError,
				Message: proto.Message{
					ID:   "msg-1",
					Role: proto.Assistant,
					Parts: []proto.ContentPart{
						proto.TextContent{Text: "partial output"},
					},
				},
				Error:        errors.New("stream failed"),
				RunID:        "run-1",
				AWSSOCommand: "aws sso login",
				AWSSOURL:     "https://example.com/sso",
			},
		},
		{
			name: "summarize event without error",
			evt: proto.AgentEvent{
				Type:         proto.AgentEventTypeSummarize,
				Message:      proto.Message{Parts: []proto.ContentPart{}},
				SessionID:    "sess-1",
				SessionTitle: "New title",
				Progress:     "50%",
				Done:         true,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tc.evt)
			require.NoError(t, err)

			var got proto.AgentEvent
			require.NoError(t, json.Unmarshal(data, &got))

			if tc.evt.Error != nil {
				require.EqualError(t, got.Error, tc.evt.Error.Error())
			} else {
				require.Nil(t, got.Error)
			}
			got.Error = nil
			want := tc.evt
			want.Error = nil
			require.Equal(t, want, got)
		})
	}
}

func TestAgentEventUnmarshalJSONFieldTypeMismatch(t *testing.T) {
	t.Parallel()

	// Valid top-level JSON syntax so the outer json.Unmarshal actually
	// dispatches into AgentEvent.UnmarshalJSON, where the field type
	// mismatch (session_id expects a string) fails the aux decode step.
	var got proto.AgentEvent
	err := json.Unmarshal([]byte(`{"session_id":123}`), &got)
	require.Error(t, err)
}
