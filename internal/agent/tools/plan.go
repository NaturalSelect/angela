package tools

import (
	"context"
	"log/slog"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/history"
	"github.com/NaturalSelect/angela/internal/permission"
)

// Plan is a tool call worked out but not yet carried out. Tools that
// rewrite a file produce one so the permission gate can show the user
// the resulting diff: the diff only exists once the file has been read
// and the new content computed, which is work the gate cannot do
// itself.
//
// The gate never looks inside a plan. Everything a tool needs to carry
// out is captured by the Apply closure, which is why a multi-file
// change fits here as comfortably as a single-file one.
type Plan struct {
	// Preview is what the user sees when the call reaches the prompt.
	Preview permission.Preview
	// Response short-circuits the call. Planning settled it already,
	// either because nothing needs doing or because it failed in a way
	// the model should read rather than the user approve, so the gate
	// returns this without prompting. Apply is nil when it is set.
	Response *fantasy.ToolResponse
	// Refusal is attached to the response when the call is refused. The
	// chat renders a refused edit with its diff, so dropping this would
	// leave the user with an error and no sight of what was declined.
	Refusal any
	// Apply carries the call out. It runs only after the gate approves.
	Apply func(context.Context) (fantasy.ToolResponse, error)
}

// Planner is a tool that works out its call before performing it.
//
// A planner must not be run outside the permission decorator: its Run
// method plans and applies in one go, without consulting the gate.
type Planner interface {
	// Plan works out what the call would do, touching nothing the call
	// itself would change. The error return is for failures the model
	// cannot act on; anything it should read about goes in
	// Plan.Response.
	Plan(ctx context.Context, call fantasy.ToolCall) (Plan, error)
}

// settled wraps an answer planning already arrived at, so the call
// returns it instead of asking the user to approve nothing.
func settled(resp fantasy.ToolResponse) Plan {
	return Plan{Response: &resp}
}

// recordFileVersions stores both sides of a change in the session's
// history. Recording only the old side leaves the timeline unable to
// say what the file became, which is what the file viewer reads back.
func recordFileVersions(ctx context.Context, files history.Service, sessionID, path, oldContent, newContent string) {
	if files == nil || sessionID == "" {
		return
	}

	file, err := files.GetByPathAndSession(ctx, path, sessionID)
	if err != nil {
		if _, err := files.Create(ctx, sessionID, path, oldContent); err != nil {
			slog.Warn("Failed to start file history", "path", path, "error", err)
			return
		}
	} else if file.Content != oldContent {
		// The file changed outside this session; keep what it held.
		if _, err := files.CreateVersion(ctx, sessionID, path, oldContent); err != nil {
			slog.Warn("Failed to record file version", "path", path, "error", err)
		}
	}

	if _, err := files.CreateVersion(ctx, sessionID, path, newContent); err != nil {
		slog.Warn("Failed to record file version", "path", path, "error", err)
	}
}
