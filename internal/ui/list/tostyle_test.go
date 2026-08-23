package list

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

// lipgloss reports an attribute it was never given as [lipgloss.NoColor],
// which is opaque black rather than nil. Handing that to ultraviolet as a real
// color makes every cell drawn with the style paint a black patch, so chrome
// drawn over a surface stops inheriting the background underneath it. Anything
// the caller did set must still survive the conversion.
func TestToStyleLeavesUnsetColorsNil(t *testing.T) {
	t.Parallel()

	t.Run("unset colors become nil", func(t *testing.T) {
		t.Parallel()

		got := ToStyle(lipgloss.NewStyle())
		require.Nil(t, got.Fg, "an unset foreground must not become opaque black")
		require.Nil(t, got.Bg, "an unset background must not become opaque black")
	})

	t.Run("only the unset half is dropped", func(t *testing.T) {
		t.Parallel()

		got := ToStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000")))
		require.NotNil(t, got.Fg, "a foreground that was set must survive")
		require.Nil(t, got.Bg, "an unset background must stay nil")
	})

	t.Run("a deliberate black survives", func(t *testing.T) {
		t.Parallel()

		got := ToStyle(lipgloss.NewStyle().Background(lipgloss.Color("#000000")))
		require.NotNil(t, got.Bg,
			"a background the caller asked for must not be mistaken for an unset one")
	})
}
