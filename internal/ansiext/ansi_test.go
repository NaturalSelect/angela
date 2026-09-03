package ansiext

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEscape(t *testing.T) {
	t.Parallel()

	t.Run("plain text is unchanged", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, "hello world", Escape("hello world"))
	})

	t.Run("control characters become control pictures", func(t *testing.T) {
		t.Parallel()
		for r := rune(0); r <= 0x1f; r++ {
			got := Escape(string(r))
			want := string('\u2400' + r)
			require.Equal(t, want, got, "control char 0x%02x", r)
		}
	})

	t.Run("DEL becomes its own control picture", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, "\u2421", Escape(string(rune(0x7f))))
	})

	t.Run("mixed content only escapes control characters", func(t *testing.T) {
		t.Parallel()
		in := "a\x01b\x02c"
		got := Escape(in)
		want := "a" + string(rune('\u2400'+1)) + "b" + string(rune('\u2400'+2)) + "c"
		require.Equal(t, want, got)
	})

	t.Run("empty string", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, "", Escape(""))
	})

	t.Run("unicode content above control range is unchanged", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, "héllo 世界", Escape("héllo 世界"))
	})
}
