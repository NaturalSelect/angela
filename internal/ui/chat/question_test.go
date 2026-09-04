package chat

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// -----------------------------------------------------------------------------
// Question Tool renderer
// -----------------------------------------------------------------------------

func TestQuestionToolPending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "q1", Name: toolnames.Question, Input: `{"questions":[{"type":"yes_no","question":"Continue?"}]}`}
	item := NewQuestionToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, toolnames.Question)
}

func TestQuestionToolInvalidParams(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "q1", Name: toolnames.Question, Input: `not-json`, Finished: true}
	item := NewQuestionToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Invalid parameters")
}

func TestQuestionToolHeaderShowsSingleQuestion(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{
		ID:       "q1",
		Name:     toolnames.Question,
		Input:    `{"questions":[{"type":"yes_no","question":"Should we deploy?"}]}`,
		Finished: true,
	}
	item := NewQuestionToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Should we deploy?")
}

func TestQuestionToolHeaderShowsMoreCountForBatch(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{
		ID:   "q1",
		Name: toolnames.Question,
		Input: `{"questions":[{"type":"yes_no","question":"Deploy now?"},` +
			`{"type":"free_text","question":"Any notes?"}]}`,
		Finished: true,
	}
	item := NewQuestionToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "+1 more")
}

func TestQuestionToolCompactShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "q1", Name: toolnames.Question, Input: `{"questions":[{"type":"yes_no","question":"Continue?"}]}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "q1", Content: "Q1: Continue?\nUser answered: yes"}
	item := NewQuestionToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok, "tool items must implement Compactable")
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "Yes")
}

func TestQuestionToolEmptyResultShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "q1", Name: toolnames.Question, Input: `{"questions":[{"type":"yes_no","question":"Continue?"}]}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "q1", Content: ""}
	item := NewQuestionToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, toolnames.Question)
}

func TestQuestionToolRendersAnswersAndNotes(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "q1", Name: toolnames.Question, Input: `{"questions":[{"type":"yes_no","question":"Continue?"}]}`, Finished: true}
	result := &message.ToolResult{
		ToolCallID: "q1",
		Content:    "Q1: Continue?\nUser answered: yes\n\nNotes:\n- keep going\n- watch the logs",
	}
	item := NewQuestionToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Continue?")
	require.Contains(t, out, "Yes")
	require.Contains(t, out, "keep going")
	require.Contains(t, out, "watch the logs")
}

// -----------------------------------------------------------------------------
// questionSummary
// -----------------------------------------------------------------------------

func TestQuestionSummary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		params   tools.QuestionParams
		expected string
	}{
		{
			name:     "no questions",
			params:   tools.QuestionParams{},
			expected: "",
		},
		{
			name: "single question truncates at 60",
			params: tools.QuestionParams{Questions: []tools.QuestionItem{
				{Question: "short question"},
			}},
			expected: "short question",
		},
		{
			name: "multiple questions shows count",
			params: tools.QuestionParams{Questions: []tools.QuestionItem{
				{Question: "first question"},
				{Question: "second question"},
				{Question: "third question"},
			}},
			expected: "first question (+2 more)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.expected, questionSummary(tt.params))
		})
	}
}

// -----------------------------------------------------------------------------
// parseQuestionBlocks / formatQuestionAnswers / styleAnswer
// -----------------------------------------------------------------------------

func TestParseQuestionBlocksSingleQuestion(t *testing.T) {
	t.Parallel()

	blocks := parseQuestionBlocks("Q1: Continue?\nUser answered: yes")
	require.Len(t, blocks, 1)
	require.Equal(t, "Continue?", blocks[0].question)
	require.Equal(t, "User answered: yes", blocks[0].answer)
	require.Empty(t, blocks[0].notes)
}

func TestParseQuestionBlocksMultipleQuestionsWithNotes(t *testing.T) {
	t.Parallel()

	content := "Q1: Deploy now?\nUser answered: yes\n\nNotes:\n- double check staging\n\n" +
		"Q2: Any concerns?\nUser provided: none"
	blocks := parseQuestionBlocks(content)
	require.Len(t, blocks, 2)

	require.Equal(t, "Deploy now?", blocks[0].question)
	require.Equal(t, "User answered: yes", blocks[0].answer)
	require.Equal(t, []string{"double check staging"}, blocks[0].notes)

	require.Equal(t, "Any concerns?", blocks[1].question)
	require.Equal(t, "User provided: none", blocks[1].answer)
	require.Empty(t, blocks[1].notes)
}

func TestParseQuestionBlocksEmptyContent(t *testing.T) {
	t.Parallel()

	require.Empty(t, parseQuestionBlocks(""))
}

func TestFormatQuestionAnswersEmpty(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	require.Empty(t, formatQuestionAnswers(&sty, "", 80))
}

func TestStyleAnswerLineVariants(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	tests := []struct {
		name     string
		answer   string
		expected string
	}{
		{name: "yes", answer: "User answered: yes", expected: "Yes"},
		{name: "no", answer: "User answered: no", expected: "No"},
		{name: "selected", answer: `User selected: ["a","b"]`, expected: "a, b"},
		{name: "provided", answer: "User provided: some text", expected: "some text"},
		{name: "skipped", answer: "User skipped this question", expected: "Skipped"},
		{name: "default passthrough", answer: "unrecognized answer", expected: "unrecognized answer"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out := ansi.Strip(styleAnswerLine(&sty, tt.answer))
			require.Equal(t, tt.expected, out)
		})
	}
}

func TestStyleAnswerJoinsMultipleLines(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	out := ansi.Strip(styleAnswer(&sty, "User selected: [\"a\"]\nUser provided: extra note"))
	require.Contains(t, out, "a")
	require.Contains(t, out, "extra note")
}
