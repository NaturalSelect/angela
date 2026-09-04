package list

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewSpacerItem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		height     int
		wantHeight int
	}{
		{name: "positive height subtracts one", height: 3, wantHeight: 2},
		{name: "height one yields zero", height: 1, wantHeight: 0},
		{name: "zero height clamps to zero", height: 0, wantHeight: 0},
		{name: "negative height clamps to zero", height: -5, wantHeight: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := NewSpacerItem(tt.height)
			require.Equal(t, tt.wantHeight, s.Height)
			require.True(t, s.Finished())
			require.Equal(t, uint64(0), s.Version())
		})
	}
}

func TestSpacerItem_Render(t *testing.T) {
	t.Parallel()

	s := NewSpacerItem(4) // Height becomes 3.
	out := s.Render(80)
	require.Equal(t, strings.Repeat("\n", 3), out)
}

func TestSpacerItem_RenderZeroHeight(t *testing.T) {
	t.Parallel()

	s := NewSpacerItem(0)
	require.Equal(t, "", s.Render(10))
}
