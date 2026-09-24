package model

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/util"
)

// CacheDistribution summarizes the per-step cache-hit rate across a
// session's qualifying assistant steps: those with a Finish part
// carrying per-step cache usage — the same eligibility guard
// common.StepCacheHitRate applies to a single step.
type CacheDistribution struct {
	// QualifyingSteps and TotalSteps let callers report how many of
	// the session's assistant steps the distribution is based on.
	QualifyingSteps int
	TotalSteps      int
	Min             float64
	// P10 sits near the low end of the distribution (90% of steps hit
	// the cache at or above this rate), so it is the useful one for
	// spotting a degraded tail; P90 sits near the high end instead.
	P10    float64
	Median float64
	P90    float64
	Max    float64
}

// computeCacheDistribution reports the cache-hit-rate distribution
// across msgs' qualifying assistant steps, alongside how many of the
// session's assistant steps qualified. ok is false when none
// qualify, in which case dist carries no usable rate data
// (TotalSteps is still set).
func computeCacheDistribution(msgs []*message.Message) (dist CacheDistribution, ok bool) {
	var rates []float64
	for _, msg := range msgs {
		if msg == nil || msg.Role != message.Assistant {
			continue
		}
		dist.TotalSteps++
		if rate, rateOK := common.StepCacheHitRate(msg); rateOK {
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

// cacheNoticeSeq provides unique IDs for cache-hit distribution
// notices, mirroring tpsNoticeSeq, so running the command more than
// once in the same session does not collide with the previous
// notice's item ID.
var cacheNoticeSeq atomic.Int64

// cacheComputedMsg carries the result of computing a session's
// cache-hit-rate distribution, fetched off the Update goroutine.
type cacheComputedMsg struct {
	sessionID      string
	dist           CacheDistribution
	ok             bool
	readTokens     int64
	creationTokens int64
	uncachedTokens int64
}

// showCache fetches the current session's messages and reports the
// cache-hit-rate distribution across its qualifying assistant steps
// as an in-memory chat notice, mirroring showTPS.
func (m *UI) showCache() tea.Cmd {
	if !m.hasSession() {
		return nil
	}

	sessionID := m.session.ID
	readTokens, creationTokens, uncachedTokens := m.session.CacheReadTokens, m.session.CacheCreationTokens, m.session.UncachedInputTokens
	return func() tea.Msg {
		msgs, err := m.com.Workspace.ListMessages(context.Background(), sessionID)
		if err != nil {
			return util.ReportError(err)()
		}
		msgPtrs := make([]*message.Message, len(msgs))
		for i := range msgs {
			msgPtrs[i] = &msgs[i]
		}
		dist, ok := computeCacheDistribution(msgPtrs)
		return cacheComputedMsg{
			sessionID:      sessionID,
			dist:           dist,
			ok:             ok,
			readTokens:     readTokens,
			creationTokens: creationTokens,
			uncachedTokens: uncachedTokens,
		}
	}
}

// appendCacheNotice appends dist (or a "not enough data" notice when
// ok is false) to the chat as an in-memory notice, mirroring
// appendTPSNotice: the snapshot is never written to the session's
// message history, so running the command again does not clutter the
// transcript on reload.
func (m *UI) appendCacheNotice(dist CacheDistribution, ok bool, readTokens, creationTokens, uncachedTokens int64) tea.Cmd {
	t := m.com.Styles
	item := chat.NewSystemNoticeItem(t, &message.Message{
		ID:   fmt.Sprintf("cache-notice-%d", cacheNoticeSeq.Add(1)),
		Role: message.System,
		Parts: []message.ContentPart{
			message.TextContent{Text: formatCacheDistribution(dist, ok, readTokens, creationTokens, uncachedTokens)},
		},
	})
	m.chat.AppendMessages(item)
	return m.chat.ScrollToBottomAndAnimate()
}

// cacheBarLine renders one "label: [bar] N.N%" distribution row,
// scaled against a fixed 100% ceiling (unlike tpsBarLine, which
// scales against the distribution's own max) since a cache-hit rate
// is already a percentage with a natural upper bound.
func cacheBarLine(label string, value float64) string {
	return fmt.Sprintf("%-*s [%s] %.1f%%", tpsBarLabelWidth, label+":", tpsBar(value, 100), value)
}

// formatCacheDistribution renders a session-wide average cache-hit-rate
// line — computed via common.CacheHitRate from readTokens/creationTokens/
// uncachedTokens, the session's cumulative counters — followed by dist as
// a row of bar charts (one per statistic, each scaled against a fixed
// 100% ceiling), or a "not enough data yet" notice when ok is false. The
// average line reads "avg n/a" when the session has no usable cumulative
// data yet.
func formatCacheDistribution(dist CacheDistribution, ok bool, readTokens, creationTokens, uncachedTokens int64) string {
	avgLine := "avg n/a"
	if avgPct, avgOK := common.CacheHitRate(readTokens, creationTokens, uncachedTokens); avgOK {
		// cached tracks only readTokens, matching CacheHitRate's own
		// numerator: creation tokens are cache writes, not hits, so
		// including them here would make this figure disagree with
		// avgPct (e.g. "83% hit" next to a "92% cached" fraction).
		total := readTokens + creationTokens + uncachedTokens
		avgLine = fmt.Sprintf("avg %.2f%% cache hit (%s of %s input tokens read from cache)", avgPct, formatTokensCompact(readTokens), formatTokensCompact(total))
	}

	if !ok {
		if dist.TotalSteps == 0 {
			return avgLine + "\nNo assistant steps in this session yet."
		}
		return fmt.Sprintf(
			"%s\nNot enough data yet: 0 of %d assistant step(s) have cache usage recorded (steps without usage data excluded).",
			avgLine, dist.TotalSteps,
		)
	}
	rows := []string{
		cacheBarLine("min", dist.Min),
		cacheBarLine("p10", dist.P10),
		cacheBarLine("median", dist.Median),
		cacheBarLine("p90", dist.P90),
		cacheBarLine("max", dist.Max),
	}
	return fmt.Sprintf(
		"%s\nBased on %d of %d assistant steps (steps without usage data excluded)\n%s",
		avgLine, dist.QualifyingSteps, dist.TotalSteps, strings.Join(rows, "\n"),
	)
}
