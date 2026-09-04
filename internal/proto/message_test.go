package proto_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestMessageRoleTextMarshaling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		role proto.MessageRole
	}{
		{"assistant", proto.Assistant},
		{"user", proto.User},
		{"system", proto.System},
		{"tool", proto.Tool},
		{"custom", proto.MessageRole("custom-role")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			text, err := tc.role.MarshalText()
			require.NoError(t, err)
			require.Equal(t, string(tc.role), string(text))

			var got proto.MessageRole
			require.NoError(t, got.UnmarshalText(text))
			require.Equal(t, tc.role, got)

			// MessageRole is used as a struct field on the wire, so it goes
			// through the encoding/json TextMarshaler path, not direct calls.
			type wrapper struct {
				Role proto.MessageRole `json:"role"`
			}
			data, err := json.Marshal(wrapper{Role: tc.role})
			require.NoError(t, err)
			var gotWrapper wrapper
			require.NoError(t, json.Unmarshal(data, &gotWrapper))
			require.Equal(t, tc.role, gotWrapper.Role)
		})
	}
}

func TestFinishReasonTextMarshaling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		reason proto.FinishReason
	}{
		{"end turn", proto.FinishReasonEndTurn},
		{"max tokens", proto.FinishReasonMaxTokens},
		{"tool use", proto.FinishReasonToolUse},
		{"canceled", proto.FinishReasonCanceled},
		{"error", proto.FinishReasonError},
		{"content filter", proto.FinishReasonContentFilter},
		{"unknown", proto.FinishReasonUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			text, err := tc.reason.MarshalText()
			require.NoError(t, err)
			require.Equal(t, string(tc.reason), string(text))

			var got proto.FinishReason
			require.NoError(t, got.UnmarshalText(text))
			require.Equal(t, tc.reason, got)
		})
	}
}

func TestContentPartStringers(t *testing.T) {
	t.Parallel()

	t.Run("reasoning", func(t *testing.T) {
		t.Parallel()
		c := proto.ReasoningContent{Thinking: "pondering"}
		require.Equal(t, "pondering", c.String())
	})
	t.Run("text", func(t *testing.T) {
		t.Parallel()
		c := proto.TextContent{Text: "hello"}
		require.Equal(t, "hello", c.String())
	})
	t.Run("image url", func(t *testing.T) {
		t.Parallel()
		c := proto.ImageURLContent{URL: "https://x/y.png"}
		require.Equal(t, "https://x/y.png", c.String())
	})
}

func TestBinaryContentString(t *testing.T) {
	t.Parallel()

	data := []byte("binary-data")
	encoded := base64.StdEncoding.EncodeToString(data)

	tests := []struct {
		name     string
		provider catwalk.InferenceProvider
		want     string
	}{
		{"openai gets a data uri prefix", catwalk.InferenceProviderOpenAI, "data:image/png;base64," + encoded},
		{"non-openai gets raw base64", catwalk.InferenceProviderAnthropic, encoded},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := proto.BinaryContent{MIMEType: "image/png", Data: data}
			require.Equal(t, tc.want, c.String(tc.provider))
		})
	}
}

// TestMarshalUnmarshalPartsRoundTrip is the primary coverage target for the
// polymorphic ContentPart union codec: build one of every concrete part
// type, marshal, unmarshal, and check the decoded slice is exactly equal
// (same concrete types and field values) to the original.
func TestMarshalUnmarshalPartsRoundTrip(t *testing.T) {
	t.Parallel()

	parts := []proto.ContentPart{
		proto.ReasoningContent{Thinking: "step 1", Signature: "sig-xyz", StartedAt: 1000, FinishedAt: 2000},
		proto.TextContent{Text: "hello world"},
		proto.ImageURLContent{URL: "https://example.com/a.png", Detail: "high"},
		proto.BinaryContent{Path: "/tmp/a.bin", MIMEType: "application/octet-stream", Data: []byte{0x01, 0x02, 0xFF}},
		proto.ToolCall{ID: "call-1", Name: "bash", Input: `{"command":"ls"}`, Type: "function", Finished: true},
		proto.ToolResult{ToolCallID: "call-1", Name: "bash", Content: "file1\nfile2", Data: "extra", MIMEType: "text/plain", Metadata: `{"exit_code":0}`, IsError: false},
		proto.Finish{Reason: proto.FinishReasonToolUse, Time: 5000, Message: "done", Details: "all good"},
		proto.ShellCommand{Command: "ls -la", Output: "total 0", ExitCode: 0},
	}

	data, err := proto.MarshalParts(parts)
	require.NoError(t, err)

	got, err := proto.UnmarshalParts(data)
	require.NoError(t, err)
	require.Equal(t, parts, got)
}

func TestMarshalPartsEmpty(t *testing.T) {
	t.Parallel()

	data, err := proto.MarshalParts(nil)
	require.NoError(t, err)
	require.JSONEq(t, `[]`, string(data))

	got, err := proto.UnmarshalParts(data)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestMarshalPartsUnknownType(t *testing.T) {
	t.Parallel()

	// A nil ContentPart has no dynamic type, so it can never match one of
	// MarshalParts' concrete type-switch cases.
	_, err := proto.MarshalParts([]proto.ContentPart{nil})
	require.Error(t, err)
	require.ErrorContains(t, err, "unknown part type")
}

func TestUnmarshalPartsMalformed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		data          string
		wantErrSubstr string
	}{
		{"not json at all", `not json`, ""},
		{"json object instead of array", `{"type":"text"}`, ""},
		{"unknown type discriminator", `[{"type":"bogus","data":{}}]`, "bogus"},
		{"part envelope is not an object", `[123]`, ""},
		// Each concrete part type decodes its "data" payload independently,
		// so a type mismatch there must surface as an error for every
		// branch, not just one.
		{"text data does not match the declared type", `[{"type":"text","data":123}]`, ""},
		{"reasoning data does not match the declared type", `[{"type":"reasoning","data":123}]`, ""},
		{"image_url data does not match the declared type", `[{"type":"image_url","data":123}]`, ""},
		{"binary data does not match the declared type", `[{"type":"binary","data":123}]`, ""},
		{"tool_call data does not match the declared type", `[{"type":"tool_call","data":123}]`, ""},
		{"tool_result data does not match the declared type", `[{"type":"tool_result","data":123}]`, ""},
		{"finish data does not match the declared type", `[{"type":"finish","data":123}]`, ""},
		{"shell_command data does not match the declared type", `[{"type":"shell_command","data":123}]`, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := proto.UnmarshalParts([]byte(tc.data))
			require.Error(t, err)
			require.Nil(t, got)
			if tc.wantErrSubstr != "" {
				require.ErrorContains(t, err, tc.wantErrSubstr)
			}
		})
	}
}

func TestMessageMarshalUnmarshalJSON(t *testing.T) {
	t.Parallel()

	msg := proto.Message{
		ID:        "msg-1",
		Role:      proto.Assistant,
		SessionID: "sess-1",
		Parts: []proto.ContentPart{
			proto.TextContent{Text: "hi there"},
			proto.ToolCall{ID: "call-1", Name: "bash", Input: `{"command":"ls"}`},
		},
		Model:            "gpt-4o",
		Provider:         "openai",
		CreatedAt:        1000,
		UpdatedAt:        2000,
		IsSummaryMessage: true,
	}

	data, err := json.Marshal(msg)
	require.NoError(t, err)

	var got proto.Message
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, msg, got)
}

func TestMessageUnmarshalJSONMalformed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
	}{
		{"invalid json syntax", `not json`},
		// Valid top-level JSON syntax so the outer json.Unmarshal actually
		// dispatches into Message.UnmarshalJSON, where the field type
		// mismatch (id expects a string) fails the aux decode step.
		{"field type mismatch outside parts", `{"id":123,"parts":[]}`},
		{"unknown part type", `{"id":"m1","parts":[{"type":"bogus","data":{}}]}`},
		{"parts is not an array", `{"id":"m1","parts":"not-an-array"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got proto.Message
			err := json.Unmarshal([]byte(tc.data), &got)
			require.Error(t, err)
		})
	}
}

func TestMessageMarshalJSONUnknownPartType(t *testing.T) {
	t.Parallel()

	// A nil ContentPart can never match MarshalParts' type switch, so
	// Message.MarshalJSON must surface that error instead of panicking.
	_, err := json.Marshal(proto.Message{Parts: []proto.ContentPart{nil}})
	require.Error(t, err)
	require.ErrorContains(t, err, "unknown part type")
}

func TestMessageAccessorsOnEmptyMessage(t *testing.T) {
	t.Parallel()

	m := &proto.Message{}
	require.Equal(t, proto.TextContent{}, m.Content())
	require.Equal(t, proto.ReasoningContent{}, m.ReasoningContent())
	require.Empty(t, m.ImageURLContent())
	require.Empty(t, m.BinaryContent())
	require.Empty(t, m.ToolCalls())
	require.Empty(t, m.ToolResults())
	require.False(t, m.IsFinished())
	require.Nil(t, m.FinishPart())
	require.Equal(t, proto.FinishReason(""), m.FinishReason())
	require.False(t, m.IsThinking())
}

func TestMessageAccessorsOnPopulatedMessage(t *testing.T) {
	t.Parallel()

	m := &proto.Message{
		Parts: []proto.ContentPart{
			proto.TextContent{Text: "first text"},
			proto.TextContent{Text: "second text"},
			proto.ReasoningContent{Thinking: "thinking first"},
			proto.ReasoningContent{Thinking: "thinking second"},
			proto.ImageURLContent{URL: "https://x/1.png"},
			proto.ImageURLContent{URL: "https://x/2.png"},
			proto.BinaryContent{MIMEType: "application/pdf", Data: []byte{1, 2}},
			proto.ToolCall{ID: "call-1", Name: "bash"},
			proto.ToolCall{ID: "call-2", Name: "grep"},
			proto.ToolResult{ToolCallID: "call-1", Content: "result-1"},
			proto.ToolResult{ToolCallID: "call-2", Content: "result-2"},
			proto.Finish{Reason: proto.FinishReasonEndTurn, Message: "wrapped up"},
		},
	}

	require.Equal(t, "first text", m.Content().Text, "Content returns the first text part")
	require.Equal(t, "thinking first", m.ReasoningContent().Thinking, "ReasoningContent returns the first reasoning part")
	require.Equal(t, []proto.ImageURLContent{{URL: "https://x/1.png"}, {URL: "https://x/2.png"}}, m.ImageURLContent())
	require.Equal(t, []proto.BinaryContent{{MIMEType: "application/pdf", Data: []byte{1, 2}}}, m.BinaryContent())
	require.Equal(t, []proto.ToolCall{{ID: "call-1", Name: "bash"}, {ID: "call-2", Name: "grep"}}, m.ToolCalls())
	require.Equal(t, []proto.ToolResult{{ToolCallID: "call-1", Content: "result-1"}, {ToolCallID: "call-2", Content: "result-2"}}, m.ToolResults())
	require.True(t, m.IsFinished())
	require.Equal(t, &proto.Finish{Reason: proto.FinishReasonEndTurn, Message: "wrapped up"}, m.FinishPart())
	require.Equal(t, proto.FinishReasonEndTurn, m.FinishReason())
	require.False(t, m.IsThinking())
}

func TestMessageIsThinking(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		parts []proto.ContentPart
		want  bool
	}{
		{"no parts", nil, false},
		{"reasoning only", []proto.ContentPart{proto.ReasoningContent{Thinking: "hmm"}}, true},
		{"reasoning and text", []proto.ContentPart{proto.ReasoningContent{Thinking: "hmm"}, proto.TextContent{Text: "answer"}}, false},
		{"reasoning and finished", []proto.ContentPart{proto.ReasoningContent{Thinking: "hmm"}, proto.Finish{Reason: proto.FinishReasonEndTurn}}, false},
		{"empty reasoning", []proto.ContentPart{proto.ReasoningContent{}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := &proto.Message{Parts: tc.parts}
			require.Equal(t, tc.want, m.IsThinking())
		})
	}
}

func TestMessageAppendContent(t *testing.T) {
	t.Parallel()

	m := &proto.Message{}
	m.AppendContent("Hello, ")
	m.AppendContent("world!")

	require.Equal(t, "Hello, world!", m.Content().Text)
	require.Len(t, m.Parts, 1, "delta appends merge into the single text part")
}

func TestMessageReasoningMutators(t *testing.T) {
	t.Parallel()

	m := &proto.Message{}
	m.AppendReasoningContent("step one")
	require.Equal(t, "step one", m.ReasoningContent().Thinking)
	started := m.ReasoningContent().StartedAt
	require.NotZero(t, started, "the first append starts the clock")

	m.AppendReasoningContent(", step two")
	require.Equal(t, "step one, step two", m.ReasoningContent().Thinking)
	require.Equal(t, started, m.ReasoningContent().StartedAt, "StartedAt is preserved across appends")
	require.Zero(t, m.ReasoningContent().FinishedAt)

	m.AppendReasoningSignature("sig-a")
	require.Equal(t, "sig-a", m.ReasoningContent().Signature)
	m.AppendReasoningSignature("-sig-b")
	require.Equal(t, "sig-a-sig-b", m.ReasoningContent().Signature)

	m.FinishThinking()
	finishedAt := m.ReasoningContent().FinishedAt
	require.NotZero(t, finishedAt)

	m.FinishThinking()
	require.Equal(t, finishedAt, m.ReasoningContent().FinishedAt, "finishing twice is idempotent")

	require.Len(t, m.Parts, 1, "reasoning mutators operate on a single part")
}

func TestMessageAppendReasoningSignatureWithoutExistingContent(t *testing.T) {
	t.Parallel()

	m := &proto.Message{}
	m.AppendReasoningSignature("sig-only")

	require.Equal(t, "sig-only", m.ReasoningContent().Signature)
	require.Empty(t, m.ReasoningContent().Thinking)
}

func TestMessageFinishThinkingWithoutReasoning(t *testing.T) {
	t.Parallel()

	m := &proto.Message{}
	m.FinishThinking()
	require.Empty(t, m.Parts, "no-op when there is no reasoning part")
}

func TestMessageThinkingDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		parts []proto.ContentPart
		want  time.Duration
	}{
		{"no reasoning", nil, 0},
		{"finished reasoning", []proto.ContentPart{proto.ReasoningContent{StartedAt: 1000, FinishedAt: 1005}}, 5 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := &proto.Message{Parts: tc.parts}
			require.Equal(t, tc.want, m.ThinkingDuration())
		})
	}

	t.Run("still thinking measures against now", func(t *testing.T) {
		t.Parallel()
		m := &proto.Message{Parts: []proto.ContentPart{proto.ReasoningContent{StartedAt: time.Now().Unix()}}}
		d := m.ThinkingDuration()
		require.GreaterOrEqual(t, d, time.Duration(0))
		require.Less(t, d, 5*time.Second)
	})
}

func TestMessageToolCallMutators(t *testing.T) {
	t.Parallel()

	m := &proto.Message{}
	m.AddToolCall(proto.ToolCall{ID: "tc1", Name: "bash", Input: `{"command":"ls`})
	m.AppendToolCallInput("tc1", ` -la"}`)
	require.Equal(t, `{"command":"ls -la"}`, m.ToolCalls()[0].Input)
	require.False(t, m.ToolCalls()[0].Finished)

	// Both mutators are no-ops for an unknown tool call id.
	m.AppendToolCallInput("missing", "ignored")
	m.FinishToolCall("missing")
	require.Len(t, m.ToolCalls(), 1)

	m.FinishToolCall("tc1")
	require.True(t, m.ToolCalls()[0].Finished)

	// AddToolCall replaces an existing call with the same id in place.
	m.AddToolCall(proto.ToolCall{ID: "tc1", Name: "bash-replaced"})
	require.Len(t, m.ToolCalls(), 1)
	require.Equal(t, "bash-replaced", m.ToolCalls()[0].Name)

	// AddToolCall appends a call with a new id.
	m.AddToolCall(proto.ToolCall{ID: "tc2", Name: "grep"})
	require.Len(t, m.ToolCalls(), 2)
	require.Equal(t, []string{"tc1", "tc2"}, []string{m.ToolCalls()[0].ID, m.ToolCalls()[1].ID})
}

func TestMessageSetToolCalls(t *testing.T) {
	t.Parallel()

	m := &proto.Message{
		Parts: []proto.ContentPart{
			proto.TextContent{Text: "keep me"},
			proto.ToolCall{ID: "old-1"},
			proto.ToolCall{ID: "old-2"},
		},
	}

	m.SetToolCalls([]proto.ToolCall{{ID: "new-1"}})

	require.Equal(t, "keep me", m.Content().Text, "non tool-call parts survive")
	require.Equal(t, []proto.ToolCall{{ID: "new-1"}}, m.ToolCalls(), "old tool calls are fully replaced")
}

func TestMessageToolResultMutators(t *testing.T) {
	t.Parallel()

	m := &proto.Message{}
	m.AddToolResult(proto.ToolResult{ToolCallID: "tc1", Content: "first"})
	require.Equal(t, []proto.ToolResult{{ToolCallID: "tc1", Content: "first"}}, m.ToolResults())

	// Unlike SetToolCalls, SetToolResults appends rather than replacing.
	m.SetToolResults([]proto.ToolResult{
		{ToolCallID: "tc2", Content: "second"},
		{ToolCallID: "tc3", Content: "third"},
	})
	require.Equal(t, []proto.ToolResult{
		{ToolCallID: "tc1", Content: "first"},
		{ToolCallID: "tc2", Content: "second"},
		{ToolCallID: "tc3", Content: "third"},
	}, m.ToolResults())
}

func TestMessageAddFinish(t *testing.T) {
	t.Parallel()

	m := &proto.Message{}
	require.False(t, m.IsFinished())

	m.AddFinish(proto.FinishReasonToolUse, "msg-1", "det-1")
	require.True(t, m.IsFinished())
	require.Equal(t, proto.FinishReasonToolUse, m.FinishReason())
	require.Equal(t, "msg-1", m.FinishPart().Message)

	// A second finish replaces the first rather than accumulating.
	m.AddFinish(proto.FinishReasonEndTurn, "msg-2", "det-2")
	require.Equal(t, proto.FinishReasonEndTurn, m.FinishReason())
	require.Equal(t, "msg-2", m.FinishPart().Message)

	finishCount := 0
	for _, part := range m.Parts {
		if _, ok := part.(proto.Finish); ok {
			finishCount++
		}
	}
	require.Equal(t, 1, finishCount)
}

func TestMessageAddImageURLAndBinary(t *testing.T) {
	t.Parallel()

	m := &proto.Message{}
	m.AddImageURL("https://x/1.png", "high")
	m.AddImageURL("https://x/2.png", "")
	require.Equal(t, []proto.ImageURLContent{
		{URL: "https://x/1.png", Detail: "high"},
		{URL: "https://x/2.png"},
	}, m.ImageURLContent())

	m.AddBinary("application/pdf", []byte{0xDE, 0xAD})
	require.Equal(t, []proto.BinaryContent{
		{MIMEType: "application/pdf", Data: []byte{0xDE, 0xAD}},
	}, m.BinaryContent())
}

func TestAttachmentConversions(t *testing.T) {
	t.Parallel()

	pa := proto.Attachment{
		FilePath: "/tmp/a.txt",
		FileName: "a.txt",
		MimeType: "text/plain",
		Content:  []byte("hello"),
	}

	ma := pa.ToMessage()
	require.Equal(t, message.Attachment{
		FilePath: "/tmp/a.txt",
		FileName: "a.txt",
		MimeType: "text/plain",
		Content:  []byte("hello"),
	}, ma)

	back := proto.AttachmentFromMessage(ma)
	require.Equal(t, pa, back)
}

func TestAttachmentsSliceConversions(t *testing.T) {
	t.Parallel()

	pas := []proto.Attachment{
		{FilePath: "/a", FileName: "a", MimeType: "text/plain", Content: []byte("1")},
		{FilePath: "/b", FileName: "b", MimeType: "text/plain", Content: []byte("2")},
	}

	mas := proto.AttachmentsToMessage(pas)
	require.Len(t, mas, 2)
	require.Equal(t, "1", string(mas[0].Content))

	back := proto.AttachmentsFromMessage(mas)
	require.Equal(t, pas, back)

	require.Empty(t, proto.AttachmentsToMessage(nil))
	require.Empty(t, proto.AttachmentsFromMessage(nil))
}

func TestAttachmentMarshalUnmarshalJSON(t *testing.T) {
	t.Parallel()

	a := proto.Attachment{
		FilePath: "/tmp/a.bin",
		FileName: "a.bin",
		MimeType: "application/octet-stream",
		Content:  []byte("hi"),
	}

	data, err := json.Marshal(a)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte("hi")), raw["content"])

	var got proto.Attachment
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, a, got)
}

func TestAttachmentUnmarshalJSONMalformed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
	}{
		{"invalid json syntax", `not json`},
		// Valid top-level JSON syntax so the outer json.Unmarshal actually
		// dispatches into Attachment.UnmarshalJSON, where the field type
		// mismatch (file_path expects a string) fails the aux decode step
		// before base64 decoding is even attempted.
		{"field type mismatch outside content", `{"file_path":123}`},
		{"invalid base64 content", `{"file_path":"/a","content":"not-valid-base64!!"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got proto.Attachment
			err := json.Unmarshal([]byte(tc.data), &got)
			require.Error(t, err)
		})
	}
}
