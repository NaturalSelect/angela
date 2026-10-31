package model

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/util"
)

// sendMessage sends a message with the given content and attachments.
func (m *UI) sendMessage(content string, attachments ...message.Attachment) tea.Cmd {
	if m.viewingSubAgent() {
		return util.ReportWarn("This is a sub-agent's transcript. Press ctrl+up to go back and reply there.")
	}
	if err := m.com.Workspace.AgentReadyErr(); err != nil {
		return util.ReportError(err)
	}

	// Start the turn timer.
	common.StartTurn()

	// Sending is an explicit "I am here now": drop whatever scrollback the
	// user was reading and follow the reply as it streams in.
	m.chat.ScrollToBottom()

	var cmds []tea.Cmd
	if !m.hasSession() {
		newSession, err := m.com.Workspace.CreateSession(context.Background(), "New Session")
		if err != nil {
			return util.ReportError(err)
		}
		if m.forceCompactMode {
			m.isCompact = true
		}
		if newSession.ID != "" {
			m.session = &newSession
			cmds = append(cmds, m.loadSession(newSession.ID))
		}
		m.setState(uiChat, m.focus)
	}

	ctx := context.Background()
	cmds = append(cmds, func() tea.Msg {
		for _, path := range m.sessionFileReads {
			m.com.Workspace.FileTrackerRecordRead(ctx, m.session.ID, path)
			m.com.Workspace.LSPStart(ctx, path)
		}
		return nil
	})

	// Capture session ID to avoid race with main goroutine updating m.session.
	sessionID := m.session.ID
	// Optimistically mark the agent busy: the prompt we are about to submit
	// either starts a run or is enqueued behind one. This keeps esc pressed
	// right after enter routing to cancelAgent instead of reading a stale
	// idle value; the authoritative state arrives via agentRunSubmittedMsg.
	// Bump the busy/queue generations so any probe started before this
	// optimistic write is discarded rather than reverting us to idle.
	m.agentBusyCache.set(true)
	m.sessionBusyCache.set(true)
	m.busyFetchGen++
	m.invalidatePromptQueue()
	cmds = append(cmds, func() tea.Msg {
		// AgentRun blocks for the whole turn and reports a terminal
		// failure right here. A failure outside a turn this UI started
		// arrives as a TypeAgentError notification
		// (handleAgentNotification). Either way a terminal failure must
		// restore whatever this session still has queued behind it, so
		// both paths funnel through restoreQueueOnAgentError.
		err := m.com.Workspace.AgentRun(context.Background(), sessionID, content, attachments...)
		if err != nil && !errors.Is(err, context.Canceled) {
			return agentRunFailedMsg{sessionID: sessionID, err: err}
		}
		return agentRunSubmittedMsg{}
	})
	return tea.Batch(cmds...)
}

// runShellCommand executes a shell command without triggering the LLM. The
// result is displayed as a tool-style item in the chat.
// Callers must ensure a session already exists.
func (m *UI) runShellCommand(command string) tea.Cmd {
	var cmds []tea.Cmd

	sessionID := m.session.ID
	contentWidth := min(m.layout.main.Dx()-2, 120)

	// Append a pending shell item immediately so the user sees feedback.
	pendingItem := chat.NewPendingShellItem(m.com.Styles, command)
	m.chat.AppendMessages(pendingItem)
	if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := pendingItem.StartAnimation(); cmd != nil {
		cmds = append(cmds, cmd)
	}

	// Stream output via channel. The progress callback writes chunks
	// to streamCh; a reader cmd converts them to shellStreamMsg values.
	streamCh := make(chan string, 64)
	pendingID := pendingItem.ID()

	onProgress := func(chunk string) {
		select {
		case streamCh <- chunk:
		default:
			// Drop if UI can't keep up.
		}
	}

	// Reader cmd: drains streamCh into shellStreamMsg until closed.
	cmds = append(cmds, func() tea.Msg {
		chunk, ok := <-streamCh
		if !ok {
			return nil
		}
		return shellStreamMsg{PendingID: pendingID, Chunk: chunk, streamCh: streamCh}
	})

	ctx, cancel := context.WithCancel(context.Background())
	m.bangCancel = cancel

	cmds = append(cmds, func() tea.Msg {
		resp, err := m.com.Workspace.AgentRunShellCommand(ctx, sessionID, command, contentWidth, onProgress, false)
		close(streamCh)
		if err != nil && !errors.Is(err, context.Canceled) {
			return util.InfoMsg{
				Type: util.InfoTypeError,
				Msg:  fmt.Sprintf("shell: %v", err),
			}
		}
		exitCode := resp.ExitCode
		if errors.Is(err, context.Canceled) {
			exitCode = 130 // conventional SIGINT exit code
		}
		return shellResultMsg{
			PendingID: pendingID,
			Command:   command,
			Output:    resp.Output,
			ExitCode:  exitCode,
		}
	})
	return tea.Batch(cmds...)
}

const cancelTimerDuration = 2 * time.Second

// cancelTimerCmd creates a command that expires the cancel timer.
func cancelTimerCmd() tea.Cmd {
	return tea.Tick(cancelTimerDuration, func(time.Time) tea.Msg {
		return cancelTimerExpiredMsg{}
	})
}

// cancelAgent handles the cancel key press. The first press sets isCanceling to true
// and starts a timer. The second press (before the timer expires) actually
// cancels the agent.
func (m *UI) cancelAgent() tea.Cmd {
	if !m.hasSession() {
		return nil
	}

	// Gate on the memoized ready state: esc is a hot key and AgentIsReady
	// may block.
	if !m.agentReady {
		return nil
	}

	if m.isCanceling {
		// Second escape press — actually cancel.
		m.isCanceling = false

		// Cancel a running bang command if one is in progress.
		if m.bangCancel != nil {
			m.bangCancel()
			m.bangCancel = nil
		}

		// NOTE: AgentCancel drops the backend queue, so the take must run
		// before it, in the same command; the busy refresh waits for the
		// result so its probe cannot race the cancel.
		m.stopTurnSpinner()
		return m.takeQueuedPromptsToEditorCmd(m.session.ID, true)
	}

	// Queued prompts pending: esc pops them back into the editor instead
	// of discarding them. Decide from the cached count (event-driven)
	// instead of a synchronous workspace probe.
	if m.promptQueue > 0 {
		return m.takeQueuedPromptsToEditorCmd(m.session.ID, false)
	}

	// First escape press - set canceling state and start timer.
	m.isCanceling = true
	return cancelTimerCmd()
}

// takeQueuedPromptsToEditorCmd takes every user-queued prompt behind
// sessionID's turn off the Update goroutine and, when cancel is set,
// cancels the turn right after. The result lands as queuedPromptsTakenMsg.
func (m *UI) takeQueuedPromptsToEditorCmd(sessionID string, cancel bool) tea.Cmd {
	ws := m.com.Workspace
	localCopy := m.promptQueueItems
	return func() tea.Msg {
		msg := queuedPromptsTakenMsg{sessionID: sessionID, cancelled: cancel}
		prompts, err := ws.AgentTakeQueuedPrompts(sessionID)
		if err != nil {
			// NOTE: AgentCancel drops the backend queue, so fall back to
			// the local copy.
			slog.Warn("Failed to take queued prompts; using the local copy",
				"session", sessionID, "error", err)
			prompts, msg.usedLocalCopy = localCopy, true
			if len(localCopy) > 0 {
				ws.AgentClearQueue(sessionID)
			}
		}
		msg.prompts = prompts
		if cancel {
			ws.AgentCancel(sessionID)
		}
		return msg
	}
}

func (m *UI) restoreQueuedPrompts(items []message.QueuedPrompt) tea.Cmd {
	// The mirror can be stale (e.g. restoreQueueOnAgentError takes even
	// when it reads 0, since a refresh may not have landed yet), so
	// whether there is anything to sync visually is decided from
	// whichever of the mirror or the take result had something, not
	// from the take result alone.
	hadMirror := m.promptQueue > 0 || len(m.promptQueueItems) > 0
	m.promptQueue = 0
	m.promptQueueItems = nil
	m.promptQueueCheckedAt = time.Now()

	if hadMirror || len(items) > 0 {
		// Bump the queue generation so a fetch started before this take
		// cannot land and repopulate the pill we just emptied.
		m.invalidatePromptQueue()
		m.syncQueuedChatItems()
		m.updateLayoutAndSize()
	}

	if len(items) == 0 {
		return nil
	}

	texts := make([]string, len(items))
	var atts []message.Attachment
	for i, qp := range items {
		texts[i] = qp.Prompt
		atts = append(atts, qp.Attachments...)
	}
	m.prependAttachments(atts)

	// NOTE: A prompt that held only files has no text, and prepending an
	// empty string would still push the draft down by a blank line.
	text := strings.Join(texts, "\n\n")
	if text == "" {
		return nil
	}
	return m.prependToEditor(text)
}

// restoreQueueOnAgentError pops any prompts still queued behind
// sessionID's turn back into the editor when that turn ends in an
// error instead of finishing normally. A failed turn never drains its
// own queue (see drainQueueForStep), so without this the prompts would
// sit stranded on the backend — indistinguishable from ones an idle
// agent simply has not gotten to yet — until the user noticed and
// pressed esc. Scoped to the session currently on screen: an error on
// a session the user has since navigated away from must not reach
// into the visible editor. The take itself runs off the Update goroutine
// and is applied when queuedPromptsTakenMsg lands.
func (m *UI) restoreQueueOnAgentError(sessionID string) tea.Cmd {
	if sessionID == "" || sessionID != m.currentSessionID() {
		return nil
	}
	return m.takeQueuedPromptsCmd(sessionID)
}

func (m *UI) takeQueuedPromptsCmd(sessionID string) tea.Cmd {
	ws := m.com.Workspace
	return func() tea.Msg {
		prompts, err := ws.AgentTakeQueuedPrompts(sessionID)
		return queuedPromptsTakenMsg{sessionID: sessionID, prompts: prompts, err: err}
	}
}

func (m *UI) applyQueuedPromptsTaken(msg queuedPromptsTakenMsg) tea.Cmd {
	var cmds []tea.Cmd
	if msg.cancelled {
		// NOTE: A cancelled turn publishes no terminal notification, so
		// the retry banner would otherwise outlive it and show on the
		// next turn until its backoff delay elapses.
		m.clearRetryStatus(msg.sessionID)
		m.invalidateBusyCaches()
		cmds = append(cmds, m.dispatchBusyRefresh())
	}

	switch {
	case msg.err != nil:
		// NOTE: The queue is still on the backend, so leave the mirror alone.
		slog.Warn("Failed to take queued prompts after an agent error",
			"session", msg.sessionID, "error", msg.err)
		cmds = append(cmds, util.ReportWarn("Could not recover queued prompts; they are still queued."))
	case msg.sessionID != m.currentSessionID():
		// NOTE: The prompts already left the backend queue, so hold them
		// until that session is shown again.
		m.stashRestoredPrompts(msg.sessionID, msg.prompts)
	default:
		if msg.usedLocalCopy && len(msg.prompts) > 0 {
			cmds = append(cmds, util.ReportWarn("Could not take queued prompts; restored the local copy (attachments may be missing)."))
		}
		cmds = append(cmds, m.restoreQueuedPrompts(msg.prompts))
	}
	return tea.Batch(cmds...)
}

func (m *UI) stashRestoredPrompts(sessionID string, prompts []message.QueuedPrompt) {
	if len(prompts) == 0 {
		return
	}
	if m.stashedPrompts == nil {
		m.stashedPrompts = make(map[string][]message.QueuedPrompt)
	}
	m.stashedPrompts[sessionID] = append(m.stashedPrompts[sessionID], prompts...)
}

func (m *UI) restoreStashedPrompts(sessionID string) tea.Cmd {
	prompts, ok := m.stashedPrompts[sessionID]
	if !ok {
		return nil
	}
	delete(m.stashedPrompts, sessionID)
	return m.restoreQueuedPrompts(prompts)
}

// prependAttachments places atts ahead of the attachments already on the
// draft, mirroring how prependToEditor treats text.
func (m *UI) prependAttachments(atts []message.Attachment) {
	if len(atts) == 0 {
		return
	}
	draft := m.attachments.List()
	m.attachments.Reset()
	for _, a := range atts {
		m.attachments.Update(a)
	}
	for _, a := range draft {
		m.attachments.Update(a)
	}
}

// prependToEditor places text ahead of whatever draft is being composed,
// so a half-typed follow-up is never clobbered.
func (m *UI) prependToEditor(text string) tea.Cmd {
	prevHeight := m.textarea.Height()
	restored := text
	if draft := m.textarea.Value(); draft != "" {
		restored += "\n\n" + draft
	}
	m.textarea.SetValue(restored)
	m.textarea.MoveToEnd()
	m.syncBangModeFromTextarea()
	return m.updateTextareaWithPrevHeight(nil, prevHeight)
}
