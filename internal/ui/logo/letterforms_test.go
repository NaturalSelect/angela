package logo

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// wordmarkLetterforms spells ANGELA.
var wordmarkLetterforms = []letterform{LetterA, LetterN, LetterG, LetterE, LetterL, LetterA}

func TestLetterformsAreThreeRows(t *testing.T) {
	t.Parallel()

	for name, lf := range map[string]letterform{
		"A":    LetterA,
		"N":    LetterN,
		"G":    LetterG,
		"E":    LetterE,
		"L":    LetterL,
		"C":    LetterC,
		"EAlt": LetterEAlt,
		"H":    LetterH,
		"R":    LetterR,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Some letterforms carry a trailing blank line from their
			// heredoc; renderWord trims it, so measure the inked rows.
			lines := strings.Split(strings.TrimRight(lf(false), "\n \t"), "\n")
			require.Len(t, lines, 3, "letterform %s must be 3 rows tall", name)

			width := ansi.StringWidth(lines[0])
			for i, line := range lines {
				require.Equal(t, width, ansi.StringWidth(line),
					"letterform %s row %d is not padded to the block width", name, i)
			}
		})
	}
}

// TestLetterformStretchWidensBlock covers the stretch=true path through
// stretchLetterformPart, which every stretchable letterform routes
// through. Each letterform's minStretch always exceeds its unstretched
// width, so the stretched render must always be wider, regardless of the
// random amount chosen.
func TestLetterformStretchWidensBlock(t *testing.T) {
	t.Parallel()

	for name, lf := range map[string]letterform{
		"A": LetterA, "C": LetterC, "G": LetterG, "P": LetterP, "Y": LetterY,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			unstretched := ansi.StringWidth(strings.Split(lf(false), "\n")[0])
			stretched := ansi.StringWidth(strings.Split(lf(true), "\n")[0])
			require.Greater(t, stretched, unstretched,
				"letterform %s must widen when stretched", name)
		})
	}
}

// TestLetterN covers LetterN directly: it ignores its stretch argument
// (a diagonal cannot survive column repetition), so both calls must
// render identically.
func TestLetterNIgnoresStretch(t *testing.T) {
	t.Parallel()

	require.Equal(t, LetterN(false), LetterN(true))
}

func TestStretchLetterformPart(t *testing.T) {
	t.Parallel()

	t.Run("no stretch uses width copies", func(t *testing.T) {
		t.Parallel()

		got := stretchLetterformPart("x\n", letterformProps{
			stretch: false, width: 4, minStretch: 10, maxStretch: 20,
		})
		require.Equal(t, 4, ansi.StringWidth(strings.Split(got, "\n")[0]))
	})

	t.Run("stretch picks a width within [min, max)", func(t *testing.T) {
		t.Parallel()

		got := stretchLetterformPart("x\n", letterformProps{
			stretch: true, width: 1, minStretch: 5, maxStretch: 8,
		})
		w := ansi.StringWidth(strings.Split(got, "\n")[0])
		require.GreaterOrEqual(t, w, 5)
		require.Less(t, w, 8)
	})

	t.Run("swaps inverted min and max", func(t *testing.T) {
		t.Parallel()

		got := stretchLetterformPart("x\n", letterformProps{
			stretch: true, width: 1, minStretch: 9, maxStretch: 3,
		})
		w := ansi.StringWidth(strings.Split(got, "\n")[0])
		require.GreaterOrEqual(t, w, 3)
		require.Less(t, w, 9)
	})
}

// TestLetterformsPadConsistently covers letterforms whose block includes a
// fully blank trailing row (baked into one of their heredoc parts, e.g. an
// extra blank line before the closing backtick). Unlike the 3-row
// letterforms above, aggressively trimming trailing whitespace would eat
// into legitimate in-row padding, so this only checks that every row
// shares the same width and that the block carries visible ink somewhere.
func TestLetterformsPadConsistently(t *testing.T) {
	t.Parallel()

	for name, lf := range map[string]letterform{
		"P": LetterP, "U": LetterU, "Y": LetterY, "SAlt": LetterSAlt, "YAlt": LetterYAlt,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := strings.Split(strings.TrimRight(lf(false), "\n"), "\n")
			require.NotEmpty(t, lines)

			width := ansi.StringWidth(lines[0])
			hasInk := false
			for i, line := range lines {
				require.Equal(t, width, ansi.StringWidth(line),
					"letterform %s row %d is not padded to the block width", name, i)
				if strings.TrimSpace(line) != "" {
					hasInk = true
				}
			}
			require.True(t, hasInk, "letterform %s must render some visible ink", name)
		})
	}
}

// TestRenderWordNegativeSpacingClampsToZero covers renderWord's guard
// against a negative spacing argument.
func TestRenderWordNegativeSpacingClampsToZero(t *testing.T) {
	t.Parallel()

	require.Equal(t, renderWord(0, -1, wordmarkLetterforms...), renderWord(-3, -1, wordmarkLetterforms...))
}

func TestRenderWordSpellsAngela(t *testing.T) {
	t.Parallel()

	word := renderWord(1, -1, wordmarkLetterforms...)
	lines := strings.Split(word, "\n")
	require.Len(t, lines, 3)

	width := ansi.StringWidth(lines[0])
	for i, line := range lines {
		require.Equal(t, width, ansi.StringWidth(line), "row %d is ragged", i)
	}

	// Every row must carry ink; a blank row means a letterform collapsed.
	for i, line := range lines {
		require.NotEmpty(t, strings.TrimSpace(line), "row %d is blank", i)
	}

	t.Logf("ANGELA wordmark (%d cols):\n%s", width, word)
}
