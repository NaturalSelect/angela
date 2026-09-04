package message

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/google"
	"charm.land/fantasy/providers/openai"
	"github.com/stretchr/testify/require"
)

func makeTestAttachments(n int, contentSize int) []Attachment {
	attachments := make([]Attachment, n)
	content := []byte(strings.Repeat("x", contentSize))
	for i := range n {
		attachments[i] = Attachment{
			FilePath: fmt.Sprintf("/path/to/file%d.txt", i),
			MimeType: "text/plain",
			Content:  content,
		}
	}
	return attachments
}

func TestToAIMessage_CorruptedMediaData(t *testing.T) {
	t.Parallel()

	msg := &Message{
		Role: Tool,
		Parts: []ContentPart{
			ToolResult{
				ToolCallID: "call_123",
				Name:       "screenshot",
				Content:    "Loaded image/png content",
				Data:       "abc\x80def",
				MIMEType:   "image/png",
			},
		},
	}

	messages := msg.ToAIMessage()
	require.Len(t, messages, 1)
	require.Len(t, messages[0].Content, 1)

	part, ok := messages[0].Content[0].(fantasy.ToolResultPart)
	require.True(t, ok)

	require.Equal(t, "call_123", part.ToolCallID)

	textContent, ok := part.Output.(fantasy.ToolResultOutputContentText)
	require.True(t, ok, "corrupted media should be downgraded to text")
	require.Equal(t, mediaLoadFailedPlaceholder, textContent.Text)
}

func TestToAIMessage_ValidMediaData(t *testing.T) {
	t.Parallel()

	validBase64 := base64.StdEncoding.EncodeToString([]byte{0x89, 0x50, 0x4E, 0x47})

	msg := &Message{
		Role: Tool,
		Parts: []ContentPart{
			ToolResult{
				ToolCallID: "call_456",
				Name:       "screenshot",
				Content:    "Loaded image/png content",
				Data:       validBase64,
				MIMEType:   "image/png",
			},
		},
	}

	messages := msg.ToAIMessage()
	require.Len(t, messages, 1)
	require.Len(t, messages[0].Content, 1)

	part, ok := messages[0].Content[0].(fantasy.ToolResultPart)
	require.True(t, ok)

	require.Equal(t, "call_456", part.ToolCallID)

	mediaContent, ok := part.Output.(fantasy.ToolResultOutputContentMedia)
	require.True(t, ok, "valid media should remain as media")
	require.Equal(t, validBase64, mediaContent.Data)
	require.Equal(t, "image/png", mediaContent.MediaType)
}

func TestToAIMessage_ASCIIButInvalidBase64(t *testing.T) {
	t.Parallel()

	msg := &Message{
		Role: Tool,
		Parts: []ContentPart{
			ToolResult{
				ToolCallID: "call_789",
				Name:       "screenshot",
				Content:    "Loaded image/png content",
				Data:       "not-valid-base64!!!",
				MIMEType:   "image/png",
			},
		},
	}

	messages := msg.ToAIMessage()
	require.Len(t, messages, 1)
	require.Len(t, messages[0].Content, 1)

	part, ok := messages[0].Content[0].(fantasy.ToolResultPart)
	require.True(t, ok)

	require.Equal(t, "call_789", part.ToolCallID)

	textContent, ok := part.Output.(fantasy.ToolResultOutputContentText)
	require.True(t, ok, "ASCII but invalid base64 should be downgraded to text")
	require.Equal(t, mediaLoadFailedPlaceholder, textContent.Text)
}

func BenchmarkPromptWithTextAttachments(b *testing.B) {
	cases := []struct {
		name        string
		numFiles    int
		contentSize int
	}{
		{"1file_100bytes", 1, 100},
		{"5files_1KB", 5, 1024},
		{"10files_10KB", 10, 10 * 1024},
		{"20files_50KB", 20, 50 * 1024},
	}

	for _, tc := range cases {
		attachments := makeTestAttachments(tc.numFiles, tc.contentSize)
		prompt := "Process these files"

		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				_ = PromptWithTextAttachments(prompt, attachments)
			}
		})
	}
}

func TestResetStreamedContent(t *testing.T) {
	t.Parallel()

	msg := &Message{}
	msg.AddImageURL("https://example.com/img.png", "high")
	msg.AppendContent("partial answer")
	msg.AppendReasoningContent("thinking...")
	msg.AddToolCall(ToolCall{ID: "1", Name: "bash"})
	msg.AddToolResult(ToolResult{ToolCallID: "1", Content: "output"})
	msg.AddFinish(FinishReasonError, "boom", "stream died")

	msg.ResetStreamedContent()

	// Streamed parts are gone.
	require.Empty(t, msg.Content().Text, "text should be cleared")
	require.Empty(t, msg.ReasoningContent().Thinking, "reasoning should be cleared")
	require.Empty(t, msg.ToolCalls(), "tool calls should be cleared")
	require.Nil(t, msg.FinishPart(), "finish should be cleared")

	// Non-streamed parts survive.
	require.Len(t, msg.ImageURLContent(), 1, "image should survive")
	require.Len(t, msg.ToolResults(), 1, "tool results should survive")
}

func TestResetStreamedContentEmpty(t *testing.T) {
	t.Parallel()

	// Reset on an empty message is a no-op and must not panic.
	msg := &Message{}
	msg.ResetStreamedContent()
	require.Empty(t, msg.Parts)
}

func TestMessage_HasShellCommandAndShellCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		parts    []ContentPart
		wantHas  bool
		wantCmds []ShellCommand
	}{
		{
			name:     "no parts",
			parts:    nil,
			wantHas:  false,
			wantCmds: nil,
		},
		{
			name:     "no shell commands among other parts",
			parts:    []ContentPart{TextContent{Text: "hi"}, ToolCall{ID: "1"}},
			wantHas:  false,
			wantCmds: nil,
		},
		{
			name:     "single shell command",
			parts:    []ContentPart{TextContent{Text: "hi"}, ShellCommand{Command: "ls", Output: "a.txt", ExitCode: 0}},
			wantHas:  true,
			wantCmds: []ShellCommand{{Command: "ls", Output: "a.txt", ExitCode: 0}},
		},
		{
			name: "multiple shell commands interleaved with other parts",
			parts: []ContentPart{
				ShellCommand{Command: "ls", ExitCode: 0},
				TextContent{Text: "hi"},
				ShellCommand{Command: "pwd", ExitCode: 1},
			},
			wantHas: true,
			wantCmds: []ShellCommand{
				{Command: "ls", ExitCode: 0},
				{Command: "pwd", ExitCode: 1},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := &Message{Parts: tt.parts}
			require.Equal(t, tt.wantHas, m.HasShellCommand())
			require.Equal(t, tt.wantCmds, m.ShellCommands())
		})
	}
}

func TestMessage_AccessorsOnEmptyMessage(t *testing.T) {
	t.Parallel()

	m := &Message{}
	require.Equal(t, TextContent{}, m.Content())
	require.Equal(t, ReasoningContent{}, m.ReasoningContent())
	require.Empty(t, m.ImageURLContent())
	require.Empty(t, m.BinaryContent())
	require.Empty(t, m.ToolCalls())
	require.Empty(t, m.ToolResults())
	require.False(t, m.IsFinished())
	require.Nil(t, m.FinishPart())
	require.Equal(t, FinishReason(""), m.FinishReason())
	require.False(t, m.IsErrorLike())
	require.False(t, m.IsThinking())
}

func TestMessage_AccessorsOnPopulatedMessage(t *testing.T) {
	t.Parallel()

	m := &Message{
		Parts: []ContentPart{
			TextContent{Text: "hello"},
			ReasoningContent{Thinking: "pondering"},
			ImageURLContent{URL: "https://x/1.png"},
			ImageURLContent{URL: "https://x/2.png"},
			BinaryContent{Path: "a.bin", MIMEType: "application/octet-stream", Data: []byte{1, 2}},
			ToolCall{ID: "tc1", Name: "bash"},
			ToolCall{ID: "tc2", Name: "view"},
			ToolResult{ToolCallID: "tc1", Content: "ok"},
		},
	}

	require.Equal(t, "hello", m.Content().Text)
	require.Equal(t, "pondering", m.ReasoningContent().Thinking)
	require.Len(t, m.ImageURLContent(), 2)
	require.Len(t, m.BinaryContent(), 1)
	require.Len(t, m.ToolCalls(), 2)
	require.Len(t, m.ToolResults(), 1)
	require.False(t, m.IsFinished())
	require.Nil(t, m.FinishPart())
}

func TestMessage_FinishReasonAndIsErrorLike(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		finish      *Finish
		wantReason  FinishReason
		wantIsError bool
	}{
		{name: "no finish part", finish: nil, wantReason: "", wantIsError: false},
		{name: "end turn", finish: &Finish{Reason: FinishReasonEndTurn}, wantReason: FinishReasonEndTurn, wantIsError: false},
		{name: "max tokens", finish: &Finish{Reason: FinishReasonMaxTokens}, wantReason: FinishReasonMaxTokens, wantIsError: false},
		{name: "tool use", finish: &Finish{Reason: FinishReasonToolUse}, wantReason: FinishReasonToolUse, wantIsError: false},
		{name: "canceled", finish: &Finish{Reason: FinishReasonCanceled}, wantReason: FinishReasonCanceled, wantIsError: false},
		{name: "error", finish: &Finish{Reason: FinishReasonError}, wantReason: FinishReasonError, wantIsError: true},
		{name: "content filter", finish: &Finish{Reason: FinishReasonContentFilter}, wantReason: FinishReasonContentFilter, wantIsError: true},
		{name: "unknown", finish: &Finish{Reason: FinishReasonUnknown}, wantReason: FinishReasonUnknown, wantIsError: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := &Message{}
			if tt.finish != nil {
				m.Parts = append(m.Parts, *tt.finish)
			}
			require.Equal(t, tt.wantReason, m.FinishReason())
			require.Equal(t, tt.wantIsError, m.IsErrorLike())
		})
	}
}

func TestMessage_IsThinking(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msg  *Message
		want bool
	}{
		{
			name: "no reasoning",
			msg:  &Message{},
			want: false,
		},
		{
			name: "reasoning without text and not finished",
			msg:  &Message{Parts: []ContentPart{ReasoningContent{Thinking: "hmm"}}},
			want: true,
		},
		{
			name: "reasoning with text present",
			msg: &Message{Parts: []ContentPart{
				ReasoningContent{Thinking: "hmm"},
				TextContent{Text: "answer"},
			}},
			want: false,
		},
		{
			name: "reasoning but message already finished",
			msg: &Message{Parts: []ContentPart{
				ReasoningContent{Thinking: "hmm"},
				Finish{Reason: FinishReasonEndTurn},
			}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.msg.IsThinking())
		})
	}
}

func TestContentPartStringMethods(t *testing.T) {
	t.Parallel()

	require.Equal(t, "thinking hard", ReasoningContent{Thinking: "thinking hard"}.String())
	require.Equal(t, "hello", TextContent{Text: "hello"}.String())
	require.Equal(t, "https://example.com/1.png", ImageURLContent{URL: "https://example.com/1.png"}.String())
}

func TestBinaryContentString(t *testing.T) {
	t.Parallel()

	data := []byte{0x01, 0x02, 0x03}
	b64 := base64.StdEncoding.EncodeToString(data)
	bc := BinaryContent{MIMEType: "image/png", Data: data}

	require.Equal(t, b64, bc.String(catwalk.InferenceProviderAnthropic))
	require.Equal(t, "data:image/png;base64,"+b64, bc.String(catwalk.InferenceProviderOpenAI))
}

func TestMessage_AppendThoughtSignature(t *testing.T) {
	t.Parallel()

	t.Run("creates a reasoning part when none exists", func(t *testing.T) {
		t.Parallel()
		m := &Message{}
		m.AppendThoughtSignature("sig-1", "tool-1")
		rc := m.ReasoningContent()
		require.Equal(t, "sig-1", rc.ThoughtSignature)
	})

	t.Run("accumulates into the existing reasoning part and records the tool ID", func(t *testing.T) {
		t.Parallel()
		m := &Message{Parts: []ContentPart{ReasoningContent{Thinking: "hmm", ThoughtSignature: "a"}}}
		m.AppendThoughtSignature("b", "tool-42")
		rc := m.ReasoningContent()
		require.Equal(t, "hmm", rc.Thinking)
		require.Equal(t, "ab", rc.ThoughtSignature)
		require.Equal(t, "tool-42", rc.ToolID)
	})
}

func TestMessage_AppendReasoningSignature(t *testing.T) {
	t.Parallel()

	t.Run("creates a reasoning part when none exists", func(t *testing.T) {
		t.Parallel()
		m := &Message{}
		m.AppendReasoningSignature("sig")
		require.Equal(t, "sig", m.ReasoningContent().Signature)
	})

	t.Run("accumulates into the existing reasoning part", func(t *testing.T) {
		t.Parallel()
		m := &Message{Parts: []ContentPart{ReasoningContent{Thinking: "hmm", Signature: "a"}}}
		m.AppendReasoningSignature("b")
		rc := m.ReasoningContent()
		require.Equal(t, "hmm", rc.Thinking)
		require.Equal(t, "ab", rc.Signature)
	})
}

func TestMessage_SetReasoningResponsesData(t *testing.T) {
	t.Parallel()

	data := &openai.ResponsesReasoningMetadata{ItemID: "item-1"}

	t.Run("no-op when there is no reasoning part yet", func(t *testing.T) {
		t.Parallel()
		m := &Message{}
		m.SetReasoningResponsesData(data)
		require.Nil(t, m.ReasoningContent().ResponsesData)
	})

	t.Run("attaches to the existing reasoning part", func(t *testing.T) {
		t.Parallel()
		m := &Message{Parts: []ContentPart{ReasoningContent{Thinking: "hmm"}}}
		m.SetReasoningResponsesData(data)
		rc := m.ReasoningContent()
		require.Equal(t, "hmm", rc.Thinking)
		require.Same(t, data, rc.ResponsesData)
	})
}

func TestMessage_ThinkingDuration(t *testing.T) {
	t.Parallel()

	t.Run("zero when reasoning never started", func(t *testing.T) {
		t.Parallel()
		m := &Message{}
		require.Zero(t, m.ThinkingDuration())
	})

	t.Run("measures elapsed time once finished", func(t *testing.T) {
		t.Parallel()
		start := time.Now().Unix()
		m := &Message{Parts: []ContentPart{ReasoningContent{StartedAt: start, FinishedAt: start + 5}}}
		require.Equal(t, 5*time.Second, m.ThinkingDuration())
	})

	t.Run("measures against now while still in progress", func(t *testing.T) {
		t.Parallel()
		start := time.Now().Add(-2 * time.Second).Unix()
		m := &Message{Parts: []ContentPart{ReasoningContent{StartedAt: start}}}
		require.GreaterOrEqual(t, m.ThinkingDuration(), 2*time.Second)
	})
}

func TestMessage_FinishToolCall(t *testing.T) {
	t.Parallel()

	t.Run("marks the matching tool call finished", func(t *testing.T) {
		t.Parallel()
		m := &Message{Parts: []ContentPart{
			ToolCall{ID: "tc1", Name: "bash", Input: `{"cmd":"ls"}`},
			ToolCall{ID: "tc2", Name: "view"},
		}}
		m.FinishToolCall("tc1")
		calls := m.ToolCalls()
		require.True(t, calls[0].Finished)
		require.Equal(t, `{"cmd":"ls"}`, calls[0].Input)
		require.False(t, calls[1].Finished)
	})

	t.Run("no-op for an unknown tool call ID", func(t *testing.T) {
		t.Parallel()
		m := &Message{Parts: []ContentPart{ToolCall{ID: "tc1", Name: "bash"}}}
		m.FinishToolCall("does-not-exist")
		require.False(t, m.ToolCalls()[0].Finished)
	})
}

func TestMessage_AppendToolCallInput(t *testing.T) {
	t.Parallel()

	t.Run("appends to the matching call by ID", func(t *testing.T) {
		t.Parallel()
		m := &Message{Parts: []ContentPart{ToolCall{ID: "tc1", Name: "bash", Input: `{"a":`}}}
		m.AppendToolCallInput("tc1", `1}`)
		require.Equal(t, `{"a":1}`, m.ToolCalls()[0].Input)
	})

	t.Run("no-op for an unknown ID", func(t *testing.T) {
		t.Parallel()
		m := &Message{Parts: []ContentPart{ToolCall{ID: "tc1", Input: "x"}}}
		m.AppendToolCallInput("nope", "y")
		require.Equal(t, "x", m.ToolCalls()[0].Input)
	})
}

func TestMessage_SetToolCalls(t *testing.T) {
	t.Parallel()

	m := &Message{Parts: []ContentPart{
		TextContent{Text: "hi"},
		ToolCall{ID: "old1"},
		ToolCall{ID: "old2"},
	}}

	m.SetToolCalls([]ToolCall{{ID: "new1"}, {ID: "new2"}, {ID: "new3"}})

	calls := m.ToolCalls()
	require.Len(t, calls, 3)
	require.Equal(t, "new1", calls[0].ID)
	require.Equal(t, "new2", calls[1].ID)
	require.Equal(t, "new3", calls[2].ID)
	require.Equal(t, "hi", m.Content().Text, "non tool-call parts must survive")
}

func TestMessage_SetToolCallsEmptyClearsExisting(t *testing.T) {
	t.Parallel()

	m := &Message{Parts: []ContentPart{ToolCall{ID: "old"}}}
	m.SetToolCalls(nil)
	require.Empty(t, m.ToolCalls())
}

func TestMessage_SetToolResultsAppendsRatherThanReplaces(t *testing.T) {
	t.Parallel()

	m := &Message{Parts: []ContentPart{ToolResult{ToolCallID: "existing"}}}
	m.SetToolResults([]ToolResult{{ToolCallID: "r1"}, {ToolCallID: "r2"}})

	results := m.ToolResults()
	require.Len(t, results, 3)
	require.Equal(t, "existing", results[0].ToolCallID)
	require.Equal(t, "r1", results[1].ToolCallID)
	require.Equal(t, "r2", results[2].ToolCallID)
}

func TestMessage_AddBinary(t *testing.T) {
	t.Parallel()

	m := &Message{}
	m.AddBinary("image/png", []byte{1, 2, 3})

	contents := m.BinaryContent()
	require.Len(t, contents, 1)
	require.Equal(t, "image/png", contents[0].MIMEType)
	require.Equal(t, []byte{1, 2, 3}, contents[0].Data)
}

func TestMessage_AddFinishReplacesExisting(t *testing.T) {
	t.Parallel()

	m := &Message{}
	m.AddFinish(FinishReasonToolUse, "first", "d1")
	require.Len(t, m.Parts, 1)

	m.AddFinish(FinishReasonEndTurn, "second", "d2")

	require.Len(t, m.Parts, 1, "a second AddFinish must replace, not accumulate")
	require.Equal(t, FinishReasonEndTurn, m.FinishReason())
	require.Equal(t, "second", m.FinishPart().Message)
}

func TestMessage_CloneIsIndependentOfOriginal(t *testing.T) {
	t.Parallel()

	m := &Message{ID: "m1", Parts: []ContentPart{TextContent{Text: "hi"}}}
	clone := m.Clone()

	clone.Parts[0] = TextContent{Text: "changed"}
	require.Equal(t, "hi", m.Content().Text, "mutating an element in the clone's Parts must not affect the original")
	require.Equal(t, "changed", clone.Content().Text)

	clone.Parts = append(clone.Parts, ToolCall{ID: "tc1"})
	require.Len(t, m.Parts, 1, "growing the clone's Parts must not affect the original's length")
	require.Len(t, clone.Parts, 2)

	clone.ID = "different"
	require.Equal(t, "m1", m.ID, "clone must be independent of source struct fields")
}

func TestPromptWithTextAttachments(t *testing.T) {
	t.Parallel()

	t.Run("no attachments returns the prompt unchanged", func(t *testing.T) {
		t.Parallel()
		got := PromptWithTextAttachments("do the thing", nil)
		require.Equal(t, "do the thing", got)
	})

	t.Run("single attachment with a file path", func(t *testing.T) {
		t.Parallel()
		got := PromptWithTextAttachments("prompt", []Attachment{
			{FilePath: "/tmp/a.txt", MimeType: "text/plain", Content: []byte("hello")},
		})
		require.Contains(t, got, "prompt")
		require.Contains(t, got, "<system_info>")
		require.Contains(t, got, "<file path='/tmp/a.txt'>")
		require.Contains(t, got, "hello")
	})

	t.Run("attachment without a file path uses a bare file tag", func(t *testing.T) {
		t.Parallel()
		got := PromptWithTextAttachments("prompt", []Attachment{
			{MimeType: "text/plain", Content: []byte("hello")},
		})
		require.Contains(t, got, "<file>\n")
		require.NotContains(t, got, "path=")
	})

	t.Run("non-text attachments are skipped", func(t *testing.T) {
		t.Parallel()
		got := PromptWithTextAttachments("prompt", []Attachment{
			{MimeType: "image/png", Content: []byte{0xFF}},
		})
		require.Equal(t, "prompt", got, "non-text attachments must not appear in the prompt")
	})

	t.Run("multiple text attachments are all included in order", func(t *testing.T) {
		t.Parallel()
		got := PromptWithTextAttachments("prompt", []Attachment{
			{FilePath: "/a.txt", MimeType: "text/plain", Content: []byte("AAA")},
			{FilePath: "/b.txt", MimeType: "text/markdown", Content: []byte("BBB")},
		})
		idxA := strings.Index(got, "AAA")
		idxB := strings.Index(got, "BBB")
		require.True(t, idxA >= 0 && idxB >= 0 && idxA < idxB)
		require.Equal(t, 1, strings.Count(got, "<system_info>"), "the system_info banner should appear once even with multiple attachments")
	})
}

func TestToAIMessage_UserRolePlainText(t *testing.T) {
	t.Parallel()

	m := &Message{
		Role:  User,
		Parts: []ContentPart{TextContent{Text: "  hello there  "}},
	}

	msgs := m.ToAIMessage()
	require.Len(t, msgs, 1)
	require.Equal(t, fantasy.MessageRoleUser, msgs[0].Role)
	require.Len(t, msgs[0].Content, 1)
	textPart, ok := msgs[0].Content[0].(fantasy.TextPart)
	require.True(t, ok)
	require.Equal(t, "hello there", textPart.Text, "text should be trimmed")
}

func TestToAIMessage_UserRoleEmpty(t *testing.T) {
	t.Parallel()

	m := &Message{Role: User}
	msgs := m.ToAIMessage()
	require.Len(t, msgs, 1)
	require.Empty(t, msgs[0].Content)
}

func TestToAIMessage_UserRoleTextAttachmentFoldedIntoPrompt(t *testing.T) {
	t.Parallel()

	m := &Message{
		Role: User,
		Parts: []ContentPart{
			TextContent{Text: "look at this"},
			BinaryContent{Path: "/notes.txt", MIMEType: "text/plain", Data: []byte("file contents")},
		},
	}

	msgs := m.ToAIMessage()
	require.Len(t, msgs, 1)
	require.Len(t, msgs[0].Content, 1, "a text attachment folds into the single text part instead of becoming a file part")
	textPart, ok := msgs[0].Content[0].(fantasy.TextPart)
	require.True(t, ok)
	require.Contains(t, textPart.Text, "look at this")
	require.Contains(t, textPart.Text, "file contents")
	require.Contains(t, textPart.Text, "/notes.txt")
}

func TestToAIMessage_UserRoleNonTextAttachmentBecomesFilePart(t *testing.T) {
	t.Parallel()

	m := &Message{
		Role: User,
		Parts: []ContentPart{
			TextContent{Text: "see attached"},
			BinaryContent{Path: "/img.png", MIMEType: "image/png", Data: []byte{0x89, 0x50}},
		},
	}

	msgs := m.ToAIMessage()
	require.Len(t, msgs, 1)
	require.Len(t, msgs[0].Content, 2)

	_, ok := msgs[0].Content[0].(fantasy.TextPart)
	require.True(t, ok)
	filePart, ok := msgs[0].Content[1].(fantasy.FilePart)
	require.True(t, ok)
	require.Equal(t, "/img.png", filePart.Filename)
	require.Equal(t, "image/png", filePart.MediaType)
	require.Equal(t, []byte{0x89, 0x50}, filePart.Data)
}

func TestToAIMessage_UserRoleShellCommandsBecomeContext(t *testing.T) {
	t.Parallel()

	m := &Message{
		Role: User,
		Parts: []ContentPart{
			ShellCommand{Command: "ls -la", Output: "file.txt", ExitCode: 0},
		},
	}

	msgs := m.ToAIMessage()
	require.Len(t, msgs, 1)
	require.Len(t, msgs[0].Content, 1)
	textPart, ok := msgs[0].Content[0].(fantasy.TextPart)
	require.True(t, ok)
	require.Contains(t, textPart.Text, "$ ls -la")
	require.Contains(t, textPart.Text, "file.txt")
	require.Contains(t, textPart.Text, "(exit code 0)")
}

func TestToAIMessage_UserRoleShellCommandAppendedAfterExistingText(t *testing.T) {
	t.Parallel()

	m := &Message{
		Role: User,
		Parts: []ContentPart{
			TextContent{Text: "please check this"},
			ShellCommand{Command: "pwd", Output: "/tmp", ExitCode: 0},
		},
	}

	msgs := m.ToAIMessage()
	require.Len(t, msgs, 1)
	require.Len(t, msgs[0].Content, 1)
	textPart, ok := msgs[0].Content[0].(fantasy.TextPart)
	require.True(t, ok)
	require.Equal(t, "please check this\n\n$ pwd\n/tmp\n(exit code 0)", textPart.Text)
}

func TestToAIMessage_AssistantRoleTextOnly(t *testing.T) {
	t.Parallel()

	m := &Message{Role: Assistant, Parts: []ContentPart{TextContent{Text: " hi "}}}
	msgs := m.ToAIMessage()
	require.Len(t, msgs, 1)
	require.Equal(t, fantasy.MessageRoleAssistant, msgs[0].Role)
	require.Len(t, msgs[0].Content, 1)
	textPart, ok := msgs[0].Content[0].(fantasy.TextPart)
	require.True(t, ok)
	require.Equal(t, "hi", textPart.Text)
}

func TestToAIMessage_AssistantRoleEmpty(t *testing.T) {
	t.Parallel()

	m := &Message{Role: Assistant}
	msgs := m.ToAIMessage()
	require.Len(t, msgs, 1)
	require.Empty(t, msgs[0].Content)
}

func TestToAIMessage_AssistantRoleReasoningWithAllProviderSignatures(t *testing.T) {
	t.Parallel()

	m := &Message{
		Role: Assistant,
		Parts: []ContentPart{
			TextContent{Text: "answer"},
			ReasoningContent{
				Thinking:         "let me think",
				Signature:        "anthropic-sig",
				ThoughtSignature: "google-sig",
				ToolID:           "tool-7",
				ResponsesData:    &openai.ResponsesReasoningMetadata{ItemID: "item-9"},
			},
			ToolCall{ID: "tc1", Name: "bash", Input: `{"cmd":"ls"}`, ProviderExecuted: true},
		},
	}

	msgs := m.ToAIMessage()
	require.Len(t, msgs, 1)
	require.Len(t, msgs[0].Content, 3)

	_, ok := msgs[0].Content[0].(fantasy.TextPart)
	require.True(t, ok)

	reasoningPart, ok := msgs[0].Content[1].(fantasy.ReasoningPart)
	require.True(t, ok)
	require.Equal(t, "let me think", reasoningPart.Text)

	anthropicOpt, ok := reasoningPart.ProviderOptions[anthropic.Name].(*anthropic.ReasoningOptionMetadata)
	require.True(t, ok)
	require.Equal(t, "anthropic-sig", anthropicOpt.Signature)

	openaiOpt, ok := reasoningPart.ProviderOptions[openai.Name].(*openai.ResponsesReasoningMetadata)
	require.True(t, ok)
	require.Equal(t, "item-9", openaiOpt.ItemID)

	googleOpt, ok := reasoningPart.ProviderOptions[google.Name].(*google.ReasoningMetadata)
	require.True(t, ok)
	require.Equal(t, "google-sig", googleOpt.Signature)
	require.Equal(t, "tool-7", googleOpt.ToolID)

	toolCallPart, ok := msgs[0].Content[2].(fantasy.ToolCallPart)
	require.True(t, ok)
	require.Equal(t, "tc1", toolCallPart.ToolCallID)
	require.Equal(t, "bash", toolCallPart.ToolName)
	require.Equal(t, `{"cmd":"ls"}`, toolCallPart.Input)
	require.True(t, toolCallPart.ProviderExecuted)
}

func TestToAIMessage_AssistantRoleReasoningWithoutSignatures(t *testing.T) {
	t.Parallel()

	m := &Message{
		Role: Assistant,
		Parts: []ContentPart{
			ReasoningContent{Thinking: "plain thought"},
		},
	}

	msgs := m.ToAIMessage()
	require.Len(t, msgs[0].Content, 1)
	reasoningPart, ok := msgs[0].Content[0].(fantasy.ReasoningPart)
	require.True(t, ok)
	require.Equal(t, "plain thought", reasoningPart.Text)
	require.Empty(t, reasoningPart.ProviderOptions, "no signature fields set means no provider options attached")
}

func TestToAIMessage_ToolRoleIsError(t *testing.T) {
	t.Parallel()

	m := &Message{
		Role: Tool,
		Parts: []ContentPart{
			ToolResult{ToolCallID: "call_err", Content: "boom", IsError: true},
		},
	}

	msgs := m.ToAIMessage()
	require.Len(t, msgs, 1)
	require.Len(t, msgs[0].Content, 1)

	part, ok := msgs[0].Content[0].(fantasy.ToolResultPart)
	require.True(t, ok)
	errContent, ok := part.Output.(fantasy.ToolResultOutputContentError)
	require.True(t, ok)
	require.EqualError(t, errContent.Error, "boom")
}

func TestToAIMessage_ToolRolePlainTextResult(t *testing.T) {
	t.Parallel()

	m := &Message{
		Role: Tool,
		Parts: []ContentPart{
			ToolResult{ToolCallID: "call_1", Content: "plain output"},
		},
	}

	msgs := m.ToAIMessage()
	require.Len(t, msgs, 1)
	part, ok := msgs[0].Content[0].(fantasy.ToolResultPart)
	require.True(t, ok)
	textContent, ok := part.Output.(fantasy.ToolResultOutputContentText)
	require.True(t, ok)
	require.Equal(t, "plain output", textContent.Text)
}

func TestToAIMessage_ToolRoleMultipleResults(t *testing.T) {
	t.Parallel()

	m := &Message{
		Role: Tool,
		Parts: []ContentPart{
			ToolResult{ToolCallID: "call_1", Content: "first"},
			ToolResult{ToolCallID: "call_2", Content: "second"},
		},
	}

	msgs := m.ToAIMessage()
	require.Len(t, msgs, 1)
	require.Len(t, msgs[0].Content, 2)
	require.Equal(t, "call_1", msgs[0].Content[0].(fantasy.ToolResultPart).ToolCallID)
	require.Equal(t, "call_2", msgs[0].Content[1].(fantasy.ToolResultPart).ToolCallID)
}

func TestToAIMessage_UnhandledRoleReturnsEmpty(t *testing.T) {
	t.Parallel()

	m := &Message{Role: System, Parts: []ContentPart{TextContent{Text: "system prompt"}}}
	msgs := m.ToAIMessage()
	require.Empty(t, msgs)
}
