package model

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/util"
)

// TPSDistribution summarizes the tokens/sec generation rate across a
// session's qualifying assistant steps: those with a Finish part
// reporting output tokens and a known generation duration — the same
// eligibility guard common.StepTPS applies to a single step.
type TPSDistribution struct {
	// QualifyingSteps and TotalSteps let callers report how many of
	// the session's assistant steps the distribution is based on.
	QualifyingSteps int
	TotalSteps      int
	Min             float64
	// P10 sits near the slow end of the distribution (90% of steps ran
	// at or above this rate), so it is the useful one for spotting a
	// degraded tail; P90 sits near the fast end instead.
	P10    float64
	Median float64
	P90    float64
	Max    float64
}

// computeTPSDistribution reports the tok/s distribution across msgs'
// qualifying assistant steps, alongside how many of the session's
// assistant steps qualified. ok is false when none qualify, in which
// case dist carries no usable rate data (TotalSteps is still set).
func computeTPSDistribution(msgs []*message.Message) (dist TPSDistribution, ok bool) {
	var rates []float64
	for _, msg := range msgs {
		if msg == nil || msg.Role != message.Assistant {
			continue
		}
		dist.TotalSteps++
		if rate, rateOK := common.StepTPSRate(msg); rateOK {
			rates = append(rates, rate)
		}
	}
	if len(rates) == 0 {
		return dist, false
	}

	slices.Sort(rates)
	dist.QualifyingSteps = len(rates)
	dist.Min = rates[0]
	dist.Max = rates[len(rates)-1]
	dist.Median = tpsMedian(rates)
	dist.P10 = rates[tpsPercentileIndex(len(rates), 0.1)]
	dist.P90 = rates[tpsPercentileIndex(len(rates), 0.9)]
	return dist, true
}

// tpsMedian returns the middle value of a sorted, non-empty slice,
// averaging the two middle values when its length is even.
func tpsMedian(sorted []float64) float64 {
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// tpsPercentileIndex returns the index of the pth percentile (0-1) in
// a sorted slice of length n, using a simple nearest-rank definition.
// This is a rough summary figure, not a statistically rigorous
// estimator.
func tpsPercentileIndex(n int, p float64) int {
	idx := int(math.Ceil(p*float64(n))) - 1
	return min(max(idx, 0), n-1)
}

// tpsNoticeSeq provides unique IDs for TPS distribution notices,
// mirroring todosNoticeSeq, so running the command more than once in
// the same session does not collide with the previous notice's item
// ID.
var tpsNoticeSeq atomic.Int64

// tpsComputedMsg carries the result of computing a session's TPS
// distribution, fetched off the Update goroutine.
type tpsComputedMsg struct {
	sessionID     string
	dist          TPSDistribution
	ok            bool
	avgTokens     int64
	avgDurationMs int64
}

// showTPS fetches the current session's messages and reports the
// tok/s distribution across its qualifying assistant steps as an
// in-memory chat notice. Like ActionUndo's preview
// (loadSessionMessagesCmd/undoPreviewMsg), the fetch is a round-trip
// in client/server mode, so it runs off the Update goroutine and
// reports back through tpsComputedMsg rather than mutating m.chat
// directly from within the command.
func (m *UI) showTPS() tea.Cmd {
	if !m.hasSession() {
		return nil
	}

	sessionID, avgTokens, avgDurationMs := m.session.ID, m.session.GenOutputTokens, m.session.GenDurationMs
	return func() tea.Msg {
		msgs, err := m.com.Workspace.ListMessages(context.Background(), sessionID)
		if err != nil {
			return util.ReportError(err)()
		}
		msgPtrs := make([]*message.Message, len(msgs))
		for i := range msgs {
			msgPtrs[i] = &msgs[i]
		}
		dist, ok := computeTPSDistribution(msgPtrs)
		return tpsComputedMsg{
			sessionID:     sessionID,
			dist:          dist,
			ok:            ok,
			avgTokens:     avgTokens,
			avgDurationMs: avgDurationMs,
		}
	}
}

// appendTPSNotice appends dist (or a "not enough data" notice when ok
// is false) to the chat as an in-memory notice, mirroring showTodos:
// the snapshot is never written to the session's message history, so
// running the command again does not clutter the transcript on
// reload.
func (m *UI) appendTPSNotice(dist TPSDistribution, ok bool, avgTokens, avgDurationMs int64) tea.Cmd {
	t := m.com.Styles
	item := chat.NewSystemNoticeItem(t, &message.Message{
		ID:   fmt.Sprintf("tps-notice-%d", tpsNoticeSeq.Add(1)),
		Role: message.System,
		Parts: []message.ContentPart{
			message.TextContent{Text: formatTPSDistribution(dist, ok, avgTokens, avgDurationMs)},
		},
	})
	m.chat.AppendMessages(item)
	return m.chat.ScrollToBottomAndAnimate()
}

// tpsBarWidth is the number of block characters in each distribution
// bar, chosen to stay readable in a narrow chat notice.
const tpsBarWidth = 20

// tpsBarLabelWidth pads every row's label (including its trailing
// colon) to the width of the longest one ("median:"), so every bar's
// opening bracket lines up in the monospace render.
const tpsBarLabelWidth = 7

// tpsBar renders value as a block bar scaled against maxRate, filling
// 0 to tpsBarWidth characters, e.g. "████████░░░░░░░░░░░░" for a
// value 40% of the way to maxRate. maxRate<=0 renders an empty bar,
// since there is nothing meaningful to scale against.
func tpsBar(value, maxRate float64) string {
	filled := 0
	if maxRate > 0 {
		filled = min(tpsBarWidth, max(0, int(math.Round(value/maxRate*tpsBarWidth))))
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", tpsBarWidth-filled)
}

// tpsBarLine renders one "label: [bar] N tok/s" distribution row.
func tpsBarLine(label string, value, maxRate float64) string {
	return fmt.Sprintf("%-*s [%s] %d tok/s", tpsBarLabelWidth, label+":", tpsBar(value, maxRate), int64(math.Round(value)))
}

// formatTPSDistribution renders a session-wide average tok/s line —
// computed via common.AverageTPS from avgTokens/avgDurationMs, the
// session's cumulative GenOutputTokens/GenDurationMs — followed by
// dist as a row of bar charts (one per statistic, each scaled against
// the session's max rate), or a "not enough data yet" notice when ok
// is false. The average line reads "avg n/a" when the session has no
// usable cumulative data yet.
func formatTPSDistribution(dist TPSDistribution, ok bool, avgTokens, avgDurationMs int64) string {
	avgLine := "avg n/a"
	if avgTPS, avgOK := common.AverageTPS(avgTokens, avgDurationMs); avgOK {
		duration := common.FormatDuration(time.Duration(avgDurationMs) * time.Millisecond)
		avgLine = fmt.Sprintf("avg %d tok/s (%d output tokens over %s)", avgTPS, avgTokens, duration)
	}

	if !ok {
		if dist.TotalSteps == 0 {
			return avgLine + "\nNo assistant steps in this session yet."
		}
		return fmt.Sprintf(
			"%s\nNot enough data yet: 0 of %d assistant step(s) qualify for a tok/s reading (steps without timing data excluded).",
			avgLine, dist.TotalSteps,
		)
	}
	rows := []string{
		tpsBarLine("min", dist.Min, dist.Max),
		tpsBarLine("p10", dist.P10, dist.Max),
		tpsBarLine("median", dist.Median, dist.Max),
		tpsBarLine("p90", dist.P90, dist.Max),
		tpsBarLine("max", dist.Max, dist.Max),
	}
	return fmt.Sprintf(
		"%s\nBased on %d of %d assistant steps (steps without timing data excluded)\n%s",
		avgLine, dist.QualifyingSteps, dist.TotalSteps, strings.Join(rows, "\n"),
	)
}
