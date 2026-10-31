package agent

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/session"
)

func (a *sessionAgent) Summarize(ctx context.Context, sessionID string, compact resolvedAgent, opts fantasy.ProviderOptions, onAuthRefresh func(context.Context, *fantasy.ProviderError) error) error {
	_, err := a.summarizeAndResume(ctx, sessionID, compact, opts, onAuthRefresh)
	return err
}

// summarizeAndResume compacts the session, then runs the head of the
// session's queue. It returns the result of the run it started, or nil
// when nothing was queued. That run belongs to its own caller unless it
// is the resume of the turn that asked for the compaction, so a caller
// adopts the result only when it queued that resume itself.
func (a *sessionAgent) summarizeAndResume(ctx context.Context, sessionID string, compact resolvedAgent, opts fantasy.ProviderOptions, onAuthRefresh func(context.Context, *fantasy.ProviderError) error) (*fantasy.AgentResult, error) {
	if !compact.Available() {
		if compact.Err != nil {
			return nil, fmt.Errorf("compact agent unavailable: %w", compact.Err)
		}
		return nil, errors.New("compact agent unavailable")
	}

	// Claim the session before doing any I/O. Run holds the same
	// per-session lock across its own busy check and registration; doing
	// the check here and registering after two DB round-trips left a
	// window where a prompt and a summarize both passed the check.
	genCtx, cancel := context.WithCancel(ctx)
	ac := &activeCancel{cancel: cancel}

	sessMu := a.sessionMu(sessionID)
	sessMu.Lock()
	if a.IsSessionBusy(sessionID) {
		sessMu.Unlock()
		cancel()
		return nil, ErrSessionBusy
	}
	a.activeRequests.Set(sessionID, ac)
	sessMu.Unlock()

	defer a.activeRequests.CompareAndDelete(sessionID, ac)
	defer a.endThinking(ctx, sessionID, ac)
	defer cancel()
	a.setThinkingStart(ctx, sessionID, time.Now())

	systemPromptPrefix := compact.SystemPromptPrefix

	currentSession, err := a.sessions.Get(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	msgs, err := a.getSessionMessages(ctx, currentSession)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		// Nothing to summarize. Still drop any internal resume entry
		// queued ahead of this call (see enqueueResumeBeforeSummarize):
		// this early return never reaches the queue drain below, so
		// without this the entry would sit orphaned in the queue and
		// later fire as a stray turn.
		a.popResumeOnSummarizeFailure(sessionID)
		return nil, nil
	}

	aiMsgs, _ := a.preparePrompt(msgs, compact.Model.CatwalkCfg.SupportsImages)

	defer func() {
		if flushErr := a.messages.FlushAll(ctx); flushErr != nil {
			slog.Error("Failed to flush pending message updates after summarize", "error", flushErr)
		}
	}()

	agentOpts := []fantasy.AgentOption{
		fantasy.WithSystemPrompt(compact.SystemPrompt + compactSummaryFormatInstruction),
		fantasy.WithUserAgent(userAgent),
	}
	if compact.MaxTokens > 0 {
		agentOpts = append(agentOpts, fantasy.WithMaxOutputTokens(compact.MaxTokens))
	}
	agent := fantasy.NewAgent(compact.Model.Model, agentOpts...)
	summaryMessage, err := a.messages.Create(ctx, sessionID, message.CreateMessageParams{
		Role:             message.Assistant,
		Model:            compact.Model.ModelCfg.Model,
		Provider:         compact.Model.ModelCfg.Provider,
		Agent:            compact.ID,
		IsSummaryMessage: true,
	})
	if err != nil {
		return nil, err
	}

	summaryPromptText := buildSummaryPrompt(currentSession.Todos)
	if a.runningBackgroundAgents != nil {
		summaryPromptText += buildRunningBackgroundAgentsNote(a.runningBackgroundAgents(sessionID))
	}

	retryModel := &atomic.Pointer[fantasy.LanguageModel]{}
	retryModel.Store(&compact.Model.Model)

	streamCall := fantasy.AgentStreamCall{
		Prompt:          summaryPromptText,
		Messages:        aiMsgs,
		Headers:         sessionHeaders(sessionID),
		ProviderOptions: opts,
		OnAuthRefresh:   compactAuthRefreshRebuilding(sessionID, compact, onAuthRefresh, retryModel),
		ModelProvider: func() fantasy.LanguageModel {
			return *retryModel.Load()
		},
		PrepareStep: func(callContext context.Context, options fantasy.PrepareStepFunctionOptions) (_ context.Context, prepared fantasy.PrepareStepResult, err error) {
			prepared.Messages = options.Messages
			if systemPromptPrefix != "" {
				prepared.Messages = append([]fantasy.Message{fantasy.NewSystemMessage(systemPromptPrefix)}, prepared.Messages...)
			}
			return callContext, prepared, nil
		},
		OnReasoningDelta: func(id string, text string) error {
			summaryMessage.AppendReasoningContent(text)
			return a.messages.Update(genCtx, summaryMessage)
		},
		OnReasoningEnd: func(id string, reasoning fantasy.ReasoningContent) error {
			// Handle anthropic signature.
			if anthropicData, ok := reasoning.ProviderMetadata["anthropic"]; ok {
				if signature, ok := anthropicData.(*anthropic.ReasoningOptionMetadata); ok && signature.Signature != "" {
					summaryMessage.AppendReasoningSignature(signature.Signature)
				}
			}
			summaryMessage.FinishThinking()
			return a.messages.Update(genCtx, summaryMessage)
		},
		OnTextDelta: func(id, text string) error {
			summaryMessage.AppendContent(text)
			return a.messages.Update(genCtx, summaryMessage)
		},
	}

	var (
		resp       *fantasy.AgentResult
		extracted  string
		spentUsage fantasy.Usage
		spentCost  *float64
		billed     bool
	)
	// NOTE: Every response is paid for, adopted or not. Without this, the
	// attempts a retry throws away, or the ones of a compaction that fails
	// outright, would never reach the session cost.
	defer func() {
		if !billed && !usageIsZero(spentUsage) {
			a.chargeDiscardedSummary(context.WithoutCancel(ctx), compact.Model, sessionID, spentUsage, spentCost)
		}
	}()
	for attempt := 1; ; attempt++ {
		resp, err = agent.Stream(genCtx, streamCall)
		if err != nil {
			isCancelErr := errors.Is(err, context.Canceled)
			if isCancelErr {
				// User cancelled summarize we need to remove the summary
				// message. Return the cancellation itself (joined with any
				// delete failure) rather than swallowing it as success:
				// the caller (Run, for the auto-compaction path) needs a
				// non-nil, context.Canceled-wrapping error so it (a) drops
				// the internal resume entry instead of treating it as
				// still pending, and (b) reports this turn's RunComplete
				// as cancelled instead of a silent success.
				deleteErr := a.messages.Delete(ctx, summaryMessage.ID)
				return nil, errors.Join(context.Canceled, deleteErr)
			}
			// Mark the summary message as finished with an error so the UI
			// stops spinning.
			summaryMessage.AddFinish(message.FinishReasonError, "Summarization Error", err.Error())
			if updateErr := a.messages.Update(ctx, summaryMessage); updateErr != nil {
				return nil, updateErr
			}
			return nil, err
		}

		spentUsage = addUsage(spentUsage, resp.TotalUsage)
		spentCost = addOpenrouterCosts(spentCost, resp.Steps)

		if resp.Response.FinishReason == fantasy.FinishReasonLength {
			// Accepting this as SummaryMessageID would make the truncated
			// text the session's only memory of everything before it,
			// discarding the rest of the history the summary was supposed
			// to preserve. Not retried: the same prompt hits the same
			// output cap again.
			summaryMessage.AddFinish(message.FinishReasonMaxTokens, "Summarization Error", "the summary hit the output token limit before it finished")
			if updateErr := a.messages.Update(ctx, summaryMessage); updateErr != nil {
				return nil, updateErr
			}
			return nil, errors.New("summarization hit the output token limit")
		}

		var ok bool
		extracted, ok = extractSummaryTag(summaryMessage.Content().Text)
		if ok {
			break
		}

		// A model can ignore the compact prompt entirely and just keep
		// doing the task in plain prose, still finishing with a clean
		// end_turn. Nothing above would catch that: it isn't a length
		// cutoff, and fantasy reports the same finish reason either
		// way. The <summary> sentinel is what lets us tell the two
		// apart, and reject the ones that aren't a summary — instead
		// of adopting arbitrary task output as SummaryMessageID and
		// permanently discarding everything before it.
		if attempt >= summaryMaxAttempts {
			summaryMessage.AddFinish(message.FinishReasonError, "Summarization Error", "the model's response was not wrapped in "+summaryTagOpen+" tags")
			if updateErr := a.messages.Update(ctx, summaryMessage); updateErr != nil {
				return nil, updateErr
			}
			return nil, errors.New("summarization output missing " + summaryTagOpen + " tags")
		}

		// NOTE: Only the final attempt's text stays on the message, so the
		// retry does not concatenate onto the rejected output.
		slog.Warn("Summary was not wrapped in tags, retrying", "session_id", sessionID, "attempt", attempt, "max_attempts", summaryMaxAttempts)
		summaryMessage.ResetStreamedContent()
		if updateErr := a.messages.Update(genCtx, summaryMessage); updateErr != nil {
			return nil, updateErr
		}
	}
	summaryMessage.SetContent(extracted)

	summaryMessage.AddFinish(message.FinishReasonEndTurn, "", "")
	err = a.messages.Update(genCtx, summaryMessage)
	if err != nil {
		return nil, err
	}

	// NOTE: The row read before the stream is stale by now, and Save writes
	// the whole row back. Re-read it so cost added meanwhile, such as a
	// background agent finishing, is not overwritten.
	currentSession, err = a.sessions.Get(genCtx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	a.updateSessionUsage(compact.Model, &currentSession, spentUsage, spentCost, false)

	// Just in case, get just the last usage info.
	usage := resp.Response.Usage
	currentSession.SummaryMessageID = summaryMessage.ID
	currentSession.CompletionTokens = summaryCompletionTokens(usage, summaryMessage)
	currentSession.PromptTokens = 0
	currentSession.EstimatedUsage = usageIsZero(usage)
	_, err = a.sessions.Save(genCtx, currentSession)
	if err != nil {
		return nil, err
	}
	billed = true

	// Release the active request before processing queued messages so that
	// Run() does not see the session as busy. Conditional so a run that
	// has already taken the session over is not cancelled.
	a.endThinking(ctx, sessionID, ac)
	a.activeRequests.CompareAndDelete(sessionID, ac)
	cancel()

	// Process any messages that were queued while summarizing. Locked
	// only around the Get-and-Set pop, not the recursive Run below: a
	// concurrent takeUserQueue (e.g. the UI popping a user prompt back
	// to the editor while this summarize was still running) must not
	// have the entry it just took written back here.
	mu := a.sessionMu(sessionID)
	mu.Lock()
	queuedMessages, ok := a.messageQueue.Get(sessionID)
	if !ok || len(queuedMessages) == 0 {
		mu.Unlock()
		return nil, nil
	}
	firstQueuedMessage := queuedMessages[0]
	a.messageQueue.Set(sessionID, queuedMessages[1:])
	mu.Unlock()
	return a.Run(ctx, reresolve(ctx, firstQueuedMessage))
}

// buildSummaryPrompt constructs the prompt text for session summarization.
func buildSummaryPrompt(todos []session.Todo) string {
	var sb strings.Builder
	sb.WriteString("Provide a detailed summary of our conversation above.")
	if len(todos) > 0 {
		sb.WriteString("\n\n## Current Todo List\n\n")
		for _, t := range todos {
			fmt.Fprintf(&sb, "- [%s] %s\n", t.Status, t.Content)
		}
		sb.WriteString("\nInclude these tasks and their statuses in your summary. ")
		sb.WriteString("Instruct the resuming assistant to use the `todos` tool to continue tracking progress on these tasks.")
	}
	return sb.String()
}

// buildRunningBackgroundAgentsNote asks the summary to carry background
// agents that are still running across the compaction. Their completion
// notices arrive by agent id, so a summary that drops the ids leaves the
// resumed assistant unable to tell which result answers which request,
// and likely to launch the same work again.
func buildRunningBackgroundAgentsNote(running []backgroundAgentInfo) string {
	if len(running) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n\n## Running Background Agents\n\n")
	for _, info := range running {
		fmt.Fprintf(&sb, "- agentId %s (%s): %s\n", info.ID, info.AgentType, info.Task)
	}
	sb.WriteString("\nThese agents are still running. Record each agentId and what it was asked to find out, ")
	sb.WriteString("and tell the resuming assistant that every result will arrive in a notification on its own. ")
	sb.WriteString("The resuming assistant must not launch them again, wait for them, or poll for them.")
	return sb.String()
}
