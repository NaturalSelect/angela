package tools

import (
	"encoding/json"
	"testing"

	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
)

func proposalMetadata(t *testing.T, metadata string) ProposalResponseMetadata {
	t.Helper()
	var meta ProposalResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(metadata), &meta))
	return meta
}

// The first draft has nothing to diff against, so every line is an
// addition and the old side is empty.
func TestProposalWriteReportsTheFirstDraftAsAdditions(t *testing.T) {
	t.Parallel()

	resp, err := NewProposalWriteTool(NewProposalStore()).Run(
		proposalCtx(t, "s1"),
		proposalCall(toolnames.ProposalWrite, `{"content":"# Plan\n\nStep one."}`),
	)
	require.NoError(t, err)

	meta := proposalMetadata(t, resp.Metadata)
	require.Empty(t, meta.OldContent)
	require.Equal(t, "# Plan\n\nStep one.", meta.NewContent)
	require.Equal(t, 3, meta.Additions)
	require.Zero(t, meta.Removals)
}

// Rewriting a draft diffs against the draft it replaced, not against
// nothing, or the user would be shown the whole document as new.
func TestProposalWriteDiffsAgainstThePreviousDraft(t *testing.T) {
	t.Parallel()

	store := NewProposalStore()
	store.Set("s1", "ship it on Tuesday\n")

	resp, err := NewProposalWriteTool(store).Run(
		proposalCtx(t, "s1"),
		proposalCall(toolnames.ProposalWrite, `{"content":"ship it on Friday\n"}`),
	)
	require.NoError(t, err)

	meta := proposalMetadata(t, resp.Metadata)
	require.Equal(t, "ship it on Tuesday\n", meta.OldContent)
	require.Equal(t, "ship it on Friday\n", meta.NewContent)
	require.Equal(t, 1, meta.Additions)
	require.Equal(t, 1, meta.Removals)
}

func TestProposalEditReportsTheChange(t *testing.T) {
	t.Parallel()

	store := NewProposalStore()
	store.Set("s1", "ship it on Tuesday\n")

	resp, err := NewProposalEditTool(store).Run(
		proposalCtx(t, "s1"),
		proposalCall(toolnames.ProposalEdit, `{"old_string":"Tuesday","new_string":"Friday"}`),
	)
	require.NoError(t, err)

	meta := proposalMetadata(t, resp.Metadata)
	require.Equal(t, "ship it on Tuesday\n", meta.OldContent)
	require.Equal(t, "ship it on Friday\n", meta.NewContent)
	require.Equal(t, 1, meta.Additions)
	require.Equal(t, 1, meta.Removals)
}

// A failed edit changed nothing, so it has no diff to show.
func TestProposalEditFailureCarriesNoMetadata(t *testing.T) {
	t.Parallel()

	store := NewProposalStore()
	store.Set("s1", "ship it on Tuesday")

	resp, err := NewProposalEditTool(store).Run(
		proposalCtx(t, "s1"),
		proposalCall(toolnames.ProposalEdit, `{"old_string":"Wednesday","new_string":"Friday"}`),
	)
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Empty(t, resp.Metadata)
}
