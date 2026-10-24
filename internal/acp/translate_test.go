package acp

import (
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/acp/protocol"
	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/reminder"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

const testCwd = "/work/proj"

type diffStep struct {
	next message.Message
	want []protocol.SessionUpdate
}

// runDiffSteps feeds a message through successive snapshots, the way the
// event pump does, and checks the idempotence invariant at every step.
func runDiffSteps(t *testing.T, dc DiffContext, steps []diffStep) {
	t.Helper()
	var prev message.Message
	for i, step := range steps {
		require.Equal(t, step.want, Diff(prev, step.next, dc), "step %d", i)
		require.Empty(t, Diff(step.next, step.next, dc), "step %d is not idempotent", i)
		prev = step.next
	}
}

func assistantMsg(id string, parts ...message.ContentPart) message.Message {
	return message.Message{ID: id, Role: message.Assistant, Parts: parts}
}

func toolMsg(id string, results ...message.ToolResult) message.Message {
	m := message.Message{ID: id, Role: message.Tool}
	for _, r := range results {
		m.Parts = append(m.Parts, r)
	}
	return m
}

func thought(id, text string) protocol.SessionUpdate {
	return protocol.AgentThoughtChunk{MessageID: id, Content: protocol.TextBlock{Text: text}}
}

func answer(id, text string) protocol.SessionUpdate {
	return protocol.AgentMessageChunk{MessageID: id, Content: protocol.TextBlock{Text: text}}
}

func userChunk(id string, block protocol.ContentBlock) protocol.SessionUpdate {
	return protocol.UserMessageChunk{MessageID: id, Content: block}
}

func TestTextDelta(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		sent, next string
		want       string
	}{
		{name: "first text", sent: "", next: "a", want: "a"},
		{name: "appended", sent: "a", next: "ab", want: "b"},
		{name: "unchanged", sent: "ab", next: "ab", want: ""},
		{name: "both empty", sent: "", next: "", want: ""},
		{name: "shortened to a prefix", sent: "ab", next: "a", want: ""},
		{name: "cleared", sent: "ab", next: "", want: ""},
		{name: "rewritten", sent: "ab", next: "xy", want: "xy"},
		{name: "rewritten to a longer text", sent: "ab", next: "xyz", want: "xyz"},
		{name: "appended multibyte", sent: "你", next: "你好", want: "好"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, textDelta(tt.sent, tt.next))
		})
	}
}

func TestDiffRoles(t *testing.T) {
	t.Parallel()

	system := message.Message{
		ID: "s1", Role: message.System,
		Parts: []message.ContentPart{message.TextContent{Text: "model changed"}},
	}
	require.Empty(t, Diff(message.Message{}, system, DiffContext{}))
	require.Empty(t, Diff(message.Message{}, assistantMsg("a1"), DiffContext{}))
	require.Empty(t, Diff(message.Message{}, toolMsg("t1"), DiffContext{}))
}

func TestDiffAssistantStreaming(t *testing.T) {
	t.Parallel()

	think := func(s string) message.ContentPart { return message.ReasoningContent{Thinking: s} }
	say := func(s string) message.ContentPart { return message.TextContent{Text: s} }
	finish := message.Finish{Reason: message.FinishReasonEndTurn}

	runDiffSteps(t, DiffContext{Cwd: testCwd}, []diffStep{
		{
			next: assistantMsg("a1", think("a")),
			want: []protocol.SessionUpdate{thought("a1", "a")},
		},
		{
			next: assistantMsg("a1", think("ab"), say("x")),
			want: []protocol.SessionUpdate{thought("a1", "b"), answer("a1", "x")},
		},
		{
			next: assistantMsg("a1", think("ab"), say("xy")),
			want: []protocol.SessionUpdate{answer("a1", "y")},
		},
		{
			next: assistantMsg("a1", think("ab"), say("xy"), finish),
			want: nil,
		},
		{
			// A retry wipes the streamed content; nothing can be retracted.
			next: assistantMsg("a1"),
			want: nil,
		},
		{
			next: assistantMsg("a1", say("z")),
			want: []protocol.SessionUpdate{answer("a1", "z")},
		},
		{
			// A rewrite that is not an extension is sent whole.
			next: assistantMsg("a1", say("Q")),
			want: []protocol.SessionUpdate{answer("a1", "Q")},
		},
	})
}

func TestDiffAssistantChunksConcatenateToFinalText(t *testing.T) {
	t.Parallel()

	snapshots := []string{"", "He", "Hel", "Hel", "Hello, ", "Hello, wor", "Hello, world"}
	var prev message.Message
	var got strings.Builder
	for _, text := range snapshots {
		next := assistantMsg("a1", message.TextContent{Text: text})
		for _, u := range Diff(prev, next, DiffContext{}) {
			got.WriteString(u.(protocol.AgentMessageChunk).Content.(protocol.TextBlock).Text)
		}
		prev = next
	}
	require.Equal(t, "Hello, world", got.String())
}

func TestDiffAssistantPartOrderFollowsTheMessage(t *testing.T) {
	t.Parallel()

	editInput := mustJSON(t, tools.EditParams{FilePath: "a.go", OldString: "a", NewString: "b"})
	bashInput := mustJSON(t, tools.BashParams{Command: "ls"})
	next := assistantMsg("a1",
		message.ReasoningContent{Thinking: "plan"},
		message.TextContent{Text: "doing it"},
		message.ToolCall{ID: "c1", Name: toolnames.Edit, Input: editInput, Finished: true},
		message.ToolCall{ID: "c2", Name: toolnames.Bash, Input: bashInput, Finished: true},
		message.Finish{Reason: message.FinishReasonToolUse},
	)

	got := Diff(message.Message{}, next, DiffContext{Cwd: testCwd})

	require.Equal(t, []protocol.SessionUpdate{
		thought("a1", "plan"),
		answer("a1", "doing it"),
		protocol.ToolCallStart{
			ID: "c1", Name: toolnames.Edit, Title: "Edit a.go", Kind: protocol.ToolKindEdit,
			Status:    protocol.ToolCallStatusPending,
			Locations: []protocol.Location{{Path: filepath.Join(testCwd, "a.go")}},
			RawInput:  json.RawMessage(editInput),
		},
		protocol.ToolCallStart{
			ID: "c2", Name: toolnames.Bash, Title: "ls", Kind: protocol.ToolKindExecute,
			Status:   protocol.ToolCallStatusPending,
			RawInput: json.RawMessage(bashInput),
		},
	}, got)
}

func TestDiffToolCallLifecycle(t *testing.T) {
	t.Parallel()

	editPath := filepath.Join(testCwd, "a.go")
	input := mustJSON(t, tools.EditParams{FilePath: "a.go", OldString: "a", NewString: "b"})
	announced := message.ToolCall{ID: "c1", Name: toolnames.Edit}
	finished := message.ToolCall{ID: "c1", Name: toolnames.Edit, Input: input, Finished: true}

	t.Run("announced with empty input then finished", func(t *testing.T) {
		t.Parallel()
		runDiffSteps(t, DiffContext{Cwd: testCwd}, []diffStep{
			{
				next: assistantMsg("a1", announced),
				want: []protocol.SessionUpdate{protocol.ToolCallStart{
					ID: "c1", Name: toolnames.Edit, Title: "Edit", Kind: protocol.ToolKindEdit,
					Status: protocol.ToolCallStatusPending,
				}},
			},
			{
				// The input is still streaming: the title cannot improve yet.
				next: assistantMsg("a1", message.ToolCall{ID: "c1", Name: toolnames.Edit, Input: `{"file_pa`}),
				want: nil,
			},
			{
				next: assistantMsg("a1", finished),
				want: []protocol.SessionUpdate{protocol.ToolCallUpdate{
					ID:        "c1",
					Title:     new("Edit a.go"),
					Status:    new(protocol.ToolCallStatusInProgress),
					Locations: []protocol.Location{{Path: editPath}},
				}},
			},
			{
				next: assistantMsg("a1", finished, message.Finish{Reason: message.FinishReasonToolUse}),
				want: nil,
			},
		})
	})

	t.Run("first seen already finished", func(t *testing.T) {
		t.Parallel()
		runDiffSteps(t, DiffContext{Cwd: testCwd}, []diffStep{{
			next: assistantMsg("a1", finished),
			want: []protocol.SessionUpdate{protocol.ToolCallStart{
				ID: "c1", Name: toolnames.Edit, Title: "Edit a.go", Kind: protocol.ToolKindEdit,
				Status:    protocol.ToolCallStatusPending,
				Locations: []protocol.Location{{Path: editPath}},
				RawInput:  json.RawMessage(input),
			}},
		}})
	})

	t.Run("finished with input that is not json carries no raw input", func(t *testing.T) {
		t.Parallel()
		call := message.ToolCall{ID: "c1", Name: toolnames.Bash, Input: "ls -la", Finished: true}
		runDiffSteps(t, DiffContext{Cwd: testCwd}, []diffStep{{
			next: assistantMsg("a1", call),
			want: []protocol.SessionUpdate{protocol.ToolCallStart{
				ID: "c1", Name: toolnames.Bash, Title: "Bash", Kind: protocol.ToolKindExecute,
				Status: protocol.ToolCallStatusPending,
			}},
		}})
	})

	t.Run("start always precedes updates for an id", func(t *testing.T) {
		t.Parallel()
		var prev message.Message
		seenStart := map[string]bool{}
		for _, call := range []message.ToolCall{
			{ID: "c1", Name: toolnames.Read},
			{ID: "c1", Name: toolnames.Read, Input: `{"file_path":"a.go"}`, Finished: true},
		} {
			next := assistantMsg("a1", call)
			for _, u := range Diff(prev, next, DiffContext{Cwd: testCwd}) {
				switch u := u.(type) {
				case protocol.ToolCallStart:
					seenStart[u.ID] = true
				case protocol.ToolCallUpdate:
					require.True(t, seenStart[u.ID], "update for %s before its start", u.ID)
				}
			}
			prev = next
		}
		require.True(t, seenStart["c1"])
	})

	t.Run("results complete the call", func(t *testing.T) {
		t.Parallel()
		dc := DiffContext{
			Cwd: testCwd,
			ToolCall: func(id string) (message.ToolCall, bool) {
				return finished, id == "c1"
			},
		}
		ok := message.ToolResult{
			ToolCallID: "c1", Name: toolnames.Edit, Content: "edited",
			Metadata: mustJSON(t, tools.EditResponseMetadata{OldContent: "a", NewContent: "b"}),
		}
		failed := message.ToolResult{
			ToolCallID: "c1", Name: toolnames.Edit, Content: "old_string not found", IsError: true,
		}
		runDiffSteps(t, dc, []diffStep{
			{
				next: toolMsg("t1", ok),
				want: []protocol.SessionUpdate{protocol.ToolCallUpdate{
					ID:      "c1",
					Status:  new(protocol.ToolCallStatusCompleted),
					Content: []protocol.ToolCallContent{protocol.ToolDiff{Path: editPath, OldText: new("a"), NewText: "b"}},
				}},
			},
			{
				next: toolMsg("t1", failed),
				want: []protocol.SessionUpdate{protocol.ToolCallUpdate{
					ID:      "c1",
					Status:  new(protocol.ToolCallStatusFailed),
					Content: []protocol.ToolCallContent{protocol.ToolContent{Block: protocol.TextBlock{Text: "old_string not found"}}},
				}},
			},
		})
	})

	t.Run("each result in a message is reported once", func(t *testing.T) {
		t.Parallel()
		r1 := message.ToolResult{ToolCallID: "c1", Name: toolnames.Bash, Content: "one"}
		r2 := message.ToolResult{ToolCallID: "c2", Name: toolnames.Bash, Content: "two", IsError: true}
		update := func(id, text string, status protocol.ToolCallStatus) protocol.SessionUpdate {
			return protocol.ToolCallUpdate{
				ID:      id,
				Status:  &status,
				Content: []protocol.ToolCallContent{protocol.ToolContent{Block: protocol.TextBlock{Text: text}}},
			}
		}
		runDiffSteps(t, DiffContext{Cwd: testCwd}, []diffStep{
			{
				next: toolMsg("t1", r1),
				want: []protocol.SessionUpdate{update("c1", "one", protocol.ToolCallStatusCompleted)},
			},
			{
				next: toolMsg("t1", r1, r2),
				want: []protocol.SessionUpdate{update("c2", "two", protocol.ToolCallStatusFailed)},
			},
		})
	})
}

func TestDiffToolResultWithoutContext(t *testing.T) {
	t.Parallel()

	result := message.ToolResult{
		ToolCallID: "c1", Name: toolnames.Edit, Content: "edited",
		Metadata: mustJSON(t, tools.EditResponseMetadata{OldContent: "a", NewContent: "b"}),
	}

	got := Diff(message.Message{}, toolMsg("t1", result), DiffContext{})

	require.Equal(t, []protocol.SessionUpdate{protocol.ToolCallUpdate{
		ID:      "c1",
		Status:  new(protocol.ToolCallStatusCompleted),
		Content: []protocol.ToolCallContent{protocol.ToolContent{Block: protocol.TextBlock{Text: "edited"}}},
	}}, got)
}

func TestDiffUser(t *testing.T) {
	t.Parallel()

	png := []byte{0x89, 'P', 'N', 'G'}
	userMsg := func(parts ...message.ContentPart) message.Message {
		return message.Message{ID: "u1", Role: message.User, Parts: parts}
	}
	finish := message.Finish{Reason: message.FinishReasonEndTurn}
	wrapped := reminder.Wrap("remember the todos")

	tests := []struct {
		name string
		prev message.Message
		next message.Message
		want []protocol.SessionUpdate
	}{
		{
			name: "text",
			next: userMsg(message.TextContent{Text: "fix it"}, finish),
			want: []protocol.SessionUpdate{userChunk("u1", protocol.TextBlock{Text: "fix it"})},
		},
		{
			name: "empty text is skipped",
			next: userMsg(message.TextContent{}, finish),
			want: nil,
		},
		{
			name: "image attachment",
			next: userMsg(
				message.TextContent{Text: "look"},
				message.BinaryContent{Path: "a.png", MIMEType: "image/png", Data: png},
			),
			want: []protocol.SessionUpdate{
				userChunk("u1", protocol.TextBlock{Text: "look"}),
				userChunk("u1", protocol.ImageBlock{MIMEType: "image/png", Data: png}),
			},
		},
		{
			name: "non image attachment is skipped",
			next: userMsg(
				message.TextContent{Text: "see file"},
				message.BinaryContent{Path: "a.pdf", MIMEType: "application/pdf", Data: []byte("%PDF")},
			),
			want: []protocol.SessionUpdate{userChunk("u1", protocol.TextBlock{Text: "see file"})},
		},
		{
			name: "image url is skipped",
			next: userMsg(message.ImageURLContent{URL: "https://example.com/a.png"}),
			want: nil,
		},
		{
			name: "shell command",
			next: userMsg(message.ShellCommand{Command: "ls", Output: "a\nb\n"}),
			want: []protocol.SessionUpdate{userChunk("u1", protocol.TextBlock{Text: "$ ls\na\nb\n"})},
		},
		{
			name: "reminder is skipped",
			next: userMsg(message.TextContent{Text: wrapped}, finish),
			want: nil,
		},
		{
			name: "reminder text with an attachment is real user input",
			next: userMsg(
				message.TextContent{Text: wrapped},
				message.BinaryContent{MIMEType: "image/png", Data: png},
			),
			want: []protocol.SessionUpdate{
				userChunk("u1", protocol.TextBlock{Text: wrapped}),
				userChunk("u1", protocol.ImageBlock{MIMEType: "image/png", Data: png}),
			},
		},
		{
			name: "already translated message is not repeated",
			prev: userMsg(message.TextContent{Text: "fix it"}),
			next: userMsg(message.TextContent{Text: "fix it"}, finish),
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, Diff(tt.prev, tt.next, DiffContext{}))
			require.Empty(t, Diff(tt.next, tt.next, DiffContext{}))
		})
	}
}

func TestIndexToolCalls(t *testing.T) {
	t.Parallel()

	announced := message.ToolCall{ID: "c1", Name: toolnames.Edit}
	finished := message.ToolCall{ID: "c1", Name: toolnames.Edit, Input: "{}", Finished: true}
	other := message.ToolCall{ID: "c2", Name: toolnames.Bash}
	lookup := indexToolCalls([]message.Message{
		{ID: "u1", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "hi"}}},
		assistantMsg("a1", announced, other),
		assistantMsg("a1", finished),
		toolMsg("t1", message.ToolResult{ToolCallID: "c1"}),
	})

	got, ok := lookup("c1")
	require.True(t, ok)
	require.Equal(t, finished, got, "the latest snapshot of a call wins")

	got, ok = lookup("c2")
	require.True(t, ok)
	require.Equal(t, other, got)

	_, ok = lookup("missing")
	require.False(t, ok)

	_, ok = indexToolCalls(nil)("c1")
	require.False(t, ok)
}

func TestDiffReplay(t *testing.T) {
	t.Parallel()

	png := []byte{0x89, 'P', 'N', 'G'}
	editPath := filepath.Join(testCwd, "a.go")
	editInput := mustJSON(t, tools.EditParams{FilePath: "a.go", OldString: "a", NewString: "b"})
	editCall := message.ToolCall{ID: "c1", Name: toolnames.Edit, Input: editInput, Finished: true}
	imageResult := message.ToolResult{
		ToolCallID: "c2", Name: toolnames.Read, Content: "Loaded image/png content",
		Data: base64.StdEncoding.EncodeToString(png), MIMEType: "image/png",
	}
	finish := message.Finish{Reason: message.FinishReasonEndTurn}

	history := []message.Message{
		{ID: "u1", Role: message.User, Parts: []message.ContentPart{
			message.TextContent{Text: "fix it"},
			message.BinaryContent{Path: "a.png", MIMEType: "image/png", Data: png},
			message.ShellCommand{Command: "ls", Output: "a.go\n"},
			finish,
		}},
		{ID: "r1", Role: message.User, Parts: []message.ContentPart{
			message.TextContent{Text: reminder.Wrap("todos changed")},
			finish,
		}},
		assistantMsg("a1",
			message.ReasoningContent{Thinking: "look first"},
			message.TextContent{Text: "editing"},
			editCall,
			message.ToolCall{ID: "c2", Name: toolnames.Read, Input: `{"file_path":"img.png"}`, Finished: true},
			message.Finish{Reason: message.FinishReasonToolUse},
		),
		toolMsg("t1",
			message.ToolResult{
				ToolCallID: "c1", Name: toolnames.Edit, Content: "edited",
				Metadata: mustJSON(t, tools.EditResponseMetadata{OldContent: "a", NewContent: "b"}),
			},
			imageResult,
		),
		{ID: "s1", Role: message.Assistant, IsSummaryMessage: true, Parts: []message.ContentPart{
			message.TextContent{Text: "summary of the work"},
			finish,
		}},
		{ID: "sys1", Role: message.System, Parts: []message.ContentPart{
			message.TextContent{Text: "model changed"},
		}},
	}

	dc := DiffContext{Cwd: testCwd, ToolCall: indexToolCalls(history)}
	var got []protocol.SessionUpdate
	for _, m := range history {
		got = append(got, Diff(message.Message{}, m, dc)...)
	}

	require.Equal(t, []protocol.SessionUpdate{
		userChunk("u1", protocol.TextBlock{Text: "fix it"}),
		userChunk("u1", protocol.ImageBlock{MIMEType: "image/png", Data: png}),
		userChunk("u1", protocol.TextBlock{Text: "$ ls\na.go\n"}),
		thought("a1", "look first"),
		answer("a1", "editing"),
		protocol.ToolCallStart{
			ID: "c1", Name: toolnames.Edit, Title: "Edit a.go", Kind: protocol.ToolKindEdit,
			Status:    protocol.ToolCallStatusPending,
			Locations: []protocol.Location{{Path: editPath}},
			RawInput:  json.RawMessage(editInput),
		},
		protocol.ToolCallStart{
			ID: "c2", Name: toolnames.Read, Title: "Read img.png", Kind: protocol.ToolKindRead,
			Status:    protocol.ToolCallStatusPending,
			Locations: []protocol.Location{{Path: filepath.Join(testCwd, "img.png")}},
			RawInput:  json.RawMessage(`{"file_path":"img.png"}`),
		},
		protocol.ToolCallUpdate{
			ID:      "c1",
			Status:  new(protocol.ToolCallStatusCompleted),
			Content: []protocol.ToolCallContent{protocol.ToolDiff{Path: editPath, OldText: new("a"), NewText: "b"}},
		},
		protocol.ToolCallUpdate{
			ID:     "c2",
			Status: new(protocol.ToolCallStatusCompleted),
			Content: []protocol.ToolCallContent{
				protocol.ToolContent{Block: protocol.ImageBlock{MIMEType: "image/png", Data: png}},
			},
		},
		thought("s1", "summary of the work"),
	}, got)
}
