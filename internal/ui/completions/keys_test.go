package completions

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKeyBindings(t *testing.T) {
	t.Parallel()

	km := DefaultKeyMap()
	got := km.KeyBindings()
	require.Len(t, got, 4)
	require.Equal(t, km.Down, got[0])
	require.Equal(t, km.Up, got[1])
	require.Equal(t, km.Select, got[2])
	require.Equal(t, km.Cancel, got[3])
}

func TestFullHelp(t *testing.T) {
	t.Parallel()

	km := DefaultKeyMap()
	help := km.FullHelp()
	require.Len(t, help, 1, "4 bindings fit in a single row of 4")
	require.Len(t, help[0], 4)
}

func TestShortHelp(t *testing.T) {
	t.Parallel()

	km := DefaultKeyMap()
	short := km.ShortHelp()
	require.Len(t, short, 2)
	require.Equal(t, km.Up, short[0])
	require.Equal(t, km.Down, short[1])
}
