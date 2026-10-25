package acp

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/acp/protocol"
	"github.com/NaturalSelect/angela/internal/agent/notify"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/question"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

type editPreview struct{ path, before, after string }

func (e editPreview) DiffPreview() (string, string, string) { return e.path, e.before, e.after }

func (h *harness) elicitingSession(t *testing.T) string {
	t.Helper()
	_, err := h.agent.Initialize(t.Context(), protocol.InitializeRequest{ProtocolVersion: 1, ElicitationForm: true})
	require.NoError(t, err)
	return h.newSession(t)
}

func (h *harness) publishPermission(req permission.PermissionRequest) {
	h.ws.publish(pubsub.Event[permission.PermissionRequest]{Type: pubsub.CreatedEvent, Payload: req})
}

func (h *harness) publishQuestion(req question.Request) {
	h.ws.publish(pubsub.Event[question.Request]{Type: pubsub.CreatedEvent, Payload: req})
}

func TestPumpMessages(t *testing.T) {
	t.Parallel()

	t.Run("snapshots become deltas", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sid := h.newSession(t)

		for _, text := range []string{"he", "hello", "hello there"} {
			m := assistantMsg("a1", message.TextContent{Text: text})
			m.SessionID = sid
			h.publishMessage(m)
		}
		eventually(t, func() bool { return len(h.client.updates(sid)) == 3 })
		require.Equal(t, []protocol.SessionUpdate{answer("a1", "he"), answer("a1", "llo"), answer("a1", " there")}, h.client.updates(sid))
	})

	t.Run("user messages and deletions are not echoed", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sid := h.newSession(t)

		h.publishMessage(message.Message{ID: "u1", SessionID: sid, Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "hi"}}})
		deleted := assistantMsg("a0", message.TextContent{Text: "gone"})
		deleted.SessionID = sid
		h.ws.publish(pubsub.Event[message.Message]{Type: pubsub.DeletedEvent, Payload: deleted})

		sentinel := assistantMsg("a1", message.TextContent{Text: "ok"})
		sentinel.SessionID = sid
		h.publishMessage(sentinel)
		eventually(t, func() bool { return len(h.client.updates(sid)) == 1 })
		require.Equal(t, []protocol.SessionUpdate{answer("a1", "ok")}, h.client.updates(sid))
	})

	t.Run("sessions of other connections are ignored", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sid := h.newSession(t)

		stranger := assistantMsg("x1", message.TextContent{Text: "not mine"})
		stranger.SessionID = "stranger"
		h.publishMessage(stranger)
		mine := assistantMsg("a1", message.TextContent{Text: "mine"})
		mine.SessionID = sid
		h.publishMessage(mine)

		eventually(t, func() bool { return len(h.client.updates(sid)) == 1 })
		require.Empty(t, h.client.updates("stranger"))
	})
}

func TestPumpSubAgent(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	sid := h.newSession(t)
	h.ws.addSession(session.Session{ID: "child", ParentSessionID: sid})

	call := message.ToolCall{ID: "c1", Name: toolnames.Bash, Input: `{"command":"ls"}`, Finished: true}
	child := assistantMsg("k1", message.TextContent{Text: "private chatter"}, call)
	child.SessionID = "child"
	h.publishMessage(child)

	result := toolMsg("k2", message.ToolResult{ToolCallID: "c1", Name: toolnames.Bash, Content: "a.go"})
	result.SessionID = "child"
	h.publishMessage(result)

	eventually(t, func() bool { return len(h.client.updates(sid)) == 2 })
	updates := h.client.updates(sid)

	start, ok := updates[0].(protocol.ToolCallStart)
	require.True(t, ok, "the tool call is announced; the sub-agent's text is not")
	require.Equal(t, "child/c1", start.ID)

	done, ok := updates[1].(protocol.ToolCallUpdate)
	require.True(t, ok)
	require.Equal(t, "child/c1", done.ID)
	require.Empty(t, h.client.updates("child"))
}

func TestPumpSessionState(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.ws.active.CatwalkCfg.ContextWindow = 1000
	sid := h.newSession(t)

	publish := func(s session.Session) {
		h.ws.publish(pubsub.Event[session.Session]{Type: pubsub.UpdatedEvent, Payload: s})
	}
	titled := session.Session{ID: sid, Title: "First", PromptTokens: 10, CompletionTokens: 5, Cost: 0.5}
	publish(titled)
	publish(titled)
	publish(session.Session{ID: sid, Title: "Second", PromptTokens: 10, CompletionTokens: 5, Cost: 0.5})

	eventually(t, func() bool { return len(h.client.updates(sid)) == 4 })
	require.Equal(t, []protocol.SessionUpdate{
		protocol.SessionInfoUpdate{Title: new("First")},
		protocol.UsageUpdate{Used: 15, Size: 1000, Cost: &protocol.Cost{Amount: 0.5, Currency: "USD"}},
		protocol.Plan{Entries: []protocol.PlanEntry{}},
		protocol.SessionInfoUpdate{Title: new("Second")},
	}, h.client.updates(sid), "an unchanged state is not repeated")
}

func TestPumpNotices(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	idle, running := h.newSession(t), h.newSession(t)
	h.ws.run = func(ctx context.Context, sessionID, _ string, _ []message.Attachment) error {
		h.ws.publish(pubsub.Event[notify.Notification]{
			Type:    pubsub.CreatedEvent,
			Payload: notify.Notification{Type: notify.TypeReAuthenticate, ProviderID: "copilot"},
		})
		eventually(t, func() bool { return len(h.client.updates(sessionID)) == 1 })
		h.finishRun(ctx, sessionID, notify.RunComplete{})
		return nil
	}

	_, err := h.agent.Prompt(t.Context(), textPrompt(running, "go"))
	require.NoError(t, err)

	notice, ok := h.client.updates(running)[0].(protocol.AgentMessageChunk)
	require.True(t, ok)
	require.Contains(t, notice.Content.(protocol.TextBlock).Text, "angela login")
	require.Contains(t, notice.Content.(protocol.TextBlock).Text, "copilot")
	require.Empty(t, h.client.updates(idle), "only sessions with a running turn are told")
}

func TestPermissionRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		answer protocol.PermissionResponse
		want   func(t *testing.T, ws *fakeWorkspace)
	}{
		{
			name:   "allow once",
			answer: protocol.PermissionResponse{OptionID: permAllowOnce},
			want: func(t *testing.T, ws *fakeWorkspace) {
				require.Len(t, ws.granted, 1)
				require.Empty(t, ws.grantedAlways)
				require.Empty(t, ws.denied)
			},
		},
		{
			name:   "allow always",
			answer: protocol.PermissionResponse{OptionID: permAllowAlways},
			want: func(t *testing.T, ws *fakeWorkspace) {
				require.Len(t, ws.grantedAlways, 1)
				require.Empty(t, ws.granted)
			},
		},
		{
			name:   "reject",
			answer: protocol.PermissionResponse{OptionID: permReject},
			want:   func(t *testing.T, ws *fakeWorkspace) { require.Len(t, ws.denied, 1) },
		},
		{
			name:   "the editor dismisses the prompt",
			answer: protocol.PermissionResponse{Cancelled: true},
			want:   func(t *testing.T, ws *fakeWorkspace) { require.Len(t, ws.denied, 1) },
		},
		{
			name:   "an option nobody offered",
			answer: protocol.PermissionResponse{OptionID: "root_access"},
			want: func(t *testing.T, ws *fakeWorkspace) {
				require.Len(t, ws.denied, 1)
				require.Empty(t, ws.granted)
				require.Empty(t, ws.grantedAlways)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			sid := h.newSession(t)
			h.client.permission = func(protocol.PermissionRequest) (protocol.PermissionResponse, error) { return tt.answer, nil }

			h.publishPermission(permission.PermissionRequest{ID: "p1", SessionID: sid, ToolCallID: "c1", ToolName: toolnames.Bash})

			resolved := func() bool {
				h.ws.mu.Lock()
				defer h.ws.mu.Unlock()
				return len(h.ws.granted)+len(h.ws.grantedAlways)+len(h.ws.denied) == 1
			}
			eventually(t, resolved)
			h.ws.snapshot(func() { tt.want(t, h.ws) })

			requests := h.client.permissionRequests()
			require.Len(t, requests, 1)
			require.Equal(t, sid, requests[0].SessionID)
			require.Equal(t, "c1", requests[0].ToolCall.ID)
		})
	}
}

func TestPermissionRequestContents(t *testing.T) {
	t.Parallel()

	t.Run("an edit carries its diff", func(t *testing.T) {
		t.Parallel()
		req := permissionRequestFor("s1", "c1", permission.PermissionRequest{
			Params: editPreview{path: "main.go", before: "old", after: "new"},
		}, testCwd)
		require.Equal(t, []protocol.ToolCallContent{
			protocol.ToolDiff{Path: filepath.Join(testCwd, "main.go"), OldText: new("old"), NewText: "new"},
		}, req.ToolCall.Content)
	})

	t.Run("a new file has no old text", func(t *testing.T) {
		t.Parallel()
		req := permissionRequestFor("s1", "c1", permission.PermissionRequest{
			Params: editPreview{path: "/abs/new.go", after: "package x"},
		}, testCwd)
		require.Equal(t, []protocol.ToolCallContent{
			protocol.ToolDiff{Path: "/abs/new.go", NewText: "package x"},
		}, req.ToolCall.Content)
	})

	t.Run("other tools carry none", func(t *testing.T) {
		t.Parallel()
		req := permissionRequestFor("s1", "c1", permission.PermissionRequest{Params: map[string]string{"command": "ls"}}, testCwd)
		require.Empty(t, req.ToolCall.Content)
	})

	t.Run("every option has a distinct id and the three kinds Angela can honor", func(t *testing.T) {
		t.Parallel()
		req := permissionRequestFor("s1", "c1", permission.PermissionRequest{}, testCwd)
		kinds := map[string]protocol.PermissionKind{}
		for _, o := range req.Options {
			kinds[o.ID] = o.Kind
		}
		require.Equal(t, map[string]protocol.PermissionKind{
			permAllowOnce:   protocol.PermissionKindAllowOnce,
			permAllowAlways: protocol.PermissionKindAllowAlways,
			permReject:      protocol.PermissionKindRejectOnce,
		}, kinds)
	})
}

func TestPermissionForUnknownSessionIsDenied(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.newSession(t)
	h.publishPermission(permission.PermissionRequest{ID: "p1", SessionID: "stranger", ToolCallID: "c1"})

	eventually(t, func() bool {
		h.ws.mu.Lock()
		defer h.ws.mu.Unlock()
		return len(h.ws.denied) == 1
	})
	require.Empty(t, h.client.permissionRequests(), "the editor is not asked about someone else's session")
}

func TestPermissionBringsTheToolCallIntoView(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	sid := h.newSession(t)
	call := message.ToolCall{ID: "c1", Name: toolnames.Bash, Input: `{"command":"rm -rf x"}`, Finished: true}
	m := assistantMsg("a1", call)
	m.SessionID = sid
	h.ws.setMessages(sid, m)

	h.publishPermission(permission.PermissionRequest{ID: "p1", SessionID: sid, ToolCallID: "c1", ToolName: toolnames.Bash})
	eventually(t, func() bool { return len(h.client.permissionRequests()) == 1 })

	var order []string
	for _, e := range h.client.entries() {
		switch e.(type) {
		case protocol.SessionNotification:
			order = append(order, "update")
		case protocol.PermissionRequest:
			order = append(order, "permission")
		}
	}
	require.Equal(t, []string{"update", "permission"}, order, "the call is announced before the editor is asked about it")
}

func TestPermissionForSubAgentUsesPrefixedCall(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	sid := h.newSession(t)
	h.ws.addSession(session.Session{ID: "child", ParentSessionID: sid})
	h.client.permission = func(protocol.PermissionRequest) (protocol.PermissionResponse, error) {
		return protocol.PermissionResponse{OptionID: permAllowOnce}, nil
	}

	h.publishPermission(permission.PermissionRequest{ID: "p1", SessionID: "child", ToolCallID: "c9", ToolName: toolnames.Bash})
	eventually(t, func() bool { return len(h.client.permissionRequests()) == 1 })

	got := h.client.permissionRequests()[0]
	require.Equal(t, sid, got.SessionID)
	require.Equal(t, "child/c9", got.ToolCall.ID)
}

func choiceQuestion() question.Request {
	return question.Request{
		ID:                 "batch-1",
		ToolCallID:         "tc1",
		ConfirmTitle:       "Project setup",
		ConfirmDescription: "A few choices",
		Questions: []question.Question{
			{ID: "db", Type: question.TypeSingleChoice, Text: "Which database?", Choices: []question.Choice{{ID: "pg", Label: "Postgres"}, {ID: "my", Label: "MySQL"}}},
			{ID: "features", Type: question.TypeMultiChoice, Text: "Which features?", Choices: []question.Choice{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}}},
			{ID: "ci", Type: question.TypeYesNo, Text: "Add CI?"},
			{ID: "note", Type: question.TypeFreeText, Text: "Anything else?"},
		},
	}
}

func TestElicitationFor(t *testing.T) {
	t.Parallel()

	req := elicitationFor("s1", choiceQuestion())
	require.Equal(t, "s1", req.SessionID)
	require.Equal(t, "tc1", req.ToolCallID)
	require.Equal(t, "Project setup\n\nA few choices", req.Message)

	type field struct {
		name string
		kind protocol.FieldKind
	}
	var got []field
	for _, f := range req.Fields {
		got = append(got, field{f.Name, f.Kind})
	}
	require.Equal(t, []field{
		{"db", protocol.FieldKindSingleSelect},
		{"db__other", protocol.FieldKindText},
		{"features", protocol.FieldKindMultiSelect},
		{"features__other", protocol.FieldKindText},
		{"ci", protocol.FieldKindBoolean},
		{"note", protocol.FieldKindText},
	}, got)
	require.Equal(t, []protocol.FormOption{{Value: "pg", Title: "Postgres"}, {Value: "my", Title: "MySQL"}}, req.Fields[0].Options)

	for _, f := range req.Fields {
		require.False(t, f.Required, "%s: skipping a question is allowed", f.Name)
	}

	require.Equal(t, "Angela has a question", elicitationFor("s1", question.Request{}).Message)
}

func TestAnswersFrom(t *testing.T) {
	t.Parallel()

	yes := true
	tests := []struct {
		name   string
		values map[string]any
		want   []question.Answer
	}{
		{
			name:   "every kind of answer",
			values: map[string]any{"db": "pg", "features": []any{"a", "b"}, "ci": true, "note": "thanks"},
			want: []question.Answer{
				{QuestionID: "db", SelectedIDs: []string{"pg"}},
				{QuestionID: "features", SelectedIDs: []string{"a", "b"}},
				{QuestionID: "ci", Yes: &yes},
				{QuestionID: "note", FillInText: "thanks"},
			},
		},
		{
			name:   "writing your own beats the pick",
			values: map[string]any{"db": "pg", "db__other": "SQLite", "features": []any{"a"}, "features__other": "c"},
			want: []question.Answer{
				{QuestionID: "db", FillInText: "SQLite"},
				{QuestionID: "features", SelectedIDs: []string{"a"}, FillInText: "c"},
				{QuestionID: "ci"},
				{QuestionID: "note"},
			},
		},
		{
			name:   "skipped questions still hold their position",
			values: map[string]any{"note": "only this"},
			want: []question.Answer{
				{QuestionID: "db"},
				{QuestionID: "features"},
				{QuestionID: "ci"},
				{QuestionID: "note", FillInText: "only this"},
			},
		},
		{
			name:   "values of the wrong type are ignored",
			values: map[string]any{"db": 7, "features": "a", "ci": "yes", "note": 3},
			want: []question.Answer{
				{QuestionID: "db"},
				{QuestionID: "features"},
				{QuestionID: "ci"},
				{QuestionID: "note"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, answersFrom(choiceQuestion().Questions, tt.values))
		})
	}
}

func TestQuestionRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("an accepted form answers the question", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sid := h.elicitingSession(t)
		h.client.elicit = func(context.Context, protocol.ElicitationRequest) (protocol.ElicitationResponse, error) {
			return protocol.ElicitationResponse{Action: protocol.ElicitActionAccept, Values: map[string]any{"db": "pg"}}, nil
		}

		req := choiceQuestion()
		req.SessionID = sid
		h.publishQuestion(req)

		eventually(t, func() bool {
			h.ws.mu.Lock()
			defer h.ws.mu.Unlock()
			return len(h.ws.answers) == 1
		})
		h.ws.snapshot(func() {
			require.Len(t, h.ws.answers[0], 4)
			require.Equal(t, []string{"pg"}, h.ws.answers[0][0].SelectedIDs)
			require.Zero(t, h.ws.questionCancels)
		})
	})

	for _, action := range []protocol.ElicitAction{protocol.ElicitActionDecline, protocol.ElicitActionCancel} {
		t.Run(string(action)+" cancels the question", func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			sid := h.elicitingSession(t)
			h.client.elicit = func(context.Context, protocol.ElicitationRequest) (protocol.ElicitationResponse, error) {
				return protocol.ElicitationResponse{Action: action}, nil
			}
			req := choiceQuestion()
			req.SessionID = sid
			h.publishQuestion(req)

			eventually(t, func() bool {
				h.ws.mu.Lock()
				defer h.ws.mu.Unlock()
				return h.ws.questionCancels == 1
			})
			h.ws.snapshot(func() { require.Empty(t, h.ws.answers) })
		})
	}

	t.Run("an editor without forms cancels at once", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sid := h.newSession(t)
		req := choiceQuestion()
		req.SessionID = sid
		h.publishQuestion(req)

		eventually(t, func() bool {
			h.ws.mu.Lock()
			defer h.ws.mu.Unlock()
			return h.ws.questionCancels == 1
		})
		require.Empty(t, h.client.entries(), "the editor is never asked")
	})

	t.Run("a question for another session is cancelled", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.elicitingSession(t)
		req := choiceQuestion()
		req.SessionID = "stranger"
		h.publishQuestion(req)

		eventually(t, func() bool {
			h.ws.mu.Lock()
			defer h.ws.mu.Unlock()
			return h.ws.questionCancels == 1
		})
	})
}

func TestQuestionGuard(t *testing.T) {
	t.Parallel()

	t.Run("a replaced batch loses its answer", func(t *testing.T) {
		t.Parallel()
		var g questionGuard
		first := g.open(t.Context(), "one")
		g.open(t.Context(), "two")

		require.Error(t, first.Err(), "the replaced batch's prompt is withdrawn")
		require.False(t, g.claim("one"))
		require.True(t, g.claim("two"))
	})

	t.Run("a batch can be claimed once", func(t *testing.T) {
		t.Parallel()
		var g questionGuard
		g.open(t.Context(), "one")
		require.True(t, g.claim("one"))
		require.False(t, g.claim("one"))
	})

	t.Run("dismissal withdraws the prompt and voids the answer", func(t *testing.T) {
		t.Parallel()
		var g questionGuard
		ctx := g.open(t.Context(), "one")
		g.dismissed("other")
		require.NoError(t, ctx.Err(), "dismissing another batch changes nothing")

		g.dismissed("one")
		require.Error(t, ctx.Err())
		require.False(t, g.claim("one"))
	})
}
