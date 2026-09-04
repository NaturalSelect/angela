package chat

import (
	"strings"
	"testing"

	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// Content that does not parse into any files must fall back to a plain
// code-styled rendering of the raw text instead of an empty diff view.
func TestToolOutputDiffContentFromUnified_NoParsedFilesFallsBackToCode(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	out := ansi.Strip(toolOutputDiffContentFromUnified(&sty, "just some plain text\nwith no diff markers", 100, false))
	require.Contains(t, out, "just some plain text")
}

// A diff touching more than one file must show each file's name and
// separate the blocks from one another.
func TestToolOutputDiffContentFromUnified_MultipleFiles(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	content := `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,2 +1,2 @@
-old a
+new a
diff --git a/b.go b/b.go
--- a/b.go
+++ b/b.go
@@ -1,2 +1,2 @@
-old b
+new b
`
	out := ansi.Strip(toolOutputDiffContentFromUnified(&sty, content, 100, false))
	require.Contains(t, out, "a.go")
	require.Contains(t, out, "b.go")
	require.Contains(t, out, "new a")
	require.Contains(t, out, "new b")
}

// A wide terminal must switch the diff formatter into split view.
func TestToolOutputDiffContentFromUnified_WideTerminalSplitsView(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	content := `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,2 +1,2 @@
-old a
+new a
`
	out := ansi.Strip(toolOutputDiffContentFromUnified(&sty, content, 200, false))
	require.Contains(t, out, "new a")
}

// A long diff must be truncated to the response context height unless
// expanded, and the truncation notice must report the hidden count.
func TestToolOutputDiffContentFromUnified_TruncatesLongDiffs(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	var b strings.Builder
	b.WriteString("diff --git a/a.go b/a.go\n--- a/a.go\n+++ a/a.go\n@@ -1,30 +1,30 @@\n")
	for i := range 30 {
		b.WriteString("-old line ")
		b.WriteString(strings.Repeat("x", i%3+1))
		b.WriteString("\n+new line ")
		b.WriteString(strings.Repeat("x", i%3+1))
		b.WriteString("\n")
	}
	content := b.String()

	collapsed := ansi.Strip(toolOutputDiffContentFromUnified(&sty, content, 100, false))
	require.Contains(t, collapsed, "hidden")

	expanded := ansi.Strip(toolOutputDiffContentFromUnified(&sty, content, 100, true))
	require.NotContains(t, expanded, "hidden")
	require.Contains(t, expanded, "new line")
}
