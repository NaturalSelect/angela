package chat

import (
	"encoding/json"

	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/styles"
)

// ProposalToolMessageItem is a message item that represents a ProposalWrite
// or ProposalEdit tool call.
type ProposalToolMessageItem struct {
	*baseToolMessageItem
}

var _ ToolMessageItem = (*ProposalToolMessageItem)(nil)

// NewProposalToolMessageItem creates a new [ProposalToolMessageItem] for the
// named proposal tool.
func NewProposalToolMessageItem(
	sty *styles.Styles,
	toolCall message.ToolCall,
	result *message.ToolResult,
	canceled bool,
) ToolMessageItem {
	return newBaseToolMessageItem(sty, toolCall, result, &ProposalToolRenderContext{name: toolCall.Name}, canceled)
}

// ProposalToolRenderContext renders the tools that revise a branch's draft
// proposal. The draft lives in memory, so the diff comes from the response
// metadata rather than from a file.
type ProposalToolRenderContext struct {
	name string
}

// RenderTool implements the [ToolRenderer] interface.
func (p *ProposalToolRenderContext) RenderTool(sty *styles.Styles, width int, opts *ToolRenderOpts) string {
	// Proposal tools use full width for diffs, like edit.
	if opts.IsPending() {
		return pendingTool(sty, p.name, opts.Anim, opts.Compact)
	}

	header := toolHeader(sty, opts.Status, p.name, width, opts, tools.ProposalDocumentName)
	if opts.Compact {
		return header
	}

	if !opts.HasResult() {
		if earlyState, ok := toolEarlyStateContent(sty, opts, width); ok {
			return joinToolParts(header, earlyState)
		}
		return header
	}

	var meta tools.ProposalResponseMetadata
	if err := json.Unmarshal([]byte(opts.Result.Metadata), &meta); err != nil || (meta.OldContent == "" && meta.NewContent == "") {
		// A refused revision, such as an edit that matched nothing, changes
		// no document and so carries no diff.
		if opts.Result.IsError {
			return joinToolParts(header, toolErrorContent(sty, opts.Result, width))
		}
		bodyWidth := width - toolBodyLeftPaddingTotal
		body := sty.Tool.Body.Render(toolOutputPlainContent(sty, opts.Result.Content, bodyWidth, opts.ExpandedContent))
		return joinToolParts(header, body)
	}

	diff := toolOutputDiffContent(sty, tools.ProposalDocumentName, meta.OldContent, meta.NewContent, width, opts.ExpandedContent)
	if summary := changesSummaryLine(sty, meta.Additions, meta.Removals); summary != "" {
		diff = summary + "\n" + diff
	}
	return joinToolParts(header, diff)
}

// isProposalDiffTool reports whether a tool call renders a proposal diff.
func isProposalDiffTool(name string) bool {
	return name == toolnames.ProposalWrite || name == toolnames.ProposalEdit
}
