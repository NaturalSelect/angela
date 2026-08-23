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
		"A": LetterA,
		"N": LetterN,
		"G": LetterG,
		"E": LetterE,
		"L": LetterL,
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
