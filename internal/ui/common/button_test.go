package common

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestButton_RendersLabelInEveryState(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	tests := []struct {
		name string
		opts ButtonOpts
	}{
		{name: "blurred", opts: ButtonOpts{Text: "OK"}},
		{name: "selected", opts: ButtonOpts{Text: "OK", Selected: true}},
		{name: "hovered", opts: ButtonOpts{Text: "OK", Hovered: true}},
		{name: "selected and hovered", opts: ButtonOpts{Text: "OK", Selected: true, Hovered: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out := Button(&sty, tt.opts)
			require.Contains(t, ansi.Strip(out), "OK")
		})
	}
}

func TestButton_UnderlineIndex(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	t.Run("valid index underlines a character without altering the text", func(t *testing.T) {
		t.Parallel()
		out := Button(&sty, ButtonOpts{Text: "Save", UnderlineIndex: 0})
		require.Contains(t, ansi.Strip(out), "Save")
	})

	t.Run("out-of-bounds index is ignored", func(t *testing.T) {
		t.Parallel()
		require.NotPanics(t, func() {
			out := Button(&sty, ButtonOpts{Text: "Hi", UnderlineIndex: 10})
			require.Contains(t, ansi.Strip(out), "Hi")
		})
	})
}

func TestButton_DefaultPaddingMatchesExplicitTwo(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	withDefault := Button(&sty, ButtonOpts{Text: "Hi"})
	withExplicit := Button(&sty, ButtonOpts{Text: "Hi", Padding: 2})
	require.Equal(t, withDefault, withExplicit)
}

func TestButtonGroup(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	t.Run("no buttons returns an empty string", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, ButtonGroup(&sty, nil, ""))
	})

	t.Run("default spacing joins every button", func(t *testing.T) {
		t.Parallel()
		out := ButtonGroup(&sty, []ButtonOpts{{Text: "A"}, {Text: "B"}}, "")
		stripped := ansi.Strip(out)
		require.Contains(t, stripped, "A")
		require.Contains(t, stripped, "B")
	})

	t.Run("custom spacing is honored", func(t *testing.T) {
		t.Parallel()
		out := ButtonGroup(&sty, []ButtonOpts{{Text: "A"}, {Text: "B"}}, "\n")
		require.True(t, strings.Contains(out, "\n"))
	})
}

func TestButtonHitCompositor(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	t.Run("nil for no buttons", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, ButtonHitCompositor(&sty, nil, "", 0, 0))
	})

	t.Run("hit testing maps a position back to the button index", func(t *testing.T) {
		t.Parallel()

		opts := []ButtonOpts{{Text: "A"}, {Text: "B"}}
		c := ButtonHitCompositor(&sty, opts, "  ", 0, 0)
		require.NotNil(t, c)

		require.Equal(t, 0, HitButtonIndex(c, 0, 0), "a hit inside the first button must resolve to index 0")

		firstWidth := lipgloss.Width(Button(&sty, opts[0]))
		require.Equal(t, 1, HitButtonIndex(c, firstWidth+2, 0), "a hit inside the second button must resolve to index 1")
	})
}

func TestHitButtonIndex(t *testing.T) {
	t.Parallel()

	require.Equal(t, -1, HitButtonIndex(nil, 0, 0), "a nil compositor reports no hit")

	sty := styles.CharmtonePantera()
	c := ButtonHitCompositor(&sty, []ButtonOpts{{Text: "A"}}, "  ", 0, 0)
	require.Equal(t, -1, HitButtonIndex(c, 999, 999), "a position far outside any button reports no hit")
}
