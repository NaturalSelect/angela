package common

// CacheHitRate reports the fraction of prompt-cache-eligible tokens
// served from the cache (reads) rather than newly written to it
// (creations), as a percentage. Plain, never-cached input tokens are
// excluded from both sides of the ratio:
// they were never subject to a cache lookup, so counting them would
// understate how effectively the eligible portion of the prompt is
// being reused.
//
// Like AverageTPS, this is a session-wide figure derived from
// cumulative counters (see session.Session.CacheReadTokens /
// CacheCreationTokens), not a per-step one.
func CacheHitRate(readTokens, creationTokens int64) (pct float64, ok bool) {
	total := readTokens + creationTokens
	if total <= 0 {
		return 0, false
	}
	return float64(readTokens) / float64(total) * 100, true
}
