package model

import (
	"context"
	"log/slog"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	agenttools "github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	fimage "github.com/NaturalSelect/angela/internal/ui/image"
	"github.com/NaturalSelect/angela/internal/ui/util"
)

// loadSessionMessagesCmd fetches a session's transcript and builds its
// chat items off the Update goroutine. Both halves belong here: the
// fetch may block, and resolving nested agent tool calls costs one more
// call per nested tool, so doing it inline stalls the render loop for the
// whole tree.
func (m *UI) loadSessionMessagesCmd(sessionID string) tea.Cmd {
	return func() tea.Msg {
		msgs, err := m.com.Workspace.ListMessages(context.Background(), sessionID)
		if err != nil {
			return util.ReportError(err)()
		}
		items, lastUserMessageTime := m.buildSessionItems(sessionID, msgs)
		return sessionMessagesMsg{
			sessionID:           sessionID,
			items:               items,
			lastUserMessageTime: lastUserMessageTime,
		}
	}
}

// lastAssistantIndex reports the position of the final assistant message in
// a transcript, or -1 when there is none. Only that message can still be
// producing results: the tool calls in an earlier one were all answered
// before the next assistant message was written, so one left without a
// result was orphaned.
func lastAssistantIndex(msgs []*message.Message) int {
	last := -1
	for i, msg := range msgs {
		if msg.Role == message.Assistant {
			last = i
		}
	}
	return last
}

// hasOrphanedCall reports whether any tool call in the transcript is missing
// its result. Probing the agent for liveness is not free, and a
// transcript with every result in place has nothing to decide.
func hasOrphanedCall(msgs []*message.Message, results map[string]message.ToolResult) bool {
	for _, msg := range msgs {
		if msg.Role != message.Assistant {
			continue
		}
		for _, tc := range msg.ToolCalls() {
			if _, ok := results[tc.ID]; !ok {
				return true
			}
		}
	}
	return false
}

// buildSessionItems turns a transcript into chat items and reports the
// timestamp of the last user message. It touches no UI state, so it is
// safe to run from a command.
func (m *UI) buildSessionItems(sessionID string, msgs []message.Message) ([]chat.MessageItem, int64) {
	msgPtrs := make([]*message.Message, len(msgs))
	for i := range msgs {
		msgPtrs[i] = &msgs[i]
	}
	toolResultMap := chat.BuildToolResultMap(msgPtrs)

	// Asked per session, not through the busy cache: that cache answers
	// for the whole process and carries a TTL, so another session's run
	// would keep this one's orphans spinning.
	runActive := false
	if hasOrphanedCall(msgPtrs, toolResultMap) {
		runActive = m.com.Workspace.AgentIsSessionBusy(sessionID)
	}
	lastAssistant := lastAssistantIndex(msgPtrs)

	var lastUserMessageTime int64
	if len(msgPtrs) > 0 {
		lastUserMessageTime = msgPtrs[0].CreatedAt
	}

	var outcomes map[string]agenttools.BackgroundAgentStatus
	items := make([]chat.MessageItem, 0, len(msgs)*2)
	for i, msg := range msgPtrs {
		msgRunActive := runActive && i == lastAssistant
		switch msg.Role {
		case message.User:
			if msg.IsReminder() {
				// Hidden from the transcript, but a notice inside it
				// says how a background agent ended.
				for id, status := range noticeOutcomes(msg) {
					if outcomes == nil {
						outcomes = make(map[string]agenttools.BackgroundAgentStatus)
					}
					outcomes[id] = status
				}
				continue
			}
			lastUserMessageTime = msg.CreatedAt
			items = append(items, chat.ExtractMessageItems(m.com.Styles, msg, toolResultMap, m.com.Workspace.WorkingDir(), msgRunActive)...)
		case message.Assistant:
			items = append(items, chat.ExtractMessageItems(m.com.Styles, msg, toolResultMap, m.com.Workspace.WorkingDir(), msgRunActive)...)
			if msg.FinishPart() != nil && msg.FinishPart().Reason == message.FinishReasonEndTurn {
				infoItem := chat.NewAssistantInfoItem(m.com.Styles, msg, m.com.Config(), time.Unix(lastUserMessageTime, 0))
				items = append(items, infoItem)
			}
		default:
			items = append(items, chat.ExtractMessageItems(m.com.Styles, msg, toolResultMap, m.com.Workspace.WorkingDir(), msgRunActive)...)
		}
	}

	m.settleBackgroundAgents(items, outcomes)
	m.loadNestedToolCalls(items)
	return items, lastUserMessageTime
}

// applySessionItems installs prebuilt chat items as the current
// session's transcript.
func (m *UI) applySessionItems(items []chat.MessageItem) tea.Cmd {
	var cmds []tea.Cmd

	// If the user switches between sessions while the agent is working we
	// want to make sure the animations are shown. Gate on the agent actually
	// being busy: a session that was killed mid-generation can persist an
	// assistant message with no Finish part, which still reports isSpinning()
	// even though nothing is running. Starting animations for it here would
	// leave a ghost "working" spinner (and a second one alongside any tool
	// spinner) after the session is reloaded.
	//
	// A running background agent is the exception: it works while the
	// session is idle, so its spinner starts whatever the busy state says.
	busy := m.isSessionBusy()
	for _, item := range items {
		if !busy && !backgroundAgentRunning(item) {
			continue
		}
		if animatable, ok := item.(chat.Animatable); ok {
			if cmd := animatable.StartAnimation(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}

	if cmd := m.chat.SetMessages(items...); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.chat.ImageTransmitCmds(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.chat.RestartPausedVisibleAnimations(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	m.chat.SelectLast()
	return tea.Sequence(cmds...)
}

// maybeReportImageSupport reports client image support at most once
// per session, the first time the terminal is confirmed to support
// Kitty graphics Unicode placeholders (virtual placement) — the
// basic Kitty graphics query response alone is not enough, since
// terminals such as VS Code's integrated terminal and WezTerm answer
// it OK without rendering placeholders correctly. If the terminal
// only clears the basic check, it logs a one-time explanation of why
// the built-in image tools stay disabled.
func (m *UI) maybeReportImageSupport() tea.Cmd {
	if m.caps.SupportsKittyPlaceholders() {
		if m.imageSupportReported {
			return nil
		}
		m.imageSupportReported = true
		return m.reportClientImageSupport()
	}
	if m.caps.SupportsKittyGraphics() && !m.placeholderMismatchLogged {
		m.placeholderMismatchLogged = true
		slog.Info("Terminal supports Kitty graphics but not Unicode placeholders; built-in image tools stay disabled",
			"terminal", m.caps.TerminalVersion,
			"term_program", m.caps.Env.Getenv("TERM_PROGRAM"))
	}
	return nil
}

// reportClientImageSupport returns a fire-and-forget tea.Cmd that
// tells the workspace this client can render images, once the Kitty
// graphics Unicode placeholder handshake has confirmed real terminal
// support. This is what lets the backend register the built-in
// image generation/editing tools. Errors are logged only: the
// report is best-effort background bookkeeping and never surfaces to
// the user.
func (m *UI) reportClientImageSupport() tea.Cmd {
	return func() tea.Msg {
		if err := m.com.Workspace.SetClientImageSupport(context.Background(), true); err != nil {
			slog.Warn("Failed to report client image support", "error", err)
		}
		return nil
	}
}

// applyImageCaps propagates the terminal's current image-rendering
// capabilities to the chat and returns a command that (re)starts any
// image transmissions those capabilities newly make possible: Kitty
// support just confirmed, the per-cell pixel size just resolved, or a
// resize changing the grid an inline image is sized for.
func (m *UI) applyImageCaps() tea.Cmd {
	_, tmux := m.caps.Env.LookupEnv("TMUX")
	cellW, cellH := m.caps.CellSize()
	m.chat.SetImageCaps(chat.ImageCaps{
		Kitty: m.caps.SupportsKittyPlaceholders(),
		Tmux:  tmux,
		Cell:  fimage.CellSize{Width: cellW, Height: cellH},
	})
	return m.chat.ImageTransmitCmds()
}

// loadNestedToolCalls recursively loads nested tool calls for the agent tool.
func (m *UI) loadNestedToolCalls(items []chat.MessageItem) {
	for _, item := range items {
		nestedContainer, ok := item.(chat.NestedToolContainer)
		if !ok {
			continue
		}
		toolItem, ok := item.(chat.ToolMessageItem)
		if !ok {
			continue
		}

		tc := toolItem.ToolCall()
		messageID := toolItem.MessageID()

		// Get the agent tool session ID.
		agentSessionID := m.com.Workspace.CreateAgentToolSessionID(messageID, tc.ID)

		// Fetch nested messages.
		nestedMsgs, err := m.com.Workspace.ListMessages(context.Background(), agentSessionID)
		if err != nil || len(nestedMsgs) == 0 {
			continue
		}

		// Build tool result map for nested messages.
		nestedMsgPtrs := make([]*message.Message, len(nestedMsgs))
		for i := range nestedMsgs {
			nestedMsgPtrs[i] = &nestedMsgs[i]
		}
		nestedToolResultMap := chat.BuildToolResultMap(nestedMsgPtrs)

		// Same predicate as the top level, asked about the sub-session:
		// executorForSession answers false for a child session this
		// process never dispatched, which is exactly the question.
		nestedRunActive := false
		if hasOrphanedCall(nestedMsgPtrs, nestedToolResultMap) {
			nestedRunActive = m.com.Workspace.AgentIsSessionBusy(agentSessionID)
		}
		nestedLastAssistant := lastAssistantIndex(nestedMsgPtrs)

		// Extract nested tool items.
		var nestedTools []chat.ToolMessageItem
		for i, nestedMsg := range nestedMsgPtrs {
			nestedItems := chat.ExtractMessageItems(m.com.Styles, nestedMsg, nestedToolResultMap, m.com.Workspace.WorkingDir(), nestedRunActive && i == nestedLastAssistant)
			for _, nestedItem := range nestedItems {
				if nestedToolItem, ok := nestedItem.(chat.ToolMessageItem); ok {
					// Mark nested tools as simple (compact) rendering.
					if simplifiable, ok := nestedToolItem.(chat.Compactable); ok {
						simplifiable.SetCompact(true)
					}
					nestedTools = append(nestedTools, nestedToolItem)
				}
			}
		}

		// Recursively load nested tool calls for any agent tools within.
		nestedMessageItems := make([]chat.MessageItem, len(nestedTools))
		for i, nt := range nestedTools {
			nestedMessageItems[i] = nt
		}
		m.loadNestedToolCalls(nestedMessageItems)

		// Set nested tools on the parent.
		nestedContainer.SetNestedTools(nestedTools)
		nestedContainer.SetTiming(nestedMsgs[0].CreatedAt, nestedMsgs[len(nestedMsgs)-1].CreatedAt)
	}
}

// appendSessionMessage appends a new message to the current session in the chat
// if the message is a tool result it will update the corresponding tool call message
//
// Items built here always count as active: this path only runs in the
// process carrying the run, driven by its event stream, which delivers the
// results as they land.
func (m *UI) appendSessionMessage(msg message.Message) tea.Cmd {
	var cmds []tea.Cmd

	existing := m.chat.MessageItem(msg.ID)
	if existing != nil {
		// message already exists, skip
		return nil
	}

	switch msg.Role {
	case message.User:
		// Persisted reminders are harness-internal nudges kept in the
		// transcript for prompt caching; they were never typed by the
		// user and must not render as a message from them.
		if msg.IsReminder() {
			// A notice inside it says a background agent ended. The busy
			// probe that follows every created message covers the wake
			// turn this may have started.
			applyBackgroundOutcomes(m.chat.AgentToolItems(), noticeOutcomes(&msg))
			return nil
		}
		// Shell commands are rendered live via shellResultMsg; skip
		// the persisted duplicate.
		hasShellCmd := false
		for _, part := range msg.Parts {
			if _, ok := part.(message.ShellCommand); ok {
				hasShellCmd = true
				break
			}
		}
		if hasShellCmd {
			return nil
		}
		m.lastUserMessageTime = msg.CreatedAt
		items := chat.ExtractMessageItems(m.com.Styles, &msg, nil, m.com.Workspace.WorkingDir(), true)
		for _, item := range items {
			if animatable, ok := item.(chat.Animatable); ok {
				if cmd := animatable.StartAnimation(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}
		m.chat.AppendMessages(items...)
		if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case message.Assistant:
		items := chat.ExtractMessageItems(m.com.Styles, &msg, nil, m.com.Workspace.WorkingDir(), true)
		for _, item := range items {
			if animatable, ok := item.(chat.Animatable); ok {
				if cmd := animatable.StartAnimation(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}
		m.chat.AppendMessages(items...)
		for _, tc := range msg.ToolCalls() {
			m.observeToolCall(tc)
		}
		if m.chat.Follow() {
			if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		if msg.FinishPart() != nil && msg.FinishPart().Reason == message.FinishReasonEndTurn {
			infoItem := chat.NewAssistantInfoItem(m.com.Styles, &msg, m.com.Config(), time.Unix(m.lastUserMessageTime, 0))
			m.chat.AppendMessages(infoItem)
			if m.chat.Follow() {
				if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}
	case message.System:
		items := chat.ExtractMessageItems(m.com.Styles, &msg, nil, m.com.Workspace.WorkingDir(), true)
		if len(items) == 0 {
			break
		}
		m.chat.AppendMessages(items...)
		if m.chat.Follow() {
			if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	case message.Tool:
		for _, tr := range msg.ToolResults() {
			toolItem := m.chat.MessageItem(tr.ToolCallID)
			if toolItem == nil {
				// we should have an item!
				continue
			}
			if toolMsgItem, ok := toolItem.(chat.ToolMessageItem); ok {
				toolMsgItem.SetResult(&tr)
				if cmd := m.chat.ImageTransmitCmds(); cmd != nil {
					cmds = append(cmds, cmd)
				}
				if m.chat.Follow() {
					if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			}
		}
	}
	return tea.Sequence(cmds...)
}

// updateSessionMessage updates an existing message in the current session in
// the chat when an assistant message is updated it may include updated tool
// calls as well that is why we need to handle creating/updating each tool call
// message too.
func (m *UI) updateSessionMessage(msg message.Message) tea.Cmd {
	var cmds []tea.Cmd
	existingItem := m.chat.MessageItem(msg.ID)

	if existingItem != nil {
		if assistantItem, ok := existingItem.(*chat.AssistantMessageItem); ok {
			// SetMessage returns a StartAnimation Cmd when the message
			// transitions back to spinning (e.g. its streamed content was
			// reset for a retry). Propagate it so the spinner re-arms
			// instead of freezing.
			if cmd := assistantItem.SetMessage(&msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}

	shouldRenderAssistant := chat.ShouldRenderAssistantMessage(&msg)
	isEndTurn := msg.FinishPart() != nil && msg.FinishPart().Reason == message.FinishReasonEndTurn
	// If the message of the assistant does not have any response just tool
	// calls we need to remove it, but keep the info item for end-of-turn
	// renders so the footer (model/provider/duration) remains visible when,
	// for example, a hook halts the turn.
	if !shouldRenderAssistant && len(msg.ToolCalls()) > 0 && existingItem != nil {
		m.chat.RemoveMessage(msg.ID)
		if !isEndTurn {
			if infoItem := m.chat.MessageItem(chat.AssistantInfoID(msg.ID)); infoItem != nil {
				m.chat.RemoveMessage(chat.AssistantInfoID(msg.ID))
			}
		}
	}

	if isEndTurn {
		if infoItem := m.chat.MessageItem(chat.AssistantInfoID(msg.ID)); infoItem == nil {
			newInfoItem := chat.NewAssistantInfoItem(m.com.Styles, &msg, m.com.Config(), time.Unix(m.lastUserMessageTime, 0))
			m.chat.AppendMessages(newInfoItem)
		}
	}

	// A provider-level retry can reset this message's streamed content
	// mid-step (see message.Message.ResetStreamedContent), wiping tool
	// calls the model had already started emitting. Drop any tool item
	// this message no longer claims before reconciling the tool calls
	// it currently has, so a retried call doesn't leave its predecessor
	// stuck on screen next to the fresh one.
	keepToolIDs := make(map[string]struct{}, len(msg.ToolCalls()))
	for _, tc := range msg.ToolCalls() {
		keepToolIDs[tc.ID] = struct{}{}
	}
	m.chat.RemoveOrphanedToolCalls(msg.ID, keepToolIDs)

	var items []chat.MessageItem
	for _, tc := range msg.ToolCalls() {
		existingToolItem := m.chat.MessageItem(tc.ID)
		if toolItem, ok := existingToolItem.(chat.ToolMessageItem); ok {
			existingToolCall := toolItem.ToolCall()
			// only update if finished state changed or input changed
			// to avoid clearing the cache
			if (tc.Finished && !existingToolCall.Finished) || tc.Input != existingToolCall.Input {
				toolItem.SetToolCall(tc)
			}
		}
		if existingToolItem == nil {
			items = append(items, chat.NewToolMessageItem(m.com.Styles, msg.ID, tc, nil, false, m.com.Workspace.WorkingDir()))
			m.observeToolCall(tc)
		}
	}

	for _, item := range items {
		if animatable, ok := item.(chat.Animatable); ok {
			if cmd := animatable.StartAnimation(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}

	m.chat.AppendMessages(items...)
	if m.chat.Follow() {
		if cmd := m.chat.ScrollToBottomAndSelectLast(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	return tea.Sequence(cmds...)
}

// observeToolCall records the moment a newly seen tool call started, so
// the turn status line can report its own running time later if it turns
// out to be slow. The Agent tool is excluded: it runs a whole nested
// turn, so a long duration there is expected rather than a sign of
// stalling.
func (m *UI) observeToolCall(tc message.ToolCall) {
	if tc.Name == toolnames.Agent {
		return
	}
	m.activeTool = &toolTiming{id: tc.ID, since: time.Now()}
}

// isWritingText reports whether a child session message is the sub-agent
// streaming reply text. Once the same message grows a tool call, the call
// is what the sub-agent is doing, and once it finishes there is nothing
// left to stream, so unfinished text alone is the signal.
func isWritingText(msg message.Message) bool {
	return msg.Role == message.Assistant &&
		strings.TrimSpace(msg.Content().Text) != "" &&
		len(msg.ToolCalls()) == 0 &&
		msg.FinishPart() == nil
}

// handleChildSessionMessage handles messages from child sessions (agent tools).
func (m *UI) handleChildSessionMessage(event pubsub.Event[message.Message]) tea.Cmd {
	var cmds []tea.Cmd

	// Check if this is an agent tool session and parse it.
	childSessionID := event.Payload.SessionID
	_, toolCallID, ok := m.com.Workspace.ParseAgentToolSessionID(childSessionID)
	if !ok {
		return nil
	}

	// The agent tool's call ID keys the chat's index map directly, so one
	// lookup finds the block that owns this child session.
	item := m.chat.MessageItem(toolCallID)
	if item == nil {
		return nil
	}
	agentItem, ok := item.(chat.NestedToolContainer)
	if !ok {
		return nil
	}

	// Reply text streams in as updates to a message with no tool call, so
	// it has to be read before the tool-only filter below drops it.
	agentItem.SetWriting(isWritingText(event.Payload))

	// Only messages with tool calls or results change the nested tools.
	if len(event.Payload.ToolCalls()) == 0 && len(event.Payload.ToolResults()) == 0 {
		return nil
	}

	// Child messages are the only clock this run has: neither ToolCall nor
	// ToolResult carries a timestamp of its own.
	agentItem.MarkActivity(event.Payload.CreatedAt)

	// Get existing nested tools.
	nestedTools := agentItem.NestedTools()

	// Update or create nested tool calls.
	for _, tc := range event.Payload.ToolCalls() {
		found := false
		for _, existingTool := range nestedTools {
			if existingTool.ToolCall().ID == tc.ID {
				existingTool.SetToolCall(tc)
				found = true
				break
			}
		}
		if !found {
			// Create a new nested tool item. Animation is driven by the
			// parent AgentToolMessageItem's tick chain via AdvanceFrame,
			// so we do not start an independent tick chain here.
			nestedItem := chat.NewToolMessageItem(m.com.Styles, event.Payload.ID, tc, nil, false, m.com.Workspace.WorkingDir())
			if simplifiable, ok := nestedItem.(chat.Compactable); ok {
				simplifiable.SetCompact(true)
			}
			nestedTools = append(nestedTools, nestedItem)
		}
	}

	// Update nested tool results.
	for _, tr := range event.Payload.ToolResults() {
		for _, nestedTool := range nestedTools {
			if nestedTool.ToolCall().ID == tr.ToolCallID {
				nestedTool.SetResult(&tr)
				break
			}
		}
	}

	// Update the agent item with the new nested tools.
	agentItem.SetNestedTools(nestedTools)

	// Update the chat so it updates the index map for animations to work as expected
	m.chat.UpdateNestedToolIDs(toolCallID)

	if m.chat.Follow() {
		if cmd := m.chat.ScrollToBottomAndSelectLast(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	return tea.Sequence(cmds...)
}

// handleLoadSession handles loadSessionMsg.
func (m *UI) handleLoadSession(msg loadSessionMsg) []tea.Cmd {
	var cmds []tea.Cmd
	// Sub-session navigation: push/pop deferred to here so a
	// failed load (which sends ReportError, not loadSessionMsg)
	// never touches the stack.
	if msg.enterFrame != nil {
		m.sessionStack = append(m.sessionStack, *msg.enterFrame)
	}
	if msg.leaveLevel && len(m.sessionStack) > 0 {
		m.sessionStack = m.sessionStack[:len(m.sessionStack)-1]
	}
	if msg.truncateStackTo != nil && *msg.truncateStackTo <= len(m.sessionStack) {
		m.sessionStack = m.sessionStack[:*msg.truncateStackTo]
	}
	if msg.clearStack {
		m.sessionStack = nil
	}
	if msg.draftAfter != nil {
		m.applyDraft(*msg.draftAfter)
	}
	if m.forceCompactMode {
		m.isCompact = true
	}
	focus := m.focus
	isBranch := msg.isBranch
	if msg.session.ParentSessionID != "" && !isBranch {
		// The editor is closed off down here, so leaving focus in it
		// would strand the user in a box that cannot take input. A
		// branch keeps its editor, so it keeps the focus too.
		focus = uiFocusMain
		m.textarea.Blur()
		m.chat.Focus()
	}
	m.setState(uiChat, focus)
	switched := !m.hasSession() || m.session.ID != msg.session.ID
	m.session = msg.session
	m.sessionIsBranch = isBranch
	m.sessionFiles = msg.files
	// Session switch: the memoized busy state and queued prompts
	// belong to the previous session. Drop them and re-fetch
	// off-thread so the queue pill and esc behavior track the new
	// session instead of a stale one.
	m.invalidateBusyCaches()
	if switched {
		// NOTE: Invalidating keeps the value, and the probe for the new
		// session may be a cycle away. Until it lands the old session's
		// answer would gate the new transcript's animations.
		m.sessionBusyCache = ttlCache[bool]{}
		m.backgroundAgentsCache = ttlCache[int]{}
		// NOTE: The previous transcript would otherwise stay on screen,
		// and be laid out at the new size, until the new one arrives.
		m.chat.ClearMessages()
	}
	m.invalidatePromptQueue()
	m.promptQueue = 0
	m.promptQueueItems = nil
	m.promptQueueCheckedAt = time.Time{}
	if cmd := m.restoreStashedPrompts(msg.session.ID); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.dispatchBusyRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.dispatchPromptQueueRefresh(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	cmds = append(cmds, m.startLSPs(msg.lspFilePaths()))
	cmds = append(cmds, m.loadSessionMessagesCmd(m.session.ID))
	if cmd := m.syncTurnSpinner(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	// Reload prompt history for the new session.
	m.historyReset()
	cmds = append(cmds, m.loadPromptHistory())
	m.updateLayoutAndSize()
	return cmds
}

// handleSessionMessages handles sessionMessagesMsg.
func (m *UI) handleSessionMessages(msg sessionMessagesMsg) []tea.Cmd {
	var cmds []tea.Cmd
	// Drop a load the user has already navigated away from,
	// otherwise the previous session's transcript lands in the
	// chat that replaced it.
	if msg.sessionID != m.currentSessionID() {
		return cmds
	}
	m.clearSessionLoading()
	m.lastUserMessageTime = msg.lastUserMessageTime
	if cmd := m.applySessionItems(msg.items); cmd != nil {
		cmds = append(cmds, cmd)
	}
	// NOTE: Without warming, the first scroll computes the total height
	// of the whole transcript in one blocking pass to size the
	// scrollbar. The layout runs first so the list takes the
	// scrollbar-adjusted width now; otherwise the layout pass at warm
	// completion changes the width and drops everything just warmed.
	m.updateLayoutAndSize()
	cmds = append(cmds, m.chat.BeginWarm())
	return cmds
}
