package chat

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/agent"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/anim"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
)

// -----------------------------------------------------------------------------
// Agent Tool
// -----------------------------------------------------------------------------

// agentActionTargetWidth caps the tool target on the summary line so a long
// shell command cannot push the line past the prompt above it.
const agentActionTargetWidth = 40

// agentSummaryArrow marks the one-line summary beneath the task prompt.
const agentSummaryArrow = "↳ "

// agentTitleSeparator sits between the sub-agent's name and its task.
const agentTitleSeparator = " — "

// agentToolLabel is the bare tool name, shown until the call names the
// sub-agent it dispatches to.
const agentToolLabel = "Agent"

// agentToolTitle names the sub-agent a call dispatches to, so the header
// reads "Agent(explore)" rather than the bare tool name. Input that is
// still streaming in cannot be parsed yet; an omitted subagent_type
// dispatches to explore, mirroring the agent tool itself.
func agentToolTitle(toolCall message.ToolCall) string {
	var params agent.AgentParams
	if err := json.Unmarshal([]byte(toolCall.Input), &params); err != nil {
		return agentToolLabel
	}
	name := params.SubagentType
	if name == "" {
		// The field may simply not have streamed in yet, so only assume
		// the tool's own default once the input is complete.
		if !toolCall.Finished {
			return agentToolLabel
		}
		name = config.AgentExplore
	}
	return agentToolLabel + "(" + name + ")"
}

// NestedToolContainer is an interface for tool items that can contain nested tool calls.
type NestedToolContainer interface {
	NestedTools() []ToolMessageItem
	SetNestedTools(tools []ToolMessageItem)
	AddNestedTool(tool ToolMessageItem)
	SetTiming(startedAt, endedAt int64)
	MarkActivity(ts int64)
}

// AgentToolMessageItem is a message item that represents an agent tool call.
type AgentToolMessageItem struct {
	*baseToolMessageItem

	nestedTools []ToolMessageItem

	// startedAt and endedAt bracket the sub-agent's run, in Unix seconds.
	// They come from the child session's message timestamps because
	// message.ToolCall and message.ToolResult carry none of their own.
	startedAt int64
	endedAt   int64
}

var (
	_ ToolMessageItem     = (*AgentToolMessageItem)(nil)
	_ NestedToolContainer = (*AgentToolMessageItem)(nil)
)

// NewAgentToolMessageItem creates a new [AgentToolMessageItem].
func NewAgentToolMessageItem(
	sty *styles.Styles,
	toolCall message.ToolCall,
	result *message.ToolResult,
	canceled bool,
) *AgentToolMessageItem {
	t := &AgentToolMessageItem{}
	t.baseToolMessageItem = newBaseToolMessageItem(sty, toolCall, result, &AgentToolRenderContext{agent: t}, canceled)
	// For the agent tool we keep spinning until the tool call is finished.
	t.spinningFunc = func(state SpinningState) bool {
		return !state.HasResult() && !state.IsCanceled()
	}
	return t
}

// Animate progresses the message animation if it should be spinning.
//
// Bumps the parent's F6 list-cache version on both the parent-tick and
// nested-tick branches. Nested tools are not list entries of their
// own — their IDs map to this parent's index in idInxMap
// (internal/ui/model/chat.go:240-246) and their renders are embedded
// inline in this parent's output — so the list only checks the
// parent's version. Without the bump, the list cache would serve the
// previously rendered frame indefinitely and the spinner would appear
// frozen.
func (a *AgentToolMessageItem) Animate(msg anim.StepMsg) tea.Cmd {
	if a.result != nil || a.Status() == ToolStatusCanceled {
		return nil
	}
	if msg.ID == a.ID() {
		a.Bump()
		return a.anim.Animate(msg)
	}
	for _, nestedTool := range a.nestedTools {
		if msg.ID != nestedTool.ID() {
			continue
		}
		if s, ok := nestedTool.(Animatable); ok {
			a.Bump()
			return s.Animate(msg)
		}
	}
	return nil
}

// NestedTools returns the nested tools.
func (a *AgentToolMessageItem) NestedTools() []ToolMessageItem {
	return a.nestedTools
}

// SetNestedTools sets the nested tools.
//
// SetNestedTools always bumps the version. The previous design
// deduped when the slice's length and element pointers were
// unchanged, but the live update path in internal/ui/model/ui.go
// mutates existing children in place (SetToolCall / SetResult on the
// same pointers) and then calls SetNestedTools with the same slice.
// Pointer-equality dedupe in that case skips the parent Bump even
// though the parent's rendered output (which embeds the children
// inline) has changed, leaving a stale parent entry in the list
// cache. Always bumping is cheap (one uint64 increment) and called
// at most once per agent event; in the rare case the slice is
// truly unchanged the worst case is one extra parent re-render
// while every child cache hit stays warm.
func (a *AgentToolMessageItem) SetNestedTools(tools []ToolMessageItem) {
	a.nestedTools = tools
	a.clearCache()
	a.Bump()
}

// AddNestedTool adds a nested tool.
func (a *AgentToolMessageItem) AddNestedTool(tool ToolMessageItem) {
	// Mark nested tools as simple (compact) rendering.
	if s, ok := tool.(Compactable); ok {
		s.SetCompact(true)
	}
	a.nestedTools = append(a.nestedTools, tool)
	a.clearCache()
	a.Bump()
}

// SetTiming records when the sub-agent's run started and ended, in Unix
// seconds. A zero endedAt means it is still going.
//
// Unlike SetNestedTools this dedupes, because the whole state is the two
// values compared — there are no children mutated in place behind them.
func (a *AgentToolMessageItem) SetTiming(startedAt, endedAt int64) {
	if a.startedAt == startedAt && a.endedAt == endedAt {
		return
	}
	a.startedAt = startedAt
	a.endedAt = endedAt
	a.clearCache()
	a.Bump()
}

// MarkActivity widens the run's window to cover a child message seen at ts.
// The first sighting opens the window; each one after it moves the end.
//
// The live event path learns the timing one message at a time, so keeping
// "the start never moves forward" here spares every caller from tracking it.
func (a *AgentToolMessageItem) MarkActivity(ts int64) {
	if ts == 0 {
		return
	}
	start := a.startedAt
	if start == 0 || ts < start {
		start = ts
	}
	a.SetTiming(start, ts)
}

// currentAction names the newest nested tool — the one the sub-agent is
// working on, or the one it just finished.
func (a *AgentToolMessageItem) currentAction() string {
	if len(a.nestedTools) == 0 {
		return ""
	}
	tc := a.nestedTools[len(a.nestedTools)-1].ToolCall()
	if target := ToolCallTarget(tc, agentActionTargetWidth); target != "" {
		return tc.Name + " " + target
	}
	return tc.Name
}

// elapsed formats how long the sub-agent ran. It reports nothing while the
// run is still open or when the timestamps never arrived.
func (a *AgentToolMessageItem) elapsed() string {
	if a.startedAt == 0 || a.endedAt < a.startedAt {
		return ""
	}
	return common.FormatDuration(time.Duration(a.endedAt-a.startedAt) * time.Second)
}

// summaryLine condenses the run into one line: what the sub-agent is doing
// while it works, what it added up to once it is done. The tree below
// already lists every step, so the finished form reports totals rather
// than repeating the last one.
func (a *AgentToolMessageItem) summaryLine(sty *styles.Styles, done bool) string {
	if len(a.nestedTools) == 0 {
		return ""
	}

	text := a.currentAction()
	if done {
		label := "tools"
		if len(a.nestedTools) == 1 {
			label = "tool"
		}
		text = fmt.Sprintf("%d %s", len(a.nestedTools), label)
		if elapsed := a.elapsed(); elapsed != "" {
			text += " · " + elapsed
		}
	}
	if text == "" {
		return ""
	}
	// MarginLeft(2) lines the arrow up with the task tag above it.
	return sty.Tool.AgentPrompt.MarginLeft(2).Render(agentSummaryArrow + text)
}

// agentToolDescription is the short task label the dispatch carries. It
// falls back to the prompt's first line, since a caller may omit the
// description but never the prompt.
func agentToolDescription(toolCall message.ToolCall) string {
	var params agent.AgentParams
	if err := json.Unmarshal([]byte(toolCall.Input), &params); err != nil {
		return ""
	}
	if params.Description != "" {
		return params.Description
	}
	first, _, _ := strings.Cut(params.Prompt, "\n")
	return first
}

// AgentToolRenderContext renders agent tool messages.
type AgentToolRenderContext struct {
	agent *AgentToolMessageItem
}

// agentHeader draws the title line: status icon, the sub-agent's name, and
// the task it was given. It mirrors toolHeader but keys the icon on the
// tool's own name, because the title carries the sub-agent's name and would
// not match the icon table.
func (r *AgentToolRenderContext) agentHeader(sty *styles.Styles, width int, opts *ToolRenderOpts) string {
	icon := toolStatusStyle(sty, opts.Status).Render(toolKindIcon(toolnames.Agent))
	nameStyle := sty.Tool.NameNormal
	if opts.Compact {
		nameStyle = sty.Tool.NameNested
	}
	prefix := icon + " " + nameStyle.Render(agentToolTitle(opts.ToolCall))

	desc := agentToolDescription(opts.ToolCall)
	if desc == "" {
		return prefix
	}
	remaining := width - lipgloss.Width(prefix) - lipgloss.Width(agentTitleSeparator)
	if remaining <= 0 {
		return prefix
	}
	desc = ansi.Truncate(strings.ReplaceAll(desc, "\n", " "), remaining, "…")
	return prefix + sty.Tool.AgentPrompt.Render(agentTitleSeparator+desc)
}

// RenderTool implements the [ToolRenderer] interface.
//
// The block stays two lines: a title naming the sub-agent and its task, and
// a single summary line tracking progress. The sub-agent's own tool calls
// and its report live in its session, which enter opens — inlining them
// here buried the parent transcript under work the user did not ask to see.
func (r *AgentToolRenderContext) RenderTool(sty *styles.Styles, width int, opts *ToolRenderOpts) string {
	if !opts.ToolCall.Finished && !opts.IsCanceled() && len(r.agent.nestedTools) == 0 {
		return pendingTool(sty, agentToolTitle(opts.ToolCall), opts.Anim, opts.Compact)
	}

	header := r.agentHeader(sty, cappedMessageWidth(width), opts)
	if opts.Compact {
		return header
	}

	parts := []string{header}
	if summary := r.agent.summaryLine(sty, opts.HasResult() || opts.IsCanceled()); summary != "" {
		parts = append(parts, summary)
	}
	if !opts.HasResult() && !opts.IsCanceled() {
		parts = append(parts, "", opts.Anim.Render())
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}
