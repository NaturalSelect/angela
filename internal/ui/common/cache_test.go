package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCacheHitRate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		read     int64
		creation int64
		wantPct  int64
		wantOK   bool
	}{
		{"no cache activity yet", 0, 0, 0, false},
		{"all reads is a perfect hit rate", 100, 0, 100, true},
		{"all creation is a zero hit rate", 0, 100, 0, true},
		{"half and half", 50, 50, 50, true},
		{"rounds to the nearest point", 2, 1, 67, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pct, ok := CacheHitRate(tt.read, tt.creation)
			require.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				require.Equal(t, tt.wantPct, pct)
			}
		})
	}
}
