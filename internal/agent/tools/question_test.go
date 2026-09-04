package tools

import (
	"encoding/json"
	"errors"
	"testing"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/question"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestQuestionParamsUnmarshalJSON_NativeArray(t *testing.T) {
	t.Parallel()
	input := `{"questions": [{"type": "yes_no", "question": "OK?", "description": "test"}]}`
	var p QuestionParams
	require.NoError(t, json.Unmarshal([]byte(input), &p))
	require.Len(t, p.Questions, 1)
	require.Equal(t, "OK?", p.Questions[0].Question)
}

func TestQuestionParamsUnmarshalJSON_StringEncodedArray(t *testing.T) {
	t.Parallel()
	// Simulates a model that double-serializes the questions field.
	inner := `[{"type":"yes_no","question":"OK?","description":"test"}]`
	encoded, _ := json.Marshal(inner)
	input := `{"questions": ` + string(encoded) + `}`
	var p QuestionParams
	require.NoError(t, json.Unmarshal([]byte(input), &p))
	require.Len(t, p.Questions, 1)
	require.Equal(t, "OK?", p.Questions[0].Question)
}

func TestQuestionParamsUnmarshalJSON_StringEncodedWithWhitespace(t *testing.T) {
	t.Parallel()
	inner := `  [{"type":"single_choice","question":"Pick","description":"d","choices":[{"id":"a","label":"A"}]}]  `
	encoded, _ := json.Marshal(inner)
	input := `{"questions": ` + string(encoded) + `, "confirm_title": "Go?"}`
	var p QuestionParams
	require.NoError(t, json.Unmarshal([]byte(input), &p))
	require.Len(t, p.Questions, 1)
	require.Equal(t, "Pick", p.Questions[0].Question)
	require.Equal(t, "Go?", p.ConfirmTitle)
}

func TestQuestionParamsUnmarshalJSON_InvalidString(t *testing.T) {
	t.Parallel()
	encoded, _ := json.Marshal("not valid json")
	input := `{"questions": ` + string(encoded) + `}`
	var p QuestionParams
	require.Error(t, json.Unmarshal([]byte(input), &p))
}

func TestFormatAnswer_MultiChoiceWithFillIn(t *testing.T) {
	answer := question.Answer{
		SelectedIDs: []string{"speed", "readability"},
		FillInText:  "maintainability",
	}
	resp, err := formatAnswer(&answer, question.TypeMultiChoice)
	require.NoError(t, err)
	require.Contains(t, resp.Content, `User selected: ["speed","readability"]`)
	require.Contains(t, resp.Content, "User provided: maintainability")
}

func TestFormatAnswer_SelectionsOnly(t *testing.T) {
	answer := question.Answer{SelectedIDs: []string{"gardening"}}
	resp, err := formatAnswer(&answer, question.TypeSingleChoice)
	require.NoError(t, err)
	require.Contains(t, resp.Content, `User selected: ["gardening"]`)
	require.NotContains(t, resp.Content, "User provided")
}

func TestFormatAnswer_Skipped(t *testing.T) {
	answer := question.Answer{}
	resp, err := formatAnswer(&answer, question.TypeFreeText)
	require.NoError(t, err)
	require.Equal(t, "User skipped this question", resp.Content)
}

func TestQuestionItem_GetChoices(t *testing.T) {
	t.Parallel()

	choices := []QuestionChoice{{ID: "a", Label: "A"}}
	options := []QuestionChoice{{ID: "b", Label: "B"}}

	t.Run("prefers Choices when both are set", func(t *testing.T) {
		t.Parallel()
		item := QuestionItem{Choices: choices, Options: options}
		require.Equal(t, choices, item.GetChoices())
	})

	t.Run("falls back to Options", func(t *testing.T) {
		t.Parallel()
		item := QuestionItem{Options: options}
		require.Equal(t, options, item.GetChoices())
	})

	t.Run("empty when neither is set", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, QuestionItem{}.GetChoices())
	})
}

func TestConvertChoices(t *testing.T) {
	t.Parallel()

	in := []QuestionChoice{
		{ID: "a", Label: "A", Description: "first"},
		{ID: "b", Label: "B"},
	}

	got := convertChoices(in)

	require.Equal(t, []question.Choice{
		{ID: "a", Label: "A", Description: "first"},
		{ID: "b", Label: "B"},
	}, got)
}

func TestConvertChoices_Empty(t *testing.T) {
	t.Parallel()
	require.Empty(t, convertChoices(nil))
}

func TestFormatAnswer_Yes(t *testing.T) {
	t.Parallel()

	yes := true
	answer := question.Answer{Yes: &yes}
	resp, err := formatAnswer(&answer, question.TypeYesNo)
	require.NoError(t, err)
	require.Equal(t, "User answered: yes", resp.Content)
}

func TestFormatAnswer_No(t *testing.T) {
	t.Parallel()

	no := false
	answer := question.Answer{Yes: &no}
	resp, err := formatAnswer(&answer, question.TypeYesNo)
	require.NoError(t, err)
	require.Equal(t, "User answered: no", resp.Content)
}

// TestFormatAnswer_WithNotes pins that the "_question" note key is
// rendered as a plain bullet (it restates the question, not a
// per-choice annotation) while every other key keeps its bracketed
// label.
func TestFormatAnswer_WithNotes(t *testing.T) {
	t.Parallel()

	answer := question.Answer{
		FillInText: "because",
		Notes: map[string]string{
			"_question": "why?",
			"context":   "extra detail",
		},
	}
	resp, err := formatAnswer(&answer, question.TypeFreeText)
	require.NoError(t, err)
	require.Contains(t, resp.Content, "User provided: because")
	require.Contains(t, resp.Content, "Notes:")
	require.Contains(t, resp.Content, "\n- why?")
	require.Contains(t, resp.Content, "\n- [context]: extra detail")
}

// TestFormatAnswers_MultipleQuestions pins that each answer is
// paired with its own question text under a 1-based "Qn:" header.
func TestFormatAnswers_MultipleQuestions(t *testing.T) {
	t.Parallel()

	questions := []question.Question{
		{Text: "First?"},
		{Text: "Second?"},
	}
	yes := true
	answers := []question.Answer{
		{Yes: &yes},
		{FillInText: "done"},
	}

	resp, err := formatAnswers(answers, questions)
	require.NoError(t, err)
	require.Contains(t, resp.Content, "Q1: First?")
	require.Contains(t, resp.Content, "User answered: yes")
	require.Contains(t, resp.Content, "Q2: Second?")
	require.Contains(t, resp.Content, "User provided: done")
}

// TestNewQuestionTool_RequiresAtLeastOneQuestion pins that an empty
// batch is rejected before the question service is ever consulted.
func TestNewQuestionTool_RequiresAtLeastOneQuestion(t *testing.T) {
	t.Parallel()

	tool := NewQuestionTool(NewMockQuestionService(gomock.NewController(t)))

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: `{"questions":[]}`})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "at least one question is required")
}

// TestNewQuestionTool_RejectsTooManyQuestions pins that a batch over
// the configured maximum is rejected before the question service is
// consulted, so the model gets an actionable "split it up" error
// instead of an oversized prompt reaching the user.
func TestNewQuestionTool_RejectsTooManyQuestions(t *testing.T) {
	t.Parallel()

	tool := NewQuestionTool(NewMockQuestionService(gomock.NewController(t)))

	items := make([]QuestionItem, question.MaxQuestions+1)
	for i := range items {
		items[i] = QuestionItem{Type: "yes_no", Question: "Q", Description: "d"}
	}
	input, err := json.Marshal(QuestionParams{Questions: items})
	require.NoError(t, err)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "exceeds maximum")
}

// TestNewQuestionTool_RejectsInvalidType pins that an unknown
// question type is rejected before the question service is
// consulted, naming the offending question by label or text.
func TestNewQuestionTool_RejectsInvalidType(t *testing.T) {
	t.Parallel()

	tool := NewQuestionTool(NewMockQuestionService(gomock.NewController(t)))

	input, err := json.Marshal(QuestionParams{Questions: []QuestionItem{
		{Type: "not-a-type", Question: "Ready?", Description: "d"},
	}})
	require.NoError(t, err)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "invalid type")
	require.Contains(t, resp.Content, "Ready?")
}

// TestNewQuestionTool_Success pins the happy path end to end: valid
// questions reach the service, and its answers come back formatted
// with a 1-based question header.
func TestNewQuestionTool_Success(t *testing.T) {
	t.Parallel()

	svc := NewMockQuestionService(gomock.NewController(t))
	yes := true
	svc.EXPECT().Ask(gomock.Any(), gomock.Any()).Return([]question.Answer{{Yes: &yes}}, nil)

	tool := NewQuestionTool(svc)
	input, err := json.Marshal(QuestionParams{Questions: []QuestionItem{
		{Type: "yes_no", Question: "Ready?", Description: "d"},
	}})
	require.NoError(t, err)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "Q1: Ready?")
	require.Contains(t, resp.Content, "User answered: yes")
}

// TestNewQuestionTool_Cancelled pins that a user cancellation ends
// the agent's turn (StopTurn) instead of leaving the model free to
// retry the same question.
func TestNewQuestionTool_Cancelled(t *testing.T) {
	t.Parallel()

	svc := NewMockQuestionService(gomock.NewController(t))
	svc.EXPECT().Ask(gomock.Any(), gomock.Any()).Return(nil, question.ErrCancelled)

	tool := NewQuestionTool(svc)
	input, err := json.Marshal(QuestionParams{Questions: []QuestionItem{
		{Type: "yes_no", Question: "Ready?", Description: "d"},
	}})
	require.NoError(t, err)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.True(t, resp.StopTurn)
	require.Equal(t, "User cancelled this question", resp.Content)
}

// TestNewQuestionTool_AskError pins that a non-cancellation failure
// from the service is surfaced verbatim as a tool error, without
// setting StopTurn (the model may still retry).
func TestNewQuestionTool_AskError(t *testing.T) {
	t.Parallel()

	svc := NewMockQuestionService(gomock.NewController(t))
	svc.EXPECT().Ask(gomock.Any(), gomock.Any()).Return(nil, errors.New("boom"))

	tool := NewQuestionTool(svc)
	input, err := json.Marshal(QuestionParams{Questions: []QuestionItem{
		{Type: "yes_no", Question: "Ready?", Description: "d"},
	}})
	require.NoError(t, err)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.False(t, resp.StopTurn)
	require.Contains(t, resp.Content, "boom")
}
