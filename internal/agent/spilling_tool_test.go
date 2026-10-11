package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
)

func oversizedOutput() string {
	var b strings.Builder
	for i := range tools.MaxOutputLength {
		b.WriteString("line ")
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteString("\n")
	}
	return b.String()
}

func runSpilling(t *testing.T, root, sessionID string, resp fantasy.ToolResponse) fantasy.ToolResponse {
	t.Helper()
	inner, _ := newFakeTool(t, toolnames.Bash, resp)
	ctx := context.WithValue(t.Context(), tools.SessionIDContextKey, sessionID)
	got, err := newSpillingTool(inner, root).Run(ctx, fantasy.ToolCall{ID: "call-1", Name: toolnames.Bash})
	require.NoError(t, err)
	return got
}

func TestSpillingTool_ShortOutputPassesThrough(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	got := runSpilling(t, root, "session-a", fantasy.NewTextResponse("hello"))

	require.Equal(t, "hello", got.Content)
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	require.Empty(t, entries, "short output must not create files")
}

func TestSpillingTool_LongOutputIsSavedInFull(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	full := oversizedOutput()
	got := runSpilling(t, root, "session-a", fantasy.NewTextResponse(full))

	files, err := filepath.Glob(filepath.Join(root, "session-a", "output-*.txt"))
	require.NoError(t, err)
	require.Len(t, files, 1)

	saved, err := os.ReadFile(files[0])
	require.NoError(t, err)
	require.Equal(t, full, string(saved), "the saved file must hold the untruncated output")

	require.True(t, strings.HasPrefix(got.Content, tools.TruncateOutput(full)))
	require.Contains(t, got.Content, files[0], "the model must be told where the full output is")
	require.Less(t, len(got.Content), len(full))
}

func TestSpillingTool_KeepsErrorFlag(t *testing.T) {
	t.Parallel()

	got := runSpilling(t, t.TempDir(), "session-a", fantasy.NewTextErrorResponse(oversizedOutput()))

	require.True(t, got.IsError)
}

func TestSpillingTool_UnsafeSessionIDTruncatesWithoutSaving(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	root := filepath.Join(parent, "tool-output")
	got := runSpilling(t, root, "../escape", fantasy.NewTextResponse(oversizedOutput()))

	require.Contains(t, got.Content, "could not be saved")
	require.NoDirExists(t, root)
	entries, err := os.ReadDir(parent)
	require.NoError(t, err)
	require.Empty(t, entries, "nothing may be written outside the tool-output root")
}

func TestSpillingTool_LeavesNonTextResponsesAlone(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	image := fantasy.NewImageResponse([]byte(oversizedOutput()), "image/png")
	got := runSpilling(t, root, "session-a", image)

	require.Equal(t, image, got)
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestWrapToolsWithSpill_SkipsExemptTools(t *testing.T) {
	t.Parallel()

	exempt := []string{toolnames.Read, toolnames.Agent, toolnames.LoadReport, toolnames.ProposalRead, toolnames.Merge}
	var inputs []fantasy.AgentTool
	for _, name := range append(exempt, toolnames.Bash) {
		tool, _ := newFakeTool(t, name, fantasy.ToolResponse{})
		inputs = append(inputs, tool)
	}

	out := wrapToolsWithSpill(inputs, t.TempDir())

	require.Len(t, out, len(inputs))
	for _, tool := range out {
		_, wrapped := tool.(*spillingTool)
		if tool.Info().Name == toolnames.Bash {
			require.True(t, wrapped, "Bash must be wrapped")
			continue
		}
		require.False(t, wrapped, "%s must not be wrapped", tool.Info().Name)
	}
}

// A result with one enormous line, such as minified JSON, is saved wrapped.
// Read cuts a line at tools.MaxLineLength bytes and Grep at
// tools.MaxGrepContentWidth columns, so without wrapping the middle of
// that line could never be shown.
func TestSpillingTool_WrapsLinesTooWideToPageThrough(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	var words []string
	for i := range 20_000 {
		words = append(words, fmt.Sprintf("item%d", i))
	}
	oneLine := strings.Join(words, ",")
	require.NotContains(t, oneLine, "\n")

	got := runSpilling(t, root, "session-a", fantasy.NewTextResponse(oneLine))

	files, err := filepath.Glob(filepath.Join(root, "session-a", "output-*.txt"))
	require.NoError(t, err)
	require.Len(t, files, 1)
	saved, err := os.ReadFile(files[0])
	require.NoError(t, err)

	lines := strings.Split(string(saved), "\n")
	require.Greater(t, len(lines), 1, "the single line must have been broken up")
	for _, line := range lines {
		require.LessOrEqual(t, len(line), tools.MaxGrepContentWidth, "every saved line must fit what Grep shows")
		require.LessOrEqual(t, len(line), tools.MaxLineLength, "every saved line must fit what Read shows")
	}
	require.Equal(t, oneLine, strings.ReplaceAll(string(saved), "\n", ""),
		"wrapping must only add line breaks")

	require.Contains(t, got.Content, "were wrapped in the saved file")
	require.Contains(t, got.Content, fmt.Sprintf("%d lines", len(lines)),
		"the line count must describe the saved file the model pages through")
	require.NotContains(t, got.Content, "Do not re-run")
}

func TestSpillingTool_NoticeOmitsWrapRemarkForNormalLines(t *testing.T) {
	t.Parallel()

	got := runSpilling(t, t.TempDir(), "session-a", fantasy.NewTextResponse(oversizedOutput()))

	require.NotContains(t, got.Content, "wrapped")
}
