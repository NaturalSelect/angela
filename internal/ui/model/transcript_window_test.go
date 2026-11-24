package model

import (
	"context"
	"errors"
	"strconv"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	agenttools "github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/undo"
)

// testItems returns n chat items with the IDs prefix-0 .. prefix-(n-1).
func testItems(prefix string, n int) []chat.MessageItem {
	items := make([]chat.MessageItem, 0, n)
	for i := range n {
		id := prefix + "-" + strconv.Itoa(i)
		items = append(items, testMessageItem{id: id, text: id})
	}
	return items
}

// userMessages returns n user messages of session s1 with the IDs
// prefix-0 .. prefix-(n-1), oldest first.
func userMessages(prefix string, n int) []message.Message {
	msgs := make([]message.Message, 0, n)
	for i := range n {
		id := prefix + "-" + strconv.Itoa(i)
		msgs = append(msgs, message.Message{
			ID:        id,
			SessionID: "s1",
			Role:      message.User,
			Parts:     []message.ContentPart{message.TextContent{Text: id}},
		})
	}
	return msgs
}

// loadedWindow is a UI showing loaded items (m-0 .. m-99) of session s1
// scrolled to scrollTo, with the given transcript window. Its caches are
// warm, so only the transcript can cause workspace calls.
func loadedWindow(t *testing.T, scrollTo int, w transcriptWindow) (*UI, *MockWorkspace) {
	t.Helper()
	pinTTLs(t)

	m, ws := newMockBusyUI(t)
	warmCaches(m, false)
	m.chat.SetMessages(testItems("m", 100)...)
	m.updateLayoutAndSize()
	m.chat.ScrollToIndex(scrollTo)
	require.Equal(t, scrollTo, m.chat.FirstVisibleIndex())
	m.transcript = w
	return m, ws
}

func olderMessagesIn(msgs []tea.Msg) (olderMessagesMsg, bool) {
	for _, msg := range msgs {
		if older, ok := msg.(olderMessagesMsg); ok {
			return older, true
		}
	}
	return olderMessagesMsg{}, false
}

func TestMaybeLoadOlderMessagesStaysIdleAwayFromTheTop(t *testing.T) {
	m, _ := loadedWindow(t, 50, transcriptWindow{sessionID: "s1", oldestID: "m-0", hasOlder: true})

	require.Nil(t, m.maybeLoadOlderMessages())
	require.False(t, m.transcript.loading)
}

func TestMaybeLoadOlderMessagesStaysIdleWithNothingOlder(t *testing.T) {
	m, _ := loadedWindow(t, 0, transcriptWindow{sessionID: "s1", oldestID: "m-0"})

	require.Nil(t, m.maybeLoadOlderMessages())
}

func TestMaybeLoadOlderMessagesReadsOnePageNearTheTop(t *testing.T) {
	m, ws := loadedWindow(t, 10, transcriptWindow{sessionID: "s1", oldestID: "m-0", hasOlder: true})

	var calls int
	ws.EXPECT().ListMessagePage(gomock.Any(), "s1", "m-0", transcriptPageSize).
		DoAndReturn(func(context.Context, string, string, int) (message.Page, error) {
			calls++
			return message.Page{Messages: userMessages("old", 3), HasOlder: true}, nil
		})

	cmd := m.maybeLoadOlderMessages()
	require.NotNil(t, cmd, "ten percent of a hundred items is the edge of the preload zone")
	require.True(t, m.transcript.loading)
	require.Nil(t, m.maybeLoadOlderMessages(), "a read in flight must not be started twice")

	older, ok := olderMessagesIn(runCmds(m, cmd))
	require.True(t, ok, "the command must produce the page")
	require.Equal(t, 1, calls)
	require.Equal(t, "m-0", older.beforeID)
	require.Equal(t, "old-0", older.oldestID)
	require.True(t, older.hasOlder)
	require.Len(t, older.items, 3)
}

func TestMaybeLoadOlderMessagesStaysIdleJustOutsideThePreloadZone(t *testing.T) {
	m, _ := loadedWindow(t, 11, transcriptWindow{sessionID: "s1", oldestID: "m-0", hasOlder: true})

	require.Nil(t, m.maybeLoadOlderMessages())
}

func TestMaybeLoadOlderMessagesStaysIdleForAnotherSessionsWindow(t *testing.T) {
	m, _ := loadedWindow(t, 0, transcriptWindow{sessionID: "s2", oldestID: "x", hasOlder: true})

	require.Nil(t, m.maybeLoadOlderMessages())
}

func TestMaybeLoadOlderMessagesStaysIdleWhileTheCursorIsStale(t *testing.T) {
	m, _ := loadedWindow(t, 0, transcriptWindow{sessionID: "s1", oldestID: "m-0", hasOlder: true, stale: true})

	require.Nil(t, m.maybeLoadOlderMessages())
}

// The trigger runs at the end of every Update, so scrolling to the top by
// any route reads the next page.
func TestUpdateReadsTheOlderPageOnceTheViewportNearsTheTop(t *testing.T) {
	m, ws := loadedWindow(t, 3, transcriptWindow{sessionID: "s1", oldestID: "m-0", hasOlder: true})

	var calls int
	ws.EXPECT().ListMessagePage(gomock.Any(), "s1", "m-0", transcriptPageSize).
		DoAndReturn(func(context.Context, string, string, int) (message.Page, error) {
			calls++
			return message.Page{Messages: userMessages("old", 2)}, nil
		})

	_, cmd := m.Update(plainMsg{})
	runCmds(m, cmd)

	require.Equal(t, 1, calls)
	require.True(t, m.transcript.loading)
}

func TestOlderMessagesAreAddedAboveWithoutMovingTheViewport(t *testing.T) {
	m, _ := loadedWindow(t, 5, transcriptWindow{sessionID: "s1", oldestID: "m-0", hasOlder: true, loading: true})

	m.handleOlderMessages(olderMessagesMsg{
		sessionID: "s1",
		beforeID:  "m-0",
		items:     testItems("old", 30),
		oldestID:  "old-0",
		hasOlder:  true,
	})

	require.Equal(t, 130, m.chat.Len())
	require.Equal(t, 35, m.chat.FirstVisibleIndex())
	first, ok := m.chat.list.ItemAt(m.chat.FirstVisibleIndex()).(chat.MessageItem)
	require.True(t, ok)
	require.Equal(t, "m-5", first.ID(), "the viewport must stay on the message it showed")

	w := m.transcript
	require.False(t, w.loading)
	require.True(t, w.hasOlder)
	require.Equal(t, "old-0", w.oldestID)
}

func TestOlderMessagesEndTheWindowAtTheStartOfTheSession(t *testing.T) {
	m, _ := loadedWindow(t, 0, transcriptWindow{sessionID: "s1", oldestID: "m-0", hasOlder: true, loading: true})

	m.handleOlderMessages(olderMessagesMsg{
		sessionID: "s1",
		beforeID:  "m-0",
		items:     testItems("old", 5),
		oldestID:  "old-0",
	})

	require.False(t, m.transcript.hasOlder)
	m.chat.ScrollToIndex(0)
	require.Nil(t, m.maybeLoadOlderMessages(), "nothing older is left to read")
}

func TestOlderMessagesForAnotherSessionAreDropped(t *testing.T) {
	m, _ := loadedWindow(t, 0, transcriptWindow{sessionID: "s2", oldestID: "x", hasOlder: true, loading: true})
	m.session = &session.Session{ID: "s2"}

	m.handleOlderMessages(olderMessagesMsg{
		sessionID: "s1",
		beforeID:  "m-0",
		items:     testItems("old", 5),
		oldestID:  "old-0",
	})

	require.Equal(t, 100, m.chat.Len(), "the other session's page must not land in this chat")
	require.True(t, m.transcript.loading, "the read in flight belongs to the new window")
	require.Equal(t, "x", m.transcript.oldestID)
}

func TestOlderMessagesForASupersededCursorAreDropped(t *testing.T) {
	m, _ := loadedWindow(t, 0, transcriptWindow{sessionID: "s1", oldestID: "m-0", hasOlder: true, loading: true})

	m.handleOlderMessages(olderMessagesMsg{
		sessionID: "s1",
		beforeID:  "gone",
		items:     testItems("old", 5),
		oldestID:  "old-0",
	})

	require.Equal(t, 100, m.chat.Len())
	require.Equal(t, "m-0", m.transcript.oldestID)
}

func TestOlderMessagesDroppedAfterTheWindowWasResetClearTheNotice(t *testing.T) {
	m, _ := loadedWindow(t, 0, transcriptWindow{sessionID: "s1", oldestID: "m-0", hasOlder: true})
	m.showPending(olderMessagesInfo)
	require.Equal(t, olderMessagesInfo, m.status.msg)

	m.handleOlderMessages(olderMessagesMsg{sessionID: "s1", beforeID: "m-0", oldestID: "old-0"})

	require.NotEqual(t, olderMessagesInfo, m.status.msg, "a notice with no read behind it must not linger")
}

func TestOlderMessagesFailureStopsPagingButKeepsTheChat(t *testing.T) {
	m, _ := loadedWindow(t, 0, transcriptWindow{sessionID: "s1", oldestID: "m-0", hasOlder: true, loading: true})
	m.showPending(olderMessagesInfo)

	m.handleOlderMessages(olderMessagesMsg{sessionID: "s1", beforeID: "m-0", err: errors.New("boom")})

	require.Equal(t, 100, m.chat.Len())
	require.False(t, m.transcript.loading)
	require.False(t, m.transcript.hasOlder, "a failing read must not be retried on every update")
	require.Nil(t, m.maybeLoadOlderMessages())
	require.NotEqual(t, olderMessagesInfo, m.status.msg)
}

func TestOlderMessagesLandingClearsTheLoadingNotice(t *testing.T) {
	m, _ := loadedWindow(t, 0, transcriptWindow{sessionID: "s1", oldestID: "m-0", hasOlder: true, loading: true})
	m.showPending(olderMessagesInfo)

	m.handleOlderMessages(olderMessagesMsg{
		sessionID: "s1",
		beforeID:  "m-0",
		items:     testItems("old", 2),
		oldestID:  "old-0",
	})

	require.NotEqual(t, olderMessagesInfo, m.status.msg)
}

// A background agent launched in an older page was reported on in a newer
// one. Without that outcome the page would find no notice and, with no
// live child session, mark the agent failed.
func TestOlderPageSettlesABackgroundAgentFromANewerNotice(t *testing.T) {
	m, ws := newBackgroundTranscriptUI(t)
	m.transcript = transcriptWindow{
		sessionID: "s1",
		oldestID:  "m9",
		hasOlder:  true,
		outcomes:  map[string]agenttools.BackgroundAgentStatus{bgAgentID: agenttools.BackgroundAgentCompleted},
	}
	// NOTE: AgentIsSessionBusy is not stubbed. The mock fails the test if
	// the page probes a child session its outcome is already known for.
	ws.EXPECT().ListMessagePage(gomock.Any(), "s1", "m9", transcriptPageSize).
		Return(message.Page{Messages: backgroundLaunchMessages(t, "m1", "c1", bgAgentID)}, nil)

	older, ok := olderMessagesIn(runCmds(m, m.maybeLoadOlderMessages()))
	require.True(t, ok)

	agents := agentItemsOf(older.items)
	require.Len(t, agents, 1)
	require.Equal(t, agenttools.BackgroundAgentCompleted, agents[0].BackgroundOutcome())
	require.False(t, agents[0].BackgroundRunning())

	m.handleOlderMessages(older)
	require.Equal(t, agenttools.BackgroundAgentCompleted, m.transcript.outcomes[bgAgentID],
		"the outcomes seen so far must survive for the pages still older")
}

// An outcome from a newer notice wins over one an older page read.
func TestOlderPageOutcomesDoNotOverrideNewerOnes(t *testing.T) {
	m, _ := loadedWindow(t, 0, transcriptWindow{
		sessionID: "s1",
		oldestID:  "m-0",
		hasOlder:  true,
		loading:   true,
		outcomes:  map[string]agenttools.BackgroundAgentStatus{bgAgentID: agenttools.BackgroundAgentStopped},
	})

	m.handleOlderMessages(olderMessagesMsg{
		sessionID: "s1",
		beforeID:  "m-0",
		items:     testItems("old", 1),
		oldestID:  "old-0",
		outcomes: map[string]agenttools.BackgroundAgentStatus{
			bgAgentID:      agenttools.BackgroundAgentFailed,
			otherBgAgentID: agenttools.BackgroundAgentCompleted,
		},
	})

	require.Equal(t, agenttools.BackgroundAgentStopped, m.transcript.outcomes[bgAgentID])
	require.Equal(t, agenttools.BackgroundAgentCompleted, m.transcript.outcomes[otherBgAgentID])
}

// A notice that arrives while the session is open is what the next older
// page needs, just like one read from storage.
func TestLiveNoticeIsRememberedForOlderPages(t *testing.T) {
	m, _ := newBackgroundTranscriptUI(t)
	m.transcript = transcriptWindow{sessionID: "s1", oldestID: "m9", hasOlder: true}

	m.appendSessionMessage(noticeMessage("rem1", endedNotice(bgAgentID, agenttools.BackgroundAgentStopped)))

	require.Equal(t, agenttools.BackgroundAgentStopped, m.transcript.outcomes[bgAgentID])
}

func TestSessionMessagesOpenTheWindowAtTheOldestLoadedMessage(t *testing.T) {
	m, _ := newBackgroundTranscriptUI(t)
	outcomes := map[string]agenttools.BackgroundAgentStatus{bgAgentID: agenttools.BackgroundAgentCompleted}

	m.handleSessionMessages(sessionMessagesMsg{
		sessionID: "s1",
		items:     testItems("m", 3),
		oldestID:  "m-0",
		hasOlder:  true,
		outcomes:  outcomes,
	})

	require.Equal(t, transcriptWindow{sessionID: "s1", oldestID: "m-0", hasOlder: true, outcomes: outcomes}, m.transcript)
}

func TestSessionMessagesOfAShortSessionLeaveNothingToPage(t *testing.T) {
	m, _ := newBackgroundTranscriptUI(t)

	m.handleSessionMessages(sessionMessagesMsg{sessionID: "s1", items: testItems("m", 3), hasOlder: true})

	require.False(t, m.transcript.hasOlder, "a page with no oldest message has no cursor to read before")
}

func TestDeletingTheCursorMessageMarksTheWindowStale(t *testing.T) {
	m, _ := loadedWindow(t, 0, transcriptWindow{sessionID: "s1", oldestID: "m-0", hasOlder: true})

	m.Update(pubsub.Event[message.Message]{
		Type:    pubsub.DeletedEvent,
		Payload: message.Message{ID: "m-0", SessionID: "s1"},
	})

	require.True(t, m.transcript.stale)
	require.Nil(t, m.maybeLoadOlderMessages(), "no page may be read before a cursor that no longer exists")
}

func TestDeletingAnotherMessageLeavesTheWindowAlone(t *testing.T) {
	m, _ := loadedWindow(t, 0, transcriptWindow{sessionID: "s1", oldestID: "m-0", hasOlder: true})

	m.Update(pubsub.Event[message.Message]{
		Type:    pubsub.DeletedEvent,
		Payload: message.Message{ID: "m-99", SessionID: "s1"},
	})

	require.False(t, m.transcript.stale)
}

// An undo longer than the loaded part leaves the chat with nothing to
// continue from, so the tail is read again once the undo is over.
func TestUndoResultReloadsTheTailWhenTheCursorIsGone(t *testing.T) {
	m, ws := loadedWindow(t, 0, transcriptWindow{sessionID: "s1", oldestID: "m-0", hasOlder: true, stale: true})

	var calls int
	ws.EXPECT().ListMessagePage(gomock.Any(), "s1", "", transcriptPageSize).
		DoAndReturn(func(context.Context, string, string, int) (message.Page, error) {
			calls++
			return message.Page{Messages: userMessages("tail", 2), HasOlder: true}, nil
		})

	_, cmd := m.Update(undoResultMsg{sessionID: "s1", result: undo.Result{}})
	msgs := runCmds(m, cmd)

	require.Equal(t, 1, calls)
	require.Equal(t, transcriptWindow{sessionID: "s1"}, m.transcript, "the window must start over from the tail")
	var reloaded *sessionMessagesMsg
	for _, msg := range msgs {
		if loaded, ok := msg.(sessionMessagesMsg); ok {
			reloaded = &loaded
		}
	}
	require.NotNil(t, reloaded, "the tail must be delivered to the chat")
	require.Equal(t, "tail-0", reloaded.oldestID)
	require.True(t, reloaded.hasOlder)
}

func TestUndoResultKeepsTheChatWhenTheCursorSurvives(t *testing.T) {
	m, _ := loadedWindow(t, 50, transcriptWindow{sessionID: "s1", oldestID: "m-0", hasOlder: true})

	// NOTE: ListMessagePage is not stubbed: the mock fails the test on a
	// read nobody needed. The viewport is mid-list, outside the preload zone.
	_, cmd := m.Update(undoResultMsg{sessionID: "s1", result: undo.Result{}})
	runCmds(m, cmd)

	require.Equal(t, 100, m.chat.Len())
	require.Equal(t, "m-0", m.transcript.oldestID)
}

func TestUndoResultWithAStaleCursorAndNothingOlderKeepsTheChat(t *testing.T) {
	m, _ := loadedWindow(t, 0, transcriptWindow{sessionID: "s1", oldestID: "m-0", stale: true})

	_, cmd := m.Update(undoResultMsg{sessionID: "s1", result: undo.Result{}})
	runCmds(m, cmd)

	require.Equal(t, 100, m.chat.Len(), "the whole session is loaded, so there is nothing to read again")
}
