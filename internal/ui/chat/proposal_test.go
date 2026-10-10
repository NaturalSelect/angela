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

func proposalResult(t *testing.T, meta tools.ProposalResponseMetadata, isError bool, content string) *message.ToolResult {
	t.Helper()
	metaJSON, err := json.Marshal(meta)
	require.NoError(t, err)
	return &message.ToolResult{ToolCallID: "p1", Content: content, IsError: isError, Metadata: string(metaJSON)}
}

func TestProposalToolPending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "p1", Name: toolnames.ProposalWrite, Input: `{"content":"x"}`}
	item := NewProposalToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, toolnames.ProposalWrite)
}

// The raw JSON of the call used to be the whole header, with the
// document escaped onto a single line.
func TestProposalToolHeaderNamesTheDocumentNotTheArguments(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "p1", Name: toolnames.ProposalWrite, Input: `{"content":"distinctive document body"}`, Finished: true}
	result := proposalResult(t, tools.ProposalResponseMetadata{Additions: 1, NewContent: "distinctive document body\n"}, false, "Proposal saved")
	item := NewProposalToolMessageItem(&sty, toolCall, result, false)

	header := ansi.Strip(item.Render(100))
	require.Contains(t, header, toolnames.ProposalWrite)
	require.Contains(t, header, tools.ProposalDocumentName)
	require.NotContains(t, header, `"content"`)
}

func TestProposalWriteShowsTheDiffAndSummary(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "p1", Name: toolnames.ProposalWrite, Input: `{"content":"new line\n"}`, Finished: true}
	result := proposalResult(t, tools.ProposalResponseMetadata{
		Additions:  1,
		Removals:   1,
		OldContent: "old line\n",
		NewContent: "new line\n",
	}, false, "Proposal saved")
	item := NewProposalToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "new line")
	require.Contains(t, out, "old line")
	require.Contains(t, out, "+1")
	require.Contains(t, out, "-1")
}

func TestProposalEditShowsTheDiff(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "p1", Name: toolnames.ProposalEdit, Input: `{"old_string":"Tuesday","new_string":"Friday"}`, Finished: true}
	result := proposalResult(t, tools.ProposalResponseMetadata{
		Additions:  1,
		Removals:   1,
		OldContent: "ship it on Tuesday\n",
		NewContent: "ship it on Friday\n",
	}, false, "Proposal updated")
	item := NewProposalToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, toolnames.ProposalEdit)
	require.Contains(t, out, "Tuesday")
	require.Contains(t, out, "Friday")
}

func TestProposalToolCompactShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "p1", Name: toolnames.ProposalWrite, Input: `{"content":"x"}`, Finished: true}
	result := proposalResult(t, tools.ProposalResponseMetadata{Additions: 1, NewContent: "distinctive proposal body\n"}, false, "Proposal saved")
	item := NewProposalToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok, "tool items must implement Compactable")
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "distinctive proposal body")
}

func TestProposalToolAwaitsResultAfterFinish(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "p1", Name: toolnames.ProposalEdit, Input: `{"old_string":"a","new_string":"b"}`, Finished: true}
	item := NewProposalToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Waiting for tool response")
}

// A failed edit changed nothing, so there is no diff to draw; the reason
// it failed is all the reader needs.
func TestProposalToolErrorWithoutMetadataShowsErrorOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "p1", Name: toolnames.ProposalEdit, Input: `{"old_string":"a","new_string":"b"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "p1", Content: "distinctive old_string not found", IsError: true}
	item := NewProposalToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "distinctive old_string not found")
	require.NotContains(t, out, agentSummaryArrow)
}

// Diffs need the room, so the proposal tools take the full width the
// file edit tools do rather than the capped text column.
func TestProposalToolsUseFullWidth(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	for _, name := range []string{toolnames.ProposalWrite, toolnames.ProposalEdit} {
		item := NewToolMessageItem(&sty, "m1", message.ToolCall{ID: "p1", Name: name, Input: "{}", Finished: true}, nil, false, "/tmp")
		base, ok := item.(*baseToolMessageItem)
		require.True(t, ok)
		require.IsType(t, &ProposalToolRenderContext{}, base.toolRenderer, "%s must route to the proposal renderer", name)
		require.False(t, base.hasCappedWidth, "%s renders a diff", name)
	}
}

// ProposalRead returns text, not a change, so it keeps the generic
// renderer.
func TestProposalReadIsNotRoutedToTheDiffRenderer(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	item := NewToolMessageItem(&sty, "m1", message.ToolCall{ID: "p1", Name: toolnames.ProposalRead, Input: "{}", Finished: true}, nil, false, "/tmp")
	base, ok := item.(*baseToolMessageItem)
	require.True(t, ok)
	require.IsType(t, &GenericToolRenderContext{}, base.toolRenderer)
}
