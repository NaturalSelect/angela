package tools

import (
	"context"
	"encoding/json"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/db"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

// reportEnv is a live session + message store over a throwaway SQLite
// file, which is what CollectReports reads.
type reportEnv struct {
	sessions session.Service
	messages message.Service
}

func newReportEnv(t *testing.T) reportEnv {
	t.Helper()
	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	q := db.New(conn)
	return reportEnv{
		sessions: session.NewService(q, conn),
		messages: message.NewService(q),
	}
}

func (e reportEnv) newSession(t *testing.T, title string) string {
	t.Helper()
	sess, err := e.sessions.Create(t.Context(), title)
	require.NoError(t, err)
	return sess.ID
}

// dispatch writes the assistant turn that calls the agent tool plus the
// tool turn carrying its result, mirroring what sessionAgent persists,
// and returns the report id the pair should collect under.
func (e reportEnv) dispatch(t *testing.T, sessionID, callID, subagentType, description, output string, isError bool) string {
	t.Helper()
	return e.dispatchNamed(t, sessionID, toolnames.Agent, callID, subagentType, description, output, isError)
}

func (e reportEnv) dispatchNamed(t *testing.T, sessionID, toolName, callID, subagentType, description, output string, isError bool) string {
	t.Helper()

	input, err := json.Marshal(map[string]string{
		"subagent_type": subagentType,
		"description":   description,
	})
	require.NoError(t, err)

	assistant, err := e.messages.Create(t.Context(), sessionID, message.CreateMessageParams{
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: callID, Name: toolName, Input: string(input), Finished: true},
		},
	})
	require.NoError(t, err)

	_, err = e.messages.Create(t.Context(), sessionID, message.CreateMessageParams{
		Role: message.Tool,
		Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: callID, Name: toolName, Content: output, IsError: isError},
		},
	})
	require.NoError(t, err)

	require.NoError(t, e.messages.FlushAll(t.Context()))
	return ReportID(e.sessions, assistant.ID, callID)
}

func loadReport(t *testing.T, env reportEnv, sessionID, id string) fantasy.ToolResponse {
	t.Helper()
	tool := NewLoadReportTool(env.sessions, env.messages)
	ctx := context.WithValue(t.Context(), SessionIDContextKey, sessionID)
	resp, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "load-1",
		Name:  toolnames.LoadReport,
		Input: `{"id":"` + id + `"}`,
	})
	require.NoError(t, err)
	return resp
}

func TestReportIDIsStableAndScopedToTheDispatch(t *testing.T) {
	t.Parallel()
	env := newReportEnv(t)

	first := ReportID(env.sessions, "msg-1", "call-1")
	require.Equal(t, first, ReportID(env.sessions, "msg-1", "call-1"),
		"the same dispatch must always hash to the same handle")
	require.NotEqual(t, first, ReportID(env.sessions, "msg-1", "call-2"))
	require.NotEqual(t, first, ReportID(env.sessions, "msg-2", "call-1"))

	require.True(t, len(first) == len(reportIDPrefix)+reportIDHashLen)
	require.Equal(t, reportIDPrefix, first[:len(reportIDPrefix)])
}

func TestCollectReportsPairsDispatchesWithTheirOutput(t *testing.T) {
	t.Parallel()
	env := newReportEnv(t)
	sessionID := env.newSession(t, "parent")

	firstID := env.dispatch(t, sessionID, "call-1", "explore", "find the parser", "parser lives in parse.go", false)
	secondID := env.dispatch(t, sessionID, "call-2", "plan", "design the fix", "step 1, step 2", false)

	reports, err := CollectReports(t.Context(), env.sessions, env.messages, sessionID)
	require.NoError(t, err)
	require.Len(t, reports, 2)

	require.Equal(t, firstID, reports[0].ID)
	require.Equal(t, "explore", reports[0].AgentType)
	require.Equal(t, "find the parser", reports[0].Task)
	require.Equal(t, "parser lives in parse.go", reports[0].Content)

	require.Equal(t, secondID, reports[1].ID)
	require.Equal(t, "plan", reports[1].AgentType)
}

func TestCollectReportsSkipsFailedAndNonAgentCalls(t *testing.T) {
	t.Parallel()
	env := newReportEnv(t)
	sessionID := env.newSession(t, "parent")

	env.dispatch(t, sessionID, "call-1", "explore", "doomed", "Subagent is unavailable", true)
	env.dispatchNamed(t, sessionID, toolnames.Grep, "call-2", "", "", "match in main.go", false)
	keptID := env.dispatch(t, sessionID, "call-3", "general", "real work", "done", false)

	reports, err := CollectReports(t.Context(), env.sessions, env.messages, sessionID)
	require.NoError(t, err)
	require.Len(t, reports, 1, "only the successful agent dispatch is a report")
	require.Equal(t, keptID, reports[0].ID)
}

func TestCollectReportsDefaultsMissingSubagentTypeToExplore(t *testing.T) {
	t.Parallel()
	env := newReportEnv(t)
	sessionID := env.newSession(t, "parent")
	env.dispatch(t, sessionID, "call-1", "", "no type given", "output", false)

	reports, err := CollectReports(t.Context(), env.sessions, env.messages, sessionID)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	require.Equal(t, "explore", reports[0].AgentType,
		"an omitted subagent_type is dispatched to explore, so the report must say so")
}

func TestLoadReportReturnsTheStoredOutput(t *testing.T) {
	t.Parallel()
	env := newReportEnv(t)
	sessionID := env.newSession(t, "parent")
	id := env.dispatch(t, sessionID, "call-1", "deep-research", "websocket vs sse", "the full report body", false)

	resp := loadReport(t, env, sessionID, id)
	require.False(t, resp.IsError)
	require.Equal(t, "the full report body", resp.Content)
}

func TestLoadReportOnUnknownIDListsWhatExists(t *testing.T) {
	t.Parallel()
	env := newReportEnv(t)
	sessionID := env.newSession(t, "parent")
	realID := env.dispatch(t, sessionID, "call-1", "explore", "find the parser", "body", false)

	resp := loadReport(t, env, sessionID, "rpt_deadbeef")
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, realID,
		"a guessed id must come back with the real ones so the model can correct itself")
	require.Contains(t, resp.Content, "find the parser")
	require.NotContains(t, resp.Content, "body")
}

func TestLoadReportWithNoReportsSaysSo(t *testing.T) {
	t.Parallel()
	env := newReportEnv(t)
	sessionID := env.newSession(t, "parent")

	resp := loadReport(t, env, sessionID, "rpt_deadbeef")
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "has not dispatched any agent")
}

func TestLoadReportCannotReachAnotherSession(t *testing.T) {
	t.Parallel()
	env := newReportEnv(t)
	mine := env.newSession(t, "mine")
	theirs := env.newSession(t, "theirs")

	env.dispatch(t, mine, "call-1", "explore", "my task", "my body", false)
	otherID := env.dispatch(t, theirs, "call-2", "explore", "their task", "their secret body", false)

	resp := loadReport(t, env, mine, otherID)
	require.True(t, resp.IsError)
	require.NotContains(t, resp.Content, "their secret body",
		"a report from another session must not leak through the error path either")
	require.NotContains(t, resp.Content, "their task")
}

func TestLoadReportRequiresASession(t *testing.T) {
	t.Parallel()
	env := newReportEnv(t)
	tool := NewLoadReportTool(env.sessions, env.messages)

	_, err := tool.Run(t.Context(), fantasy.ToolCall{
		ID:    "load-1",
		Name:  toolnames.LoadReport,
		Input: `{"id":"rpt_deadbeef"}`,
	})
	require.Error(t, err)
}
