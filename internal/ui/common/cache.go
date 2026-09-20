package common

// CacheHitRate reports the fraction of all input tokens served from the
// cache, as a percentage. The denominator includes plain uncached input
// tokens (usage.InputTokens after provider normalisation) so that
// providers like OpenAI — which report cached tokens as CacheReadTokens
// and never set CacheCreationTokens — do not spuriously show 100%.
//
// Like AverageTPS, this is a session-wide figure derived from cumulative
// counters (see session.Session.CacheReadTokens / CacheCreationTokens /
// UncachedInputTokens), not a per-step one.
func CacheHitRate(readTokens, creationTokens, uncachedInputTokens int64) (pct float64, ok bool) {
	total := readTokens + creationTokens + uncachedInputTokens
	if total <= 0 {
		return 0, false
	}
	return float64(readTokens) / float64(total) * 100, true
}
