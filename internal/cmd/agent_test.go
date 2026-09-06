package cmd

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// TestTruncateDescriptionIsRuneSafe pins the UTF-8 fix. Slicing at byte
// offset 47 lands mid-rune for any non-ASCII description, so the list
// used to print a replacement character for CJK or emoji text.
func TestTruncateDescriptionIsRuneSafe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		desc string
	}{
		{"ascii short", "Reviews code"},
		{"ascii long", strings.Repeat("a", 120)},
		{"chinese", strings.Repeat("审查代码变更", 20)},
		{"emoji", strings.Repeat("🚀", 60)},
		{"mixed", "Review " + strings.Repeat("变更🚀", 30)},
		{"exactly at the limit", strings.Repeat("a", 50)},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := truncateDescription(tt.desc)
			require.True(t, utf8.ValidString(got), "truncation must not split a rune")
			require.LessOrEqual(t, ansi.StringWidth(got), 50,
				"truncation must fit the description column")
		})
	}
}

func TestTruncateDescriptionKeepsShortInputIntact(t *testing.T) {
	t.Parallel()

	require.Equal(t, "Reviews code", truncateDescription("Reviews code"))
	require.Equal(t, "审查代码", truncateDescription("审查代码"))
}

func TestTruncateDescriptionMarksElision(t *testing.T) {
	t.Parallel()

	got := truncateDescription(strings.Repeat("a", 120))
	require.True(t, strings.HasSuffix(got, "..."), "a shortened description must show it was cut")
}

// TestAgentCreateCmd_RegistersSandboxFlags covers a gap where
// agentCreateCmd called setupLocalWorkspace (which reads --sandbox,
// its refinements, and --no-docker-sandbox) without ever registering
// those flags itself. Since they live on rootCmd's non-persistent
// FlagSet rather than PersistentFlags, agentCreateCmd never inherited
// them, so passing any of them failed at cobra's parse stage with
// "unknown flag" before setupLocalWorkspace ever ran.
func TestAgentCreateCmd_RegistersSandboxFlags(t *testing.T) {
	t.Parallel()

	for _, name := range append([]string{"sandbox"}, sandboxFlagNames...) {
		require.NotNil(t, agentCreateCmd.Flags().Lookup(name),
			"agentCreateCmd must register --%s so setupLocalWorkspace's read of it reflects real user intent", name)
	}
	require.NotNil(t, agentCreateCmd.Flags().Lookup("no-docker-sandbox"),
		"agentCreateCmd must register --no-docker-sandbox so setupLocalWorkspace's read of it reflects real user intent")
}
