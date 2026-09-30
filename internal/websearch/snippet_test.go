package websearch

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCleanSnippet_KeepsNoiseWordsInsideRealSentences pins that words
// such as login and subscribe survive when they are part of technical
// prose.
func TestCleanSnippet_KeepsNoiseWordsInsideRealSentences(t *testing.T) {
	t.Parallel()

	for _, text := range []string{
		"consumer.subscribe to topics and poll for records",
		"Implement login with OAuth",
		"Get started with Go generics: a tutorial",
		"docker login before you push",
		"Use sign in with Apple for iOS. It needs a capability.",
	} {
		require.Equal(t, text, cleanSnippet(text), text)
	}
}

func TestCleanSnippet_StripsLeadingAndTrailingBoilerplateSentences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "leading", in: "Sign up to read more. The Go team released 1.24.", want: "The Go team released 1.24."},
		{name: "trailing", in: "The Go team released 1.24. Read more »", want: "The Go team released 1.24."},
		{name: "trailing after ellipsis", in: "Generics landed in Go 1.18... Continue reading", want: "Generics landed in Go 1.18..."},
		{name: "both ends", in: "Log in. Real content here. Subscribe now!", want: "Real content here."},
		{name: "case insensitive", in: "SIGN UP FOR FREE\nBody text.", want: "Body text."},
		{name: "only boilerplate", in: "Read more", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, cleanSnippet(tt.in))
		})
	}
}

// TestCleanSnippet_LeavesBoilerplateBetweenContent pins that only the
// ends are trimmed.
func TestCleanSnippet_LeavesBoilerplateBetweenContent(t *testing.T) {
	t.Parallel()

	text := "Intro sentence. Sign up now. Details follow."
	require.Equal(t, text, cleanSnippet(text))
}

func TestCleanSnippet_StripsMarkupCollapsesWhitespaceAndTruncates(t *testing.T) {
	t.Parallel()

	require.Equal(t, "Title body text", cleanSnippet("## Title\n\n  body   text"))
	require.Empty(t, cleanSnippet(""))

	long := cleanSnippet(strings.Repeat("a", snippetMaxRunes+50))
	require.Len(t, []rune(long), snippetMaxRunes)
}
