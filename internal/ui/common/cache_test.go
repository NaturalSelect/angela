package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCacheHitRate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		read          int64
		creation      int64
		uncachedInput int64
		wantPct       float64
		wantOK        bool
	}{
		{"no cache activity yet", 0, 0, 0, 0, false},
		{"all reads is a perfect hit rate", 100, 0, 0, 100, true},
		{"all creation is a zero hit rate", 0, 100, 0, 0, true},
		{"half and half reads vs creation", 50, 50, 0, 50, true},
		{"two thirds reads vs creation", 2, 1, 0, float64(2) / float64(3) * 100, true},
		{"openai style: uncached input lowers rate", 75, 0, 25, 75, true},
		{"all uncached input is zero hit rate", 0, 0, 100, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pct, ok := CacheHitRate(tt.read, tt.creation, tt.uncachedInput)
			require.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				require.Equal(t, tt.wantPct, pct)
			}
		})
	}
}
