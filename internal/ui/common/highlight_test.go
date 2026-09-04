package common

import (
	"image/color"
	"testing"

	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestSyntaxHighlight(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	t.Run("a known extension picks a matching lexer", func(t *testing.T) {
		t.Parallel()
		out, err := SyntaxHighlight(&sty, "package main\n\nfunc main() {}\n", "main.go", nil)
		require.NoError(t, err)
		require.Contains(t, ansi.Strip(out), "package main")
	})

	t.Run("an unknown extension falls back gracefully", func(t *testing.T) {
		t.Parallel()
		out, err := SyntaxHighlight(&sty, "just some text", "file.unknownext", nil)
		require.NoError(t, err)
		require.Contains(t, ansi.Strip(out), "just some text")
	})

	t.Run("a background color is accepted", func(t *testing.T) {
		t.Parallel()
		bg := color.RGBA{R: 1, G: 2, B: 3, A: 255}
		out, err := SyntaxHighlight(&sty, "x := 1", "main.go", bg)
		require.NoError(t, err)
		require.NotEmpty(t, out)
	})
}

func TestSyntaxHighlightLexerName(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	t.Run("a known language name is used directly", func(t *testing.T) {
		t.Parallel()
		out, err := SyntaxHighlightLexerName(&sty, "echo hi", "bash", nil)
		require.NoError(t, err)
		require.Contains(t, ansi.Strip(out), "echo hi")
	})

	t.Run("an unknown language name falls back", func(t *testing.T) {
		t.Parallel()
		out, err := SyntaxHighlightLexerName(&sty, "plain", "not-a-real-language", nil)
		require.NoError(t, err)
		require.Contains(t, ansi.Strip(out), "plain")
	})
}
