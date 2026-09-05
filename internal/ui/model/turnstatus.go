package model

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
)

// narrowWidthBreakpoint is the width below which one-line chrome starts
// dropping optional fields.
const narrowWidthBreakpoint = 80

// turnStatusSeparator joins the fields of the status line.
const turnStatusSeparator = " · "

// maxActivityTargetWidth caps the tool target so a long shell command cannot
// crowd out every metric behind it.
const maxActivityTargetWidth = 32

// renderTurnStatus renders the single authoritative runtime line: what the
// agent is doing, how long it has taken, what it cost, and how to stop it.
//
// It reads memoized state only and must never probe the workspace: this runs
// on every frame, and the probes behind the workspace are synchronous HTTP
// round-trips in client/server mode.
func (m *UI) renderTurnStatus(width int) string {
	if width <= 0 || !m.hasSession() {
		return ""
	}
	if !m.isAgentBusy() {
		return m.renderIdleStatus(width)
	}

	spinner := m.com.Styles.TurnStatus.Spinner.Render(m.turnSpinnerFrame())
	fields := m.busyStatusFields()
	hint := m.renderTurnHint()

	return joinStatusLine(m.com.Styles, spinner, fields, hint, width)
}

// turnSpinnerFrame returns the current spinner glyph, falling back to a static
// icon when the animation tick isn't running.
func (m *UI) turnSpinnerFrame() string {
	if m.turnIsSpinning {
		return ansi.Strip(m.turnSpinner.View())
	}
	return styles.SpinnerIcon
}

// syncTurnSpinner starts the status spinner when a turn begins and stops it
// when the turn ends, returning the tick that drives the animation. It is a
// no-op while the spinner already matches the busy state, which is what keeps
// a second tick chain from starting.
func (m *UI) syncTurnSpinner() tea.Cmd {
	busy := m.isAgentBusy()
	if busy == m.turnIsSpinning {
		return nil
	}
	m.turnIsSpinning = busy
	if !busy {
		return nil
	}
	return m.turnSpinner.Tick
}

// busyStatusFields builds the busy-state fields in drop order: the last entry
// is sacrificed first when the line doesn't fit.
func (m *UI) busyStatusFields() []string {
	t := m.com.Styles

	fields := []string{t.TurnStatus.Activity.Render(m.currentActivity())}

	if elapsed := common.Elapsed(); elapsed != "" {
		fields = append(fields, t.TurnStatus.Field.Render(elapsed))
	}
	if done, total, ok := todoProgress(m.session.Todos); ok {
		fields = append(fields, t.TurnStatus.Field.Render(fmt.Sprintf("%s%d/%d", styles.TodoCompletedIcon, done, total)))
	}
	if usage := m.tokenUsageField(); usage != "" {
		fields = append(fields, t.TurnStatus.Field.Render(usage))
	}
	if cost := formatCost(m.session.Cost); cost != "" {
		fields = append(fields, t.TurnStatus.Field.Render(cost))
	}
	if m.promptQueue > 0 {
		fields = append(fields, t.TurnStatus.Field.Render(fmt.Sprintf("⋯%d queued", m.promptQueue)))
	}

	return fields
}

// currentActivity names what the agent is doing right now.
func (m *UI) currentActivity() string {
	if label := m.retryActivity(); label != "" {
		return label
	}
	tc, ok := m.chat.LastPendingTool()
	if !ok {
		return "Thinking"
	}
	label := tc.Name
	if target := chat.ToolCallTarget(tc, maxActivityTargetWidth); target != "" {
		label += " " + target
	}
	if slow := m.toolSlowness(tc); slow != "" {
		label += " (" + slow + ")"
	}
	return label
}

// retryStatus tracks an in-progress provider retry so the turn-status
// line can show it instead of a silent pause. It has no explicit
// "resolved" counterpart: retryActivity stops reporting it once the
// announced delay elapses, since by then the retried request is
// indistinguishable from ordinary thinking time.
type retryStatus struct {
	sessionID  string
	attempt    int
	maxAttempt int
	reason     string
	until      time.Time
}

// retryActivity reports the in-progress provider retry for the session
// in view, if any, so a slow-but-recovering connection doesn't read as
// a hang.
func (m *UI) retryActivity() string {
	rs := m.retryStatus
	if rs == nil || rs.sessionID != m.currentSessionID() {
		return ""
	}
	remaining := time.Until(rs.until)
	if remaining <= 0 {
		return ""
	}
	label := fmt.Sprintf("Retrying %d/%d", rs.attempt, rs.maxAttempt)
	if rs.reason != "" {
		label += ": " + ansi.Truncate(rs.reason, maxActivityTargetWidth, "…")
	}
	return label + " (" + common.FormatDuration(remaining) + ")"
}

// toolSlowThreshold is how long a tool call must be pending before its own
// running time is appended to the activity label. Most calls finish well
// under this, so the label only grows for the ones actually worth flagging.
const toolSlowThreshold = 5 * time.Second

// toolTiming remembers when the tool call currently named in the status
// line was first observed by observeToolCall, so toolSlowness can report
// how long it has actually been running.
type toolTiming struct {
	id    string
	since time.Time
}

// toolSlowness reports how long tc has been running once that exceeds
// toolSlowThreshold, so a slow tool or MCP call reads as still working
// instead of hung — there is deliberately no timeout on tool calls, so this
// running clock is the only signal the user gets. The Agent tool is
// excluded: it runs a whole nested turn, so a long duration there is
// expected rather than a sign of stalling.
func (m *UI) toolSlowness(tc message.ToolCall) string {
	if tc.Name == toolnames.Agent {
		return ""
	}
	if m.activeTool == nil || m.activeTool.id != tc.ID {
		return ""
	}
	elapsed := time.Since(m.activeTool.since)
	if elapsed < toolSlowThreshold {
		return ""
	}
	return common.FormatDuration(elapsed)
}

// renderTurnHint renders the right-hand escape hint. The three states mirror
// the cancel binding shown in the help line.
func (m *UI) renderTurnHint() string {
	desc := "stop"
	switch {
	case m.isCanceling:
		desc = "again to cancel"
	case m.promptQueue > 0:
		desc = "clear queue"
	}
	t := m.com.Styles
	return t.TurnStatus.HintKey.Render("esc") + t.TurnStatus.HintDesc.Render(" "+desc)
}

// renderIdleStatus renders the between-turns line: model, token/context use,
// cost.
func (m *UI) renderIdleStatus(width int) string {
	t := m.com.Styles

	var fields []string
	// A branch is the one place where leaving the session is a decision
	// rather than navigation, so the way out leads the line.
	if m.viewingBranch() {
		fields = append(fields, t.TurnStatus.Idle.Render("branch · merge to return · /abort to abandon"))
	}
	// The model name already sits on the prompt box's bottom border;
	// repeating it here only doubles the noise.
	if usage := m.tokenUsageField(); usage != "" {
		fields = append(fields, t.TurnStatus.Idle.Render(usage))
	}
	if cost := formatCost(m.session.Cost); cost != "" {
		fields = append(fields, t.TurnStatus.Idle.Render(cost))
	}
	if len(fields) == 0 {
		return ""
	}

	icon := t.TurnStatus.Idle.Render("◇")
	return joinStatusLine(t, icon, fields, "", width)
}

// tokenUsageField formats the running token count together with the
// percentage of the context window it fills, so the two numbers always
// appear side by side instead of one depending on whether a turn is
// in flight. The percentage is omitted until the context window size is
// known.
func (m *UI) tokenUsageField() string {
	tokens := m.session.PromptTokens + m.session.CompletionTokens
	if tokens <= 0 {
		return ""
	}
	usage := "⇣" + formatTokensCompact(tokens)
	if active := m.activeAgent(); active != nil {
		if pct := m.contextPercent(active.CatwalkCfg.ContextWindow); pct != "" {
			usage = pct + " " + usage
		}
	}
	return usage
}

// contextPercent formats how much of the context window the session fills.
func (m *UI) contextPercent(contextWindow int64) string {
	if contextWindow <= 0 {
		return ""
	}
	used := float64(m.session.CompletionTokens+m.session.PromptTokens) / float64(contextWindow) * 100
	pct := fmt.Sprintf("%d%%", int(used))
	if m.session.EstimatedUsage {
		pct = "~" + pct
	}
	return pct
}

// joinStatusLine lays out "<icon> <fields…>" flush left and the hint flush
// right, dropping fields from the tail until the line fits. When even the
// bare icon and first field overflow, the left segment is truncated.
func joinStatusLine(t *styles.Styles, icon string, fields []string, hint string, width int) string {
	sep := t.TurnStatus.Separator.Render(turnStatusSeparator)

	for {
		left := icon
		if len(fields) > 0 {
			left += " " + strings.Join(fields, sep)
		}

		if hint == "" {
			if ansi.StringWidth(left) <= width {
				return left
			}
		} else if gap := width - ansi.StringWidth(left) - ansi.StringWidth(hint); gap >= 1 {
			return left + strings.Repeat(" ", gap) + hint
		}

		if len(fields) > 1 {
			fields = fields[:len(fields)-1]
			continue
		}
		if hint != "" {
			hint = "" // the metrics matter more than the hint
			continue
		}
		return ansi.Truncate(left, width, "…")
	}
}

// todoProgress reports completed/total todos, and whether any exist.
func todoProgress(todos []session.Todo) (done, total int, ok bool) {
	if len(todos) == 0 {
		return 0, 0, false
	}
	for _, todo := range todos {
		if todo.Status == session.TodoStatusCompleted {
			done++
		}
	}
	return done, len(todos), true
}

// formatCost renders a turn cost, collapsing sub-cent amounts rather than
// showing a misleading "$0.00".
func formatCost(cost float64) string {
	switch {
	case cost <= 0:
		return ""
	case cost < 0.01:
		return "$<0.01"
	default:
		return fmt.Sprintf("$%.2f", cost)
	}
}

// formatTokensCompact abbreviates a token count for a one-line display.
func formatTokensCompact(tokens int64) string {
	switch {
	case tokens < 1000:
		return fmt.Sprintf("%d", tokens)
	case tokens < 1_000_000:
		return fmt.Sprintf("%.1fk", float64(tokens)/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(tokens)/1_000_000)
	}
}
