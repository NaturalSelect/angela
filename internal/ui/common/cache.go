package common

import "github.com/NaturalSelect/angela/internal/message"

// CacheHitRate reports the fraction of all input tokens served from the
// cache, as a percentage. The denominator includes plain uncached input
// tokens (usage.InputTokens after provider normalisation) so that
// providers like OpenAI — which report cached tokens as CacheReadTokens
// and never set CacheCreationTokens — do not spuriously show 100%.
//
// Like AverageTPS, this is a session-wide figure derived from cumulative
// counters (see session.Session.CacheReadTokens / CacheCreationTokens /
// UncachedInputTokens). See StepCacheHitRate for the per-step equivalent.
func CacheHitRate(readTokens, creationTokens, uncachedInputTokens int64) (pct float64, ok bool) {
	total := readTokens + creationTokens + uncachedInputTokens
	if total <= 0 {
		return 0, false
	}
	return float64(readTokens) / float64(total) * 100, true
}

// StepCacheHitRate reports the cache hit rate for a single agent step,
// as a percentage, using the same formula as CacheHitRate. It is
// ineligible (ok=false) when the message has no Finish part yet, or
// when the step's recorded token total is zero — which covers messages
// persisted before per-step cache tracking existed, and steps whose
// usage was only estimated (see SetFinishCacheUsage).
func StepCacheHitRate(msg *message.Message) (pct float64, ok bool) {
	if msg == nil {
		return 0, false
	}
	finish := msg.FinishPart()
	if finish == nil {
		return 0, false
	}
	return CacheHitRate(finish.CacheReadTokens, finish.CacheCreationTokens, finish.InputTokens)
}
