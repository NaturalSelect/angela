package message

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeContentPart is a ContentPart implementation that marshalParts does
// not know how to encode, used to exercise its "unknown part type" branch.
type fakeContentPart struct{}

func (fakeContentPart) isPart() {}

func TestMarshalParts_RoundTripsEveryConcreteType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		part ContentPart
	}{
		{name: "reasoning", part: ReasoningContent{Thinking: "hmm", Signature: "sig"}},
		{name: "text", part: TextContent{Text: "hello"}},
		{name: "image url", part: ImageURLContent{URL: "https://x/1.png", Detail: "high"}},
		{name: "binary", part: BinaryContent{Path: "a.bin", MIMEType: "application/octet-stream", Data: []byte{1, 2, 3}}},
		{name: "tool call", part: ToolCall{ID: "tc1", Name: "bash", Input: "{}", Finished: true}},
		{name: "tool result", part: ToolResult{ToolCallID: "tc1", Name: "bash", Content: "ok"}},
		{name: "finish", part: Finish{Reason: FinishReasonEndTurn, Time: 123, Message: "done"}},
		{name: "shell command", part: ShellCommand{Command: "ls", Output: "a.txt", ExitCode: 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := marshalParts([]ContentPart{tt.part})
			require.NoError(t, err)

			got, err := unmarshalParts(data)
			require.NoError(t, err)
			require.Len(t, got, 1)
			require.Equal(t, tt.part, got[0])
		})
	}
}

func TestMarshalParts_MultiplePartsPreserveOrder(t *testing.T) {
	t.Parallel()

	parts := []ContentPart{
		TextContent{Text: "a"},
		ToolCall{ID: "tc1"},
		Finish{Reason: FinishReasonEndTurn},
	}

	data, err := marshalParts(parts)
	require.NoError(t, err)

	got, err := unmarshalParts(data)
	require.NoError(t, err)
	require.Equal(t, parts, got)
}

func TestMarshalParts_EmptySlice(t *testing.T) {
	t.Parallel()

	data, err := marshalParts(nil)
	require.NoError(t, err)

	got, err := unmarshalParts(data)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestMarshalParts_UnknownTypeErrors(t *testing.T) {
	t.Parallel()

	_, err := marshalParts([]ContentPart{fakeContentPart{}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown part type")
}

func TestUnmarshalParts_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
	}{
		{name: "invalid top-level JSON", data: []byte("not json")},
		{name: "top-level element is not a wrapper object", data: []byte(`[123]`)},
		{name: "unknown wrapper type", data: []byte(`[{"type":"bogus","data":{}}]`)},
		{name: "reasoning data malformed", data: []byte(`[{"type":"reasoning","data":123}]`)},
		{name: "text data malformed", data: []byte(`[{"type":"text","data":123}]`)},
		{name: "image_url data malformed", data: []byte(`[{"type":"image_url","data":123}]`)},
		{name: "binary data malformed", data: []byte(`[{"type":"binary","data":123}]`)},
		{name: "tool_call data malformed", data: []byte(`[{"type":"tool_call","data":123}]`)},
		{name: "tool_result data malformed", data: []byte(`[{"type":"tool_result","data":123}]`)},
		{name: "finish data malformed", data: []byte(`[{"type":"finish","data":123}]`)},
		{name: "shell_command data malformed", data: []byte(`[{"type":"shell_command","data":123}]`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := unmarshalParts(tt.data)
			require.Error(t, err)
		})
	}
}

func TestUnmarshalParts_EmptyArray(t *testing.T) {
	t.Parallel()

	got, err := unmarshalParts([]byte(`[]`))
	require.NoError(t, err)
	require.Empty(t, got)
}
