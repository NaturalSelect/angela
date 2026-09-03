package xchroma

import (
	"bytes"
	"image/color"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/stretchr/testify/require"
)

func TestMatchLexer(t *testing.T) {
	t.Parallel()

	t.Run("returns a lexer for a known extension", func(t *testing.T) {
		t.Parallel()
		l := MatchLexer("main.go")
		require.NotNil(t, l)
		require.Equal(t, "Go", l.Config().Name)
	})

	t.Run("returns nil for an unknown extension", func(t *testing.T) {
		t.Parallel()
		l := MatchLexer("file.unknownextension12345")
		require.Nil(t, l)
	})

	t.Run("memoizes repeated lookups", func(t *testing.T) {
		t.Parallel()
		first := MatchLexer("memo_test_fixture.go")
		second := MatchLexer("memo_test_fixture.go")
		require.Same(t, first, second)
	})
}

func TestFormatter(t *testing.T) {
	t.Parallel()

	tokens := []chroma.Token{
		{Type: chroma.Keyword, Value: "func"},
		{Type: chroma.Text, Value: " "},
		{Type: chroma.NameFunction, Value: "main"},
	}
	idx := 0
	it := func() chroma.Token {
		if idx >= len(tokens) {
			return chroma.EOF
		}
		tok := tokens[idx]
		idx++
		return tok
	}

	style := styles.Get("monokai")
	require.NotNil(t, style)

	var processed []string
	formatter := Formatter(color.Black, func(v string) string {
		processed = append(processed, v)
		return v
	})

	var buf bytes.Buffer
	err := formatter.Format(&buf, style, it)
	require.NoError(t, err)
	require.NotEmpty(t, buf.String())
	require.Equal(t, []string{"func", " ", "main"}, processed)
}

func TestFormatter_NilProcessValue(t *testing.T) {
	t.Parallel()

	tokens := []chroma.Token{{Type: chroma.Text, Value: "plain"}}
	idx := 0
	it := func() chroma.Token {
		if idx >= len(tokens) {
			return chroma.EOF
		}
		tok := tokens[idx]
		idx++
		return tok
	}

	var buf bytes.Buffer
	err := Formatter(color.White, nil).Format(&buf, styles.Get("monokai"), it)
	require.NoError(t, err)
	require.Contains(t, buf.String(), "plain")
}
