package tools

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

//go:embed proposal_write.md
var proposalWriteDescription string

//go:embed proposal_edit.md
var proposalEditDescription string

//go:embed proposal_read.md
var proposalReadDescription string

// ProposalDocumentName labels the document in the approval dialog. The
// suffix is what picks the lexer that highlights it.
const ProposalDocumentName = "PROPOSAL.md"

// ProposalStore holds the document a branch is drafting, one per branch
// session.
//
// It never reaches disk. A branch drafts a proposal so the user can
// approve what crosses back, and a proposal they end up rejecting has
// no business in the working tree; keeping it in memory also means the
// draft is unaffected by whatever the permission rules say about
// writing files. Nothing outlives the branch, which cannot survive a
// restart either.
type ProposalStore struct {
	docs *csync.Map[string, string]
}

func NewProposalStore() *ProposalStore {
	return &ProposalStore{docs: csync.NewMap[string, string]()}
}

func (s *ProposalStore) Get(sessionID string) (string, bool) {
	return s.docs.Get(sessionID)
}

func (s *ProposalStore) Set(sessionID, content string) {
	s.docs.Set(sessionID, content)
}

func (s *ProposalStore) Discard(sessionID string) {
	s.docs.Del(sessionID)
}

type ProposalWriteParams struct {
	Content string `json:"content" description:"The full text of the proposal, replacing whatever it held before"`
}

type ProposalEditParams struct {
	OldString  string `json:"old_string" description:"The text to replace"`
	NewString  string `json:"new_string" description:"The text to replace it with"`
	ReplaceAll bool   `json:"replace_all,omitempty" description:"Replace all occurrences of old_string (default false)"`
}

type ProposalReadParams struct{}

func NewProposalWriteTool(store *ProposalStore) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		toolnames.ProposalWrite,
		proposalWriteDescription,
		func(ctx context.Context, params ProposalWriteParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, fmt.Errorf("session ID is required for %s", toolnames.ProposalWrite)
			}
			if params.Content == "" {
				return fantasy.NewTextErrorResponse("content is required"), nil
			}

			store.Set(sessionID, params.Content)
			// The proposal itself is deliberately absent from the reply:
			// echoing it back would spend on the return path exactly what
			// editing in place saves on the way in.
			return fantasy.NewTextResponse(fmt.Sprintf(
				"Proposal saved, %d lines. Revise it with %s rather than writing it out again.",
				lineCount(params.Content), toolnames.ProposalEdit,
			)), nil
		},
	)
}

func NewProposalEditTool(store *ProposalStore) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		toolnames.ProposalEdit,
		proposalEditDescription,
		func(ctx context.Context, params ProposalEditParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, fmt.Errorf("session ID is required for %s", toolnames.ProposalEdit)
			}
			if params.OldString == "" {
				return fantasy.NewTextErrorResponse(fmt.Sprintf(
					"old_string is required. To start the proposal, use %s.", toolnames.ProposalWrite,
				)), nil
			}

			doc, ok := store.Get(sessionID)
			if !ok {
				return fantasy.NewTextErrorResponse(fmt.Sprintf(
					"There is no proposal to edit yet. Draft it with %s first.", toolnames.ProposalWrite,
				)), nil
			}

			updated, whitespaceCorrected, err := findAndReplace(doc, params.OldString, params.NewString, params.ReplaceAll)
			if err != nil {
				// A failed match leaves the stored document untouched, so
				// the model can retry against what is still there.
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			store.Set(sessionID, updated)
			return fantasy.NewTextResponse(withWhitespaceNote(fmt.Sprintf(
				"Proposal updated, %d lines.", lineCount(updated),
			), whitespaceCorrected)), nil
		},
	)
}

func NewProposalReadTool(store *ProposalStore) fantasy.AgentTool {
	return fantasy.NewParallelAgentTool(
		toolnames.ProposalRead,
		proposalReadDescription,
		func(ctx context.Context, params ProposalReadParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, fmt.Errorf("session ID is required for %s", toolnames.ProposalRead)
			}

			doc, ok := store.Get(sessionID)
			if !ok || doc == "" {
				return fantasy.NewTextResponse(fmt.Sprintf(
					"The proposal is empty. Draft it with %s.", toolnames.ProposalWrite,
				)), nil
			}
			return fantasy.NewTextResponse(doc), nil
		},
	)
}

func lineCount(content string) int {
	if content == "" {
		return 0
	}
	return strings.Count(content, "\n") + 1
}
