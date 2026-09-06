// Package editorapproval lets an external code editor stand in for
// Angela's own permission dialog when reviewing a file diff: instead of
// approving or denying in the terminal, the user reviews the change in
// their editor, and whatever that editor's UI state ends up being (what
// got saved, what got closed) is translated back into a decision.
//
// The Editor interface captures what every such integration has in
// common: take a diff, block until the user is done with it, report
// what they decided. New editors can be added by implementing Editor
// without touching callers that only depend on this package.
package editorapproval

import "context"

// Request describes a single file change to present to the reviewer.
type Request struct {
	// FilePath is the path being changed. It is shown to the reviewer
	// and used to name the temporary files opened in the editor.
	FilePath string
	// OldContent is the file's content before the change. Empty when
	// the change creates a new file.
	OldContent string
	// NewContent is the proposed content after the change.
	NewContent string
	// Description is a short human-readable summary of the change.
	Description string
}

// Outcome is the reviewer's decision after inspecting a Request.
type Outcome uint8

const (
	// OutcomeApprove means the change should be applied, using
	// Decision.Content as the file's final contents.
	OutcomeApprove Outcome = iota
	// OutcomeDeny means the change should be discarded.
	OutcomeDeny
)

// Decision is the reviewer's answer for a Request.
type Decision struct {
	Outcome Outcome
	// Reason explains a denial. Empty for approvals.
	Reason string
	// Content is the content to apply when Outcome is OutcomeApprove.
	// It can differ from Request.NewContent if the reviewer edited the
	// proposed change further before accepting it.
	Content string
}

// Editor asks an external code editor to review a file diff and
// reports the user's decision. Review blocks until the review is
// finished, so callers should run it off any goroutine that must stay
// responsive.
type Editor interface {
	// Name identifies the editor, e.g. for logging or config selection.
	Name() string
	// Available reports whether this editor can be launched on the
	// current machine (e.g. its CLI binary is on PATH).
	Available() bool
	// Review opens req in the editor and blocks until the user finishes
	// reviewing it, then returns their decision. It respects ctx
	// cancellation by terminating the editor process.
	Review(ctx context.Context, req Request) (Decision, error)
}
