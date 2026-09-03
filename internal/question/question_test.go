package question

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func validQuestion() Question {
	return Question{Type: TypeYesNo, Text: "Proceed?", Description: "Should we proceed?"}
}

func TestRequest_Validate(t *testing.T) {
	t.Parallel()

	t.Run("requires at least one question", func(t *testing.T) {
		t.Parallel()
		err := Request{}.Validate()
		require.ErrorContains(t, err, "at least one question is required")
	})

	t.Run("rejects too many questions", func(t *testing.T) {
		t.Parallel()
		qs := make([]Question, MaxQuestions+1)
		for i := range qs {
			qs[i] = validQuestion()
		}
		err := Request{Questions: qs}.Validate()
		require.ErrorContains(t, err, "exceed maximum")
	})

	t.Run("wraps invalid question errors with position", func(t *testing.T) {
		t.Parallel()
		err := Request{Questions: []Question{validQuestion(), {}}}.Validate()
		require.ErrorContains(t, err, "question 2:")
	})

	t.Run("accepts a well-formed request", func(t *testing.T) {
		t.Parallel()
		err := Request{Questions: []Question{validQuestion()}}.Validate()
		require.NoError(t, err)
	})
}

func TestQuestion_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		q       Question
		wantErr string
	}{
		{
			name:    "empty text",
			q:       Question{Type: TypeYesNo, Description: "d"},
			wantErr: "question text is required",
		},
		{
			name:    "text too long",
			q:       Question{Type: TypeYesNo, Text: strings.Repeat("a", MaxQuestionLength+1), Description: "d"},
			wantErr: "exceeds 240 characters",
		},
		{
			name:    "empty description",
			q:       Question{Type: TypeYesNo, Text: "q"},
			wantErr: "description is required",
		},
		{
			name:    "description too long",
			q:       Question{Type: TypeYesNo, Text: "q", Description: strings.Repeat("a", MaxDescriptionLength+1)},
			wantErr: "exceeds 600 characters",
		},
		{
			name:    "free text needs no choices",
			q:       Question{Type: TypeFreeText, Text: "q", Description: "d"},
			wantErr: "",
		},
		{
			name:    "single choice needs at least two choices",
			q:       Question{Type: TypeSingleChoice, Text: "q", Description: "d", Choices: []Choice{{ID: "a", Label: "A"}}},
			wantErr: "requires at least 2 choices",
		},
		{
			name: "choices exceed maximum",
			q: Question{
				Type: TypeMultiChoice, Text: "q", Description: "d",
				Choices: []Choice{
					{ID: "1", Label: "1"},
					{ID: "2", Label: "2"},
					{ID: "3", Label: "3"},
					{ID: "4", Label: "4"},
					{ID: "5", Label: "5"},
					{ID: "6", Label: "6"},
				},
			},
			wantErr: "exceed maximum",
		},
		{
			name: "choice missing id",
			q: Question{
				Type: TypeSingleChoice, Text: "q", Description: "d",
				Choices: []Choice{{Label: "A"}, {ID: "b", Label: "B"}},
			},
			wantErr: `must have an "id" field`,
		},
		{
			name: "duplicate choice id",
			q: Question{
				Type: TypeSingleChoice, Text: "q", Description: "d",
				Choices: []Choice{{ID: "a", Label: "A"}, {ID: "a", Label: "A2"}},
			},
			wantErr: "duplicate id",
		},
		{
			name: "choice missing label",
			q: Question{
				Type: TypeSingleChoice, Text: "q", Description: "d",
				Choices: []Choice{{ID: "a"}, {ID: "b", Label: "B"}},
			},
			wantErr: `must have a "label" field`,
		},
		{
			name: "choice label too long",
			q: Question{
				Type: TypeSingleChoice, Text: "q", Description: "d",
				Choices: []Choice{{ID: "a", Label: strings.Repeat("a", MaxChoiceLabelLength+1)}, {ID: "b", Label: "B"}},
			},
			wantErr: "label exceeds",
		},
		{
			name: "choice description too long",
			q: Question{
				Type: TypeSingleChoice, Text: "q", Description: "d",
				Choices: []Choice{{ID: "a", Label: "A", Description: strings.Repeat("a", MaxChoiceDescriptionLength+1)}, {ID: "b", Label: "B"}},
			},
			wantErr: "description exceeds",
		},
		{
			name:    "valid single choice",
			q:       Question{Type: TypeSingleChoice, Text: "q", Description: "d", Choices: []Choice{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}}},
			wantErr: "",
		},
		{
			name:    "unknown type",
			q:       Question{Type: "bogus", Text: "q", Description: "d"},
			wantErr: "unknown type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.q.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestQuestion_identifier(t *testing.T) {
	t.Parallel()

	t.Run("uses label when present", func(t *testing.T) {
		t.Parallel()
		q := Question{Label: "confirm", Text: "irrelevant"}
		require.ErrorContains(t, q.Validate(), "[confirm]")
	})

	t.Run("falls back to text excerpt", func(t *testing.T) {
		t.Parallel()
		q := Question{Type: "bogus", Text: "short text", Description: "d"}
		require.ErrorContains(t, q.Validate(), "[short text]")
	})

	t.Run("truncates long text with ellipsis", func(t *testing.T) {
		t.Parallel()
		longText := strings.Repeat("a", 50)
		q := Question{Type: "bogus", Text: longText, Description: "d"}
		err := q.Validate()
		require.ErrorContains(t, err, "["+strings.Repeat("a", 40)+"…]")
	})

	t.Run("falls back to unnamed question", func(t *testing.T) {
		t.Parallel()
		q := Question{Type: "bogus", Description: "d"}
		// Text is empty, so Validate fails earlier on "text is required"
		// which itself uses the identifier — exercise identifier()
		// directly via a type that has neither label nor text but does
		// pass the text check by using free text with an empty label.
		require.ErrorContains(t, q.Validate(), "[unnamed question]")
	})
}

func TestAnswer_HasNotes(t *testing.T) {
	t.Parallel()

	require.False(t, Answer{}.HasNotes())
	require.True(t, Answer{Notes: map[string]string{"k": "v"}}.HasNotes())
}

func TestQuestionService_AskAnswerFlow(t *testing.T) {
	t.Parallel()

	s := NewService()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	sub := s.Subscribe(ctx)
	notifSub := s.SubscribeNotifications(ctx)

	req := Request{
		Questions: []Question{
			{Type: TypeYesNo, Text: "Proceed?", Description: "Should we proceed?"},
		},
	}

	type result struct {
		answers []Answer
		err     error
	}
	resultCh := make(chan result, 1)
	go func() {
		answers, err := s.Ask(ctx, req)
		resultCh <- result{answers, err}
	}()

	var published Request
	select {
	case ev := <-sub:
		published = ev.Payload
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for published request")
	}

	require.NotEmpty(t, published.ID)
	require.Len(t, published.Questions, 1)
	require.NotEmpty(t, published.Questions[0].ID)

	yes := true
	answers := []Answer{{QuestionID: published.Questions[0].ID, Yes: &yes}}
	require.True(t, s.Answer(answers))

	select {
	case res := <-resultCh:
		require.NoError(t, res.err)
		require.Equal(t, answers, res.answers)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Ask to return")
	}

	select {
	case ev := <-notifSub:
		require.Equal(t, published.ID, ev.Payload.BatchID)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for notification")
	}
}

func TestQuestionService_AskCancelledByUser(t *testing.T) {
	t.Parallel()

	s := NewService()
	ctx := t.Context()
	sub := s.Subscribe(ctx)

	resultCh := make(chan error, 1)
	go func() {
		_, err := s.Ask(ctx, Request{Questions: []Question{{Type: TypeFreeText, Text: "Q", Description: "D"}}})
		resultCh <- err
	}()

	<-sub // Wait until the question is published (and thus pending).

	require.True(t, s.Cancel())

	select {
	case err := <-resultCh:
		require.ErrorIs(t, err, ErrCancelled)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Ask to return")
	}
}

func TestQuestionService_AskContextCancelled(t *testing.T) {
	t.Parallel()

	s := NewService()
	ctx, cancel := context.WithCancel(t.Context())
	sub := s.Subscribe(t.Context())

	resultCh := make(chan error, 1)
	go func() {
		_, err := s.Ask(ctx, Request{Questions: []Question{{Type: TypeFreeText, Text: "Q", Description: "D"}}})
		resultCh <- err
	}()

	<-sub
	cancel()

	select {
	case err := <-resultCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Ask to return")
	}
}

func TestQuestionService_AskInvalidRequest(t *testing.T) {
	t.Parallel()

	s := NewService()
	_, err := s.Ask(t.Context(), Request{})
	require.Error(t, err)
}

func TestQuestionService_AskAppliesConfirmDefaults(t *testing.T) {
	t.Parallel()

	s := NewService()
	ctx := t.Context()
	sub := s.Subscribe(ctx)

	req := Request{
		Questions: []Question{
			{Type: TypeYesNo, Text: "Q1", Description: "D1"},
			{Type: TypeYesNo, Text: "Q2", Description: "D2"},
		},
	}

	go func() { _, _ = s.Ask(ctx, req) }()

	select {
	case ev := <-sub:
		require.Equal(t, "Ready to go?", ev.Payload.ConfirmTitle)
		require.Equal(t, "Review your answers above and confirm.", ev.Payload.ConfirmDescription)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for published request")
	}

	s.Cancel() // Unblock the pending Ask goroutine.
}

func TestQuestionService_CancelNoPending(t *testing.T) {
	t.Parallel()

	s := NewService()
	require.False(t, s.Cancel())
}

func TestQuestionService_AnswerNoPending(t *testing.T) {
	t.Parallel()

	s := NewService()
	require.False(t, s.Answer(nil))
}
