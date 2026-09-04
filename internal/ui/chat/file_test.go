package chat

import (
	"encoding/json"
	"testing"

	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// -----------------------------------------------------------------------------
// View Tool
// -----------------------------------------------------------------------------

func TestViewToolPending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "v1", Name: toolnames.View, Input: `{"file_path":"a.go"}`}
	item := NewViewToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, toolnames.View)
}

func TestViewToolInvalidParams(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "v1", Name: toolnames.View, Input: `not-json`, Finished: true}
	item := NewViewToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Invalid parameters")
}

func TestViewToolHeaderShowsLimitAndOffset(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "v1", Name: toolnames.View, Input: `{"file_path":"a.go","offset":10,"limit":50}`, Finished: true}
	item := NewViewToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "a.go")
	require.Contains(t, out, "limit")
	require.Contains(t, out, "50")
	require.Contains(t, out, "offset")
	require.Contains(t, out, "10")
}

func TestViewToolCompactShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "v1", Name: toolnames.View, Input: `{"file_path":"a.go"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "v1", Content: "distinctive view body content"}
	item := NewViewToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok, "tool items must implement Compactable")
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "distinctive view body content")
}

func TestViewToolAwaitsResultAfterFinish(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "v1", Name: toolnames.View, Input: `{"file_path":"a.go"}`, Finished: true}
	item := NewViewToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Waiting for tool response")
}

func TestViewToolCollapsedHidesBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "v1", Name: toolnames.View, Input: `{"file_path":"a.go"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "v1", Content: "distinctive collapsed view content"}
	item := NewViewToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "distinctive collapsed view content")
}

func TestViewToolExpandedShowsCodeContentFromMetadata(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	metaJSON, err := json.Marshal(tools.ViewResponseMetadata{
		FilePath: "a.go",
		Content:  "package main\n\ndistinctive-metadata-content\n",
	})
	require.NoError(t, err)

	toolCall := message.ToolCall{ID: "v1", Name: toolnames.View, Input: `{"file_path":"a.go"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "v1", Content: "fallback content", Metadata: string(metaJSON)}
	item := NewViewToolMessageItem(&sty, toolCall, result, false)

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	require.True(t, expandable.ToggleExpanded())

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "distinctive-metadata-content")
}

func TestViewToolExpandedFallsBackToResultContent(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "v1", Name: toolnames.View, Input: `{"file_path":"a.go"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "v1", Content: "distinctive-fallback-content"}
	item := NewViewToolMessageItem(&sty, toolCall, result, false)

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	require.True(t, expandable.ToggleExpanded())

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "distinctive-fallback-content")
}

func TestViewToolExpandedNoContentShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "v1", Name: toolnames.View, Input: `{"file_path":"a.go"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "v1", Content: ""}
	item := NewViewToolMessageItem(&sty, toolCall, result, false)

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	require.True(t, expandable.ToggleExpanded())

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, toolnames.View)
}

func TestViewToolExpandedShowsImageContent(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "v1", Name: toolnames.View, Input: `{"file_path":"a.png"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "v1", Data: "aGVsbG8=", MIMEType: "image/png"}
	item := NewViewToolMessageItem(&sty, toolCall, result, false)

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	require.True(t, expandable.ToggleExpanded())

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Loaded Image")
	require.Contains(t, out, "image/png")
}

func TestViewToolExpandedShowsSkillContent(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	metaJSON, err := json.Marshal(tools.ViewResponseMetadata{
		ResourceType:        tools.ViewResourceSkill,
		ResourceName:        "distinctive-skill-name",
		ResourceDescription: "does distinctive things",
		Content:             "skill body",
	})
	require.NoError(t, err)

	toolCall := message.ToolCall{ID: "v1", Name: toolnames.View, Input: `{"file_path":"angela://skills/x"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "v1", Metadata: string(metaJSON)}
	item := NewViewToolMessageItem(&sty, toolCall, result, false)

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	require.True(t, expandable.ToggleExpanded())

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Loaded Skill")
	require.Contains(t, out, "distinctive-skill-name")
}

// -----------------------------------------------------------------------------
// Write Tool
// -----------------------------------------------------------------------------

func TestWriteToolPending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "w1", Name: toolnames.Write, Input: `{"file_path":"a.go","content":"x"}`}
	item := NewWriteToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, toolnames.Write)
}

func TestWriteToolInvalidParams(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "w1", Name: toolnames.Write, Input: `not-json`, Finished: true}
	item := NewWriteToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Invalid parameters")
}

func TestWriteToolHeaderShowsFile(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "w1", Name: toolnames.Write, Input: `{"file_path":"a.go","content":"x"}`, Finished: true}
	item := NewWriteToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "a.go")
}

func TestWriteToolCompactShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "w1", Name: toolnames.Write, Input: `{"file_path":"a.go","content":"distinctive write content"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "w1", Content: "wrote a.go"}
	item := NewWriteToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok, "tool items must implement Compactable")
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "distinctive write content")
}

func TestWriteToolAwaitsResultAfterFinish(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "w1", Name: toolnames.Write, Input: `{"file_path":"a.go","content":"x"}`, Finished: true}
	item := NewWriteToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Waiting for tool response")
}

func TestWriteToolErrorWithDiffMetadataShowsSummaryAndDiff(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	metaJSON, err := json.Marshal(tools.WriteResponseMetadata{
		Diff:      "--- a.go\n+++ a.go\n@@ -1 +1 @@\n-old\n+new\n",
		Additions: 1,
		Removals:  1,
	})
	require.NoError(t, err)

	toolCall := message.ToolCall{ID: "w1", Name: toolnames.Write, Input: `{"file_path":"a.go","content":"new"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "w1", Content: "User denied permission to write", IsError: true, Metadata: string(metaJSON)}
	item := NewWriteToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "+1")
	require.Contains(t, out, "-1")
	require.Contains(t, out, "denied permission")
}

func TestWriteToolErrorWithoutDiffMetadataShowsErrorOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "w1", Name: toolnames.Write, Input: `{"file_path":"a.go","content":"new"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "w1", Content: "distinctive write failure", IsError: true}
	item := NewWriteToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "distinctive write failure")
}

func TestWriteToolSuccessShowsCodeContentAndSummary(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	metaJSON, err := json.Marshal(tools.WriteResponseMetadata{Additions: 2, Removals: 0})
	require.NoError(t, err)

	toolCall := message.ToolCall{ID: "w1", Name: toolnames.Write, Input: `{"file_path":"a.go","content":"line1\nline2\n"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "w1", Content: "wrote a.go", Metadata: string(metaJSON)}
	item := NewWriteToolMessageItem(&sty, toolCall, result, false)

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	require.True(t, expandable.ToggleExpanded())

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "+2")
	require.Contains(t, out, "line1")
}

func TestWriteToolSuccessNoContentShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "w1", Name: toolnames.Write, Input: `{"file_path":"a.go","content":""}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "w1", Content: "wrote a.go"}
	item := NewWriteToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "a.go")
}

// -----------------------------------------------------------------------------
// Edit Tool
// -----------------------------------------------------------------------------

func TestEditToolPending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "e1", Name: toolnames.Edit, Input: `{"file_path":"a.go","old_string":"a","new_string":"b"}`}
	item := NewEditToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, toolnames.Edit)
}

func TestEditToolInvalidParams(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "e1", Name: toolnames.Edit, Input: `not-json`, Finished: true}
	item := NewEditToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Invalid parameters")
}

func TestEditToolCompactShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "e1", Name: toolnames.Edit, Input: `{"file_path":"a.go","old_string":"a","new_string":"b"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "e1", Content: "distinctive edit content"}
	item := NewEditToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok, "tool items must implement Compactable")
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "distinctive edit content")
}

func TestEditToolAwaitsResultAfterFinish(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "e1", Name: toolnames.Edit, Input: `{"file_path":"a.go","old_string":"a","new_string":"b"}`, Finished: true}
	item := NewEditToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Waiting for tool response")
}

func TestEditToolInvalidMetadataFallsBackToPlainContent(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "e1", Name: toolnames.Edit, Input: `{"file_path":"a.go","old_string":"a","new_string":"b"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "e1", Content: "distinctive plain edit fallback", Metadata: "not-json"}
	item := NewEditToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "distinctive plain edit fallback")
}

func TestEditToolErrorShowsErrorAboveDiffWithSummary(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	metaJSON, err := json.Marshal(tools.EditResponseMetadata{
		Additions:  1,
		Removals:   1,
		OldContent: "a\n",
		NewContent: "b\n",
	})
	require.NoError(t, err)

	toolCall := message.ToolCall{ID: "e1", Name: toolnames.Edit, Input: `{"file_path":"a.go","old_string":"a","new_string":"b"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "e1", Content: "User denied permission to edit", IsError: true, Metadata: string(metaJSON)}
	item := NewEditToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "denied permission")
	require.Contains(t, out, "+1")
	require.Contains(t, out, "-1")
}

func TestEditToolErrorShowsErrorAboveDiffWithoutSummary(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	metaJSON, err := json.Marshal(tools.EditResponseMetadata{
		OldContent: "a\n",
		NewContent: "a\n",
	})
	require.NoError(t, err)

	toolCall := message.ToolCall{ID: "e1", Name: toolnames.Edit, Input: `{"file_path":"a.go","old_string":"a","new_string":"a"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "e1", Content: "distinctive edit error, no diff stats", IsError: true, Metadata: string(metaJSON)}
	item := NewEditToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "distinctive edit error, no diff stats")
	require.NotContains(t, out, agentSummaryArrow)
}

// -----------------------------------------------------------------------------
// MultiEdit Tool
// -----------------------------------------------------------------------------

func TestMultiEditToolPending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{
		ID:    "me1",
		Name:  toolnames.MultiEdit,
		Input: `{"file_path":"a.go","edits":[{"old_string":"a","new_string":"b"}]}`,
	}
	item := NewMultiEditToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Multi-Edit")
}

func TestMultiEditToolInvalidParams(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "me1", Name: toolnames.MultiEdit, Input: `not-json`, Finished: true}
	item := NewMultiEditToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Invalid parameters")
}

func TestMultiEditToolHeaderShowsEditCount(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{
		ID:       "me1",
		Name:     toolnames.MultiEdit,
		Input:    `{"file_path":"a.go","edits":[{"old_string":"a","new_string":"A"},{"old_string":"b","new_string":"B"}]}`,
		Finished: true,
	}
	item := NewMultiEditToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "a.go")
	require.Contains(t, out, "edits")
	require.Contains(t, out, "2")
}

func TestMultiEditToolCompactShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{
		ID:       "me1",
		Name:     toolnames.MultiEdit,
		Input:    `{"file_path":"a.go","edits":[{"old_string":"a","new_string":"b"}]}`,
		Finished: true,
	}
	result := &message.ToolResult{ToolCallID: "me1", Content: "distinctive multiedit content"}
	item := NewMultiEditToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok, "tool items must implement Compactable")
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "distinctive multiedit content")
}

func TestMultiEditToolAwaitsResultAfterFinish(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{
		ID:       "me1",
		Name:     toolnames.MultiEdit,
		Input:    `{"file_path":"a.go","edits":[{"old_string":"a","new_string":"b"}]}`,
		Finished: true,
	}
	item := NewMultiEditToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Waiting for tool response")
}

func TestMultiEditToolInvalidMetadataFallsBackToPlainContent(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{
		ID:       "me1",
		Name:     toolnames.MultiEdit,
		Input:    `{"file_path":"a.go","edits":[{"old_string":"a","new_string":"b"}]}`,
		Finished: true,
	}
	result := &message.ToolResult{ToolCallID: "me1", Content: "distinctive plain multiedit fallback", Metadata: "not-json"}
	item := NewMultiEditToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "distinctive plain multiedit fallback")
}

func TestMultiEditToolErrorShowsErrorAboveDiff(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	metaJSON, err := json.Marshal(tools.MultiEditResponseMetadata{
		Additions:  2,
		Removals:   2,
		OldContent: "a\nb\n",
		NewContent: "A\nB\n",
	})
	require.NoError(t, err)

	toolCall := message.ToolCall{
		ID:       "me1",
		Name:     toolnames.MultiEdit,
		Input:    `{"file_path":"a.go","edits":[{"old_string":"a","new_string":"A"},{"old_string":"b","new_string":"B"}]}`,
		Finished: true,
	}
	result := &message.ToolResult{ToolCallID: "me1", Content: "User denied permission to edit", IsError: true, Metadata: string(metaJSON)}
	item := NewMultiEditToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "denied permission")
	require.Contains(t, out, "+2")
	require.Contains(t, out, "-2")
}

// -----------------------------------------------------------------------------
// Download Tool
// -----------------------------------------------------------------------------

func TestDownloadToolPending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "d1", Name: toolnames.Download, Input: `{"url":"https://example.com/a.zip","file_path":"a.zip"}`}
	item := NewDownloadToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, toolnames.Download)
}

func TestDownloadToolInvalidParams(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "d1", Name: toolnames.Download, Input: `not-json`, Finished: true}
	item := NewDownloadToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Invalid parameters")
}

func TestDownloadToolHeaderShowsURLFilePathAndTimeout(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{
		ID:       "d1",
		Name:     toolnames.Download,
		Input:    `{"url":"https://example.com/a.zip","file_path":"a.zip","timeout":45}`,
		Finished: true,
	}
	item := NewDownloadToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "https://example.com/a.zip")
	require.Contains(t, out, "file_path")
	require.Contains(t, out, "a.zip")
	require.Contains(t, out, "timeout")
	require.Contains(t, out, "45s")
}

func TestDownloadToolCompactShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "d1", Name: toolnames.Download, Input: `{"url":"https://example.com/a.zip"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "d1", Content: "distinctive download content"}
	item := NewDownloadToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok, "tool items must implement Compactable")
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "distinctive download content")
}

func TestDownloadToolAwaitsResultAfterFinish(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "d1", Name: toolnames.Download, Input: `{"url":"https://example.com/a.zip"}`, Finished: true}
	item := NewDownloadToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Waiting for tool response")
}

func TestDownloadToolEmptyResultShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "d1", Name: toolnames.Download, Input: `{"url":"https://example.com/a.zip"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "d1", Content: ""}
	item := NewDownloadToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, toolnames.Download)
}

func TestDownloadToolCollapsedHidesBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "d1", Name: toolnames.Download, Input: `{"url":"https://example.com/a.zip"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "d1", Content: "distinctive collapsed download content"}
	item := NewDownloadToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "distinctive collapsed download content")
}

func TestDownloadToolExpandedShowsBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "d1", Name: toolnames.Download, Input: `{"url":"https://example.com/a.zip"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "d1", Content: "distinctive expanded download content"}
	item := NewDownloadToolMessageItem(&sty, toolCall, result, false)

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	require.True(t, expandable.ToggleExpanded())

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "distinctive expanded download content")
}
