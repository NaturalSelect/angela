package agent

import (
	"context"
	"sync"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/reminder"
)

// editorSequence answers editorContext with one state per call, repeating
// the last once they run out, so a test can say what the user's window
// showed at each point a turn asks.
type editorSequence struct {
	mu     sync.Mutex
	states []reminder.EditorContext
	calls  int
}

func (s *editorSequence) next() (reminder.EditorContext, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.states[min(s.calls, len(s.states)-1)]
	s.calls++
	return state, true
}

func editorOn(file string) reminder.EditorContext {
	return reminder.EditorContext{
		ActiveFile: file,
		Selection:  &reminder.EditorSelection{StartLine: 3, EndLine: 4, Text: "selected in " + file},
	}
}

// editorNoticeTexts lists the editor notices stored in a session, oldest first.
func editorNoticeTexts(t *testing.T, e *noticeAgentEnv, sessionID string) []string {
	t.Helper()
	msgs, err := e.env.messages.List(t.Context(), sessionID)
	require.NoError(t, err)
	var notices []string
	for _, m := range msgs {
		if notice, ok := reminder.PersistedEditorNotice(m.Content().Text); ok && m.IsReminder() {
			notices = append(notices, notice)
		}
	}
	return notices
}

func newEditorAgentEnv(t *testing.T, editor func() (reminder.EditorContext, bool)) *noticeAgentEnv {
	t.Helper()
	e := newNoticeAgentEnv(t)
	e.sa.editorContext = editor
	return e
}

func runEditorTurn(t *testing.T, e *noticeAgentEnv, model fantasy.LanguageModel, call SessionAgentCall) {
	t.Helper()
	call.Agent = largeWindowAgent(model)
	_, err := e.sa.Run(t.Context(), call)
	require.NoError(t, err)
}

func TestRun_EditorNoticeSitsAheadOfThePrompt(t *testing.T) {
	t.Parallel()

	editor := &editorSequence{states: []reminder.EditorContext{editorOn("a.go")}}
	e := newEditorAgentEnv(t, editor.next)
	sess, err := e.env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	var sent fantasy.Call
	model := newMockLanguageModel(t)
	model.EXPECT().Stream(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
			sent = call
			return streamOf([]string{"ok"}, fantasy.FinishReasonStop), nil
		})

	runEditorTurn(t, e, model, SessionAgentCall{SessionID: sess.ID, Prompt: "fix this"})

	notice := reminder.EditorNotice(editorOn("a.go"), "")
	require.Equal(t, []string{notice}, editorNoticeTexts(t, e, sess.ID))
	require.Equal(t, []string{reminder.Wrap(notice), "fix this"}, userTexts(sent.Prompt),
		"the notice reaches the model ahead of the user's own words")
}

func TestRun_EditorNoticeIsNotRepeatedUntilTheEditorChanges(t *testing.T) {
	t.Parallel()

	editor := &editorSequence{states: []reminder.EditorContext{editorOn("a.go"), editorOn("a.go"), editorOn("b.go")}}
	e := newEditorAgentEnv(t, editor.next)
	sess, err := e.env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	model := newMockLanguageModel(t)
	model.EXPECT().Stream(gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, fantasy.Call) (fantasy.StreamResponse, error) {
			return streamOf([]string{"ok"}, fantasy.FinishReasonStop), nil
		}).Times(3)

	for _, prompt := range []string{"one", "two", "three"} {
		runEditorTurn(t, e, model, SessionAgentCall{SessionID: sess.ID, Prompt: prompt})
	}

	require.Equal(t, []string{
		reminder.EditorNotice(editorOn("a.go"), ""),
		reminder.EditorNotice(editorOn("b.go"), ""),
	}, editorNoticeTexts(t, e, sess.ID), "the second turn saw the same editor, so only the third added a notice")
}

func TestRun_EditorNoticeIsSentAgainAfterCompaction(t *testing.T) {
	t.Parallel()

	editor := &editorSequence{states: []reminder.EditorContext{editorOn("a.go")}}
	e := newEditorAgentEnv(t, editor.next)
	sess, err := e.env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	model := newMockLanguageModel(t)
	model.EXPECT().Stream(gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, fantasy.Call) (fantasy.StreamResponse, error) {
			return streamOf([]string{"ok"}, fantasy.FinishReasonStop), nil
		}).Times(2)

	runEditorTurn(t, e, model, SessionAgentCall{SessionID: sess.ID, Prompt: "before"})

	summary, err := e.env.messages.Create(t.Context(), sess.ID, message.CreateMessageParams{
		Role:             message.Assistant,
		Parts:            []message.ContentPart{message.TextContent{Text: "the summary"}},
		IsSummaryMessage: true,
	})
	require.NoError(t, err)
	sess.SummaryMessageID = summary.ID
	_, err = e.env.sessions.Save(t.Context(), sess)
	require.NoError(t, err)

	runEditorTurn(t, e, model, SessionAgentCall{SessionID: sess.ID, Prompt: "after"})

	notice := reminder.EditorNotice(editorOn("a.go"), "")
	require.Equal(t, []string{notice, notice}, editorNoticeTexts(t, e, sess.ID),
		"the summary dropped the first notice from the model's view, so the same editor is stated again")
}

func TestRun_EditorNoticeIsLeftOutWhenTheUserDidNotStartTheTurn(t *testing.T) {
	t.Parallel()

	t.Run("a wake for background agent notices", func(t *testing.T) {
		t.Parallel()

		editor := &editorSequence{states: []reminder.EditorContext{editorOn("a.go")}}
		e := newEditorAgentEnv(t, editor.next)
		sess, err := e.env.sessions.Create(t.Context(), "session")
		require.NoError(t, err)
		e.inbox.Post(sess.ID, "<agent-notification>one</agent-notification>")

		model := newMockLanguageModel(t)
		model.EXPECT().Stream(gomock.Any(), gomock.Any()).
			Return(streamOf([]string{"noted"}, fantasy.FinishReasonStop), nil)

		runEditorTurn(t, e, model, SessionAgentCall{SessionID: sess.ID, noticeWake: true})

		require.Empty(t, editorNoticeTexts(t, e, sess.ID))
		require.Zero(t, editor.calls, "the editor is not even consulted")
	})

	t.Run("the continuation after a compaction", func(t *testing.T) {
		t.Parallel()

		editor := &editorSequence{states: []reminder.EditorContext{editorOn("a.go")}}
		e := newEditorAgentEnv(t, editor.next)
		sess, err := e.env.sessions.Create(t.Context(), "session")
		require.NoError(t, err)

		model := newMockLanguageModel(t)
		model.EXPECT().Stream(gomock.Any(), gomock.Any()).
			Return(streamOf([]string{"ok"}, fantasy.FinishReasonStop), nil)

		runEditorTurn(t, e, model, SessionAgentCall{SessionID: sess.ID, Prompt: "continue", internal: true})

		require.Empty(t, editorNoticeTexts(t, e, sess.ID))
	})
}

func TestRun_EditorNoticeIsLeftOutWithoutAWindow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		editor func() (reminder.EditorContext, bool)
	}{
		{"the feature is off", nil},
		{"no window is connected", func() (reminder.EditorContext, bool) { return reminder.EditorContext{}, false }},
		{"no file is open", func() (reminder.EditorContext, bool) { return reminder.EditorContext{}, true }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := newEditorAgentEnv(t, tt.editor)
			sess, err := e.env.sessions.Create(t.Context(), "session")
			require.NoError(t, err)

			model := newMockLanguageModel(t)
			model.EXPECT().Stream(gomock.Any(), gomock.Any()).
				Return(streamOf([]string{"ok"}, fantasy.FinishReasonStop), nil)

			runEditorTurn(t, e, model, SessionAgentCall{SessionID: sess.ID, Prompt: "hello"})

			require.Empty(t, editorNoticeTexts(t, e, sess.ID))
		})
	}
}

func TestRun_QueuedPromptFoldedIntoATurnCarriesTheCurrentEditor(t *testing.T) {
	t.Parallel()

	editor := &editorSequence{states: []reminder.EditorContext{editorOn("a.go"), editorOn("b.go")}}
	e := newEditorAgentEnv(t, editor.next)
	sess, err := e.env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	e.sa.enqueueCall(SessionAgentCall{SessionID: sess.ID, Prompt: "queued-followup"})

	var steps []fantasy.Call
	model := newMockLanguageModel(t)
	model.EXPECT().Stream(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
			steps = append(steps, call)
			return streamOf([]string{"done"}, fantasy.FinishReasonStop), nil
		})

	runEditorTurn(t, e, model, SessionAgentCall{SessionID: sess.ID, Prompt: "go"})

	require.Len(t, steps, 1)
	require.Equal(t, []string{
		reminder.Wrap(reminder.EditorNotice(editorOn("a.go"), "")),
		"go",
		reminder.Wrap(foldedPromptStatement),
		reminder.Wrap(reminder.EditorNotice(editorOn("b.go"), "")),
		"queued-followup",
	}, userTexts(steps[0].Prompt), "the queued prompt is introduced with what the user's window showed when it was folded in")
}

func TestRun_QueuedPromptFoldedIntoATurnSkipsAnUnchangedEditor(t *testing.T) {
	t.Parallel()

	editor := &editorSequence{states: []reminder.EditorContext{editorOn("a.go")}}
	e := newEditorAgentEnv(t, editor.next)
	sess, err := e.env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	e.sa.enqueueCall(SessionAgentCall{SessionID: sess.ID, Prompt: "queued-followup"})

	type noParams struct{}
	step := tools.NewTool("some_tool", "does nothing", func(_ context.Context, _ noParams, _ fantasy.ToolCall) tools.Result {
		return tools.Ok("tool done")
	})
	model := newMockLanguageModel(t)
	gomock.InOrder(
		model.EXPECT().Stream(gomock.Any(), gomock.Any()).Return(toolCallStream("call-1"), nil),
		model.EXPECT().Stream(gomock.Any(), gomock.Any()).Return(streamOf([]string{"done"}, fantasy.FinishReasonStop), nil),
	)
	agent := largeWindowAgent(model)
	agent.Tools = []fantasy.AgentTool{step}

	_, err = e.sa.Run(t.Context(), SessionAgentCall{Agent: agent, SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)

	require.Len(t, editorNoticeTexts(t, e, sess.ID), 1, "the editor did not change between the start of the turn and the fold")
}

func TestBuildAgentGivesTheEditorOnlyToPrimaryExecutors(t *testing.T) {
	t.Parallel()

	coord := newModelPrefTestCoordinator(t, nil)
	coord.editorContext = (&editorSequence{states: []reminder.EditorContext{editorOn("a.go")}}).next

	require.NotNil(t, coord.buildAgent(config.AgentCoder, false).(*sessionAgent).editorContext)
	require.Nil(t, coord.buildAgent(config.AgentExplore, true).(*sessionAgent).editorContext,
		"a subagent's prompt is written by a model, so the user's editor says nothing about it")
}

func TestLastEditorNotice(t *testing.T) {
	t.Parallel()

	noticeA := reminder.EditorNotice(editorOn("a.go"), "")
	noticeB := reminder.EditorNotice(editorOn("b.go"), "")
	userMsg := func(text string) message.Message {
		return message.Message{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: text}}}
	}

	tests := []struct {
		name string
		msgs []message.Message
		want string
	}{
		{"an empty conversation", nil, ""},
		{"no editor notice", []message.Message{userMsg(reminder.Wrap("something else")), userMsg("hello")}, ""},
		{
			"the newest notice wins",
			[]message.Message{userMsg(reminder.Wrap(noticeA)), userMsg("hello"), userMsg(reminder.Wrap(noticeB)), userMsg("again")},
			noticeB,
		},
		{"text the user typed is not a notice", []message.Message{userMsg(noticeA)}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, lastEditorNotice(tt.msgs))
		})
	}
}
