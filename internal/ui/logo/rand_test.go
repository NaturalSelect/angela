package logo

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCachedRandNInBounds(t *testing.T) {
	t.Parallel()

	for n := 1; n <= 12; n++ {
		got := cachedRandN(n)
		require.GreaterOrEqual(t, got, 0)
		require.Less(t, got, n)
	}
}

// TestCachedRandNIsStablePerN pins the caching contract: repeated calls
// with the same n must return the same value. This holds even under
// t.Parallel(), since the map only ever transitions from "unset" to a
// fixed value for a given key, never back.
func TestCachedRandNIsStablePerN(t *testing.T) {
	t.Parallel()

	const n = 137 // large, unlikely to collide with other tests' n values
	first := cachedRandN(n)
	for range 10 {
		require.Equal(t, first, cachedRandN(n))
	}
}
