package backend

import (
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/NaturalSelect/angela/internal/question"
	"github.com/stretchr/testify/require"
)

func TestBackendQuestion_WorkspaceNotFound(t *testing.T) {
	t.Parallel()
	b, _ := newTestBackend(t)

	_, err := b.AnswerQuestion("nope", proto.QuestionAnswer{})
	require.ErrorIs(t, err, ErrWorkspaceNotFound)

	_, err = b.CancelQuestion("nope")
	require.ErrorIs(t, err, ErrWorkspaceNotFound)
}

func TestBackendQuestion_NoPendingQuestion(t *testing.T) {
	b, ws, _ := newPublishingWorkspace(t)

	resolved, err := b.AnswerQuestion(ws.ID, proto.QuestionAnswer{})
	require.NoError(t, err)
	require.False(t, resolved)

	resolved, err = b.CancelQuestion(ws.ID)
	require.NoError(t, err)
	require.False(t, resolved)
}

// TestBackendQuestion_AnswerResolvesPending drives a real pending
// question.Service.Ask call (the same way internal/question's own tests
// synchronize) through Backend.AnswerQuestion, proving the response
// mapping actually unblocks the waiting Ask call with the chosen
// answer.
func TestBackendQuestion_AnswerResolvesPending(t *testing.T) {
	b, ws, _ := newPublishingWorkspace(t)

	sub := ws.Questions.Subscribe(t.Context())
	type askResult struct {
		answers []question.Answer
		err     error
	}
	resultCh := make(chan askResult, 1)
	go func() {
		answers, err := ws.Questions.Ask(t.Context(), question.Request{
			Questions: []question.Question{
				{Type: question.TypeYesNo, Text: "Proceed?", Description: "Confirm"},
			},
		})
		resultCh <- askResult{answers, err}
	}()

	var published question.Request
	select {
	case ev := <-sub:
		published = ev.Payload
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for published question")
	}
	require.Len(t, published.Questions, 1)

	yes := true
	resolved, err := b.AnswerQuestion(ws.ID, proto.QuestionAnswer{
		BatchRequestID: published.ID,
		Responses: []proto.QuestionResponse{
			{QuestionID: published.Questions[0].ID, Yes: &yes},
		},
	})
	require.NoError(t, err)
	require.True(t, resolved)

	select {
	case res := <-resultCh:
		require.NoError(t, res.err)
		require.Len(t, res.answers, 1)
		require.Equal(t, published.Questions[0].ID, res.answers[0].QuestionID)
		require.NotNil(t, res.answers[0].Yes)
		require.True(t, *res.answers[0].Yes)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Ask to return")
	}
}

// TestBackendQuestion_CancelResolvesPending proves CancelQuestion
// actually unblocks a real pending Ask call, rather than just returning
// true.
func TestBackendQuestion_CancelResolvesPending(t *testing.T) {
	b, ws, _ := newPublishingWorkspace(t)

	sub := ws.Questions.Subscribe(t.Context())
	resultCh := make(chan error, 1)
	go func() {
		_, err := ws.Questions.Ask(t.Context(), question.Request{
			Questions: []question.Question{
				{Type: question.TypeFreeText, Text: "Name?", Description: "Give a name"},
			},
		})
		resultCh <- err
	}()

	select {
	case <-sub:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for published question")
	}

	resolved, err := b.CancelQuestion(ws.ID)
	require.NoError(t, err)
	require.True(t, resolved)

	select {
	case err := <-resultCh:
		require.Error(t, err, "a cancelled Ask must return an error")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Ask to return")
	}
}
