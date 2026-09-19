package tools

import (
	"context"
	"fmt"

	"charm.land/fantasy"
)

// Result is the outcome of running a tool. It is a closed union: the only
// way to build one is through the constructors below, so tool code has no
// path that produces a Go error. fantasy.AgentTool.Run still returns
// (fantasy.ToolResponse, error), but that error slot is reserved for
// context cancellation alone; NewTool and NewParallelTool are the only
// place a Result is translated into that pair.
type Result struct {
	resp fantasy.ToolResponse
}

// Ok returns a successful text result.
func Ok(content string) Result {
	return Result{resp: fantasy.NewTextResponse(content)}
}

// Fail returns a tool-error result: the model sees content as an error
// and the conversation continues normally. This is the right return for
// any business failure (bad input, file not found, permission denied,
// a failed request, and so on) — anything short of the call itself being
// canceled.
func Fail(content string) Result {
	return Result{resp: fantasy.NewTextErrorResponse(content)}
}

// Failf is Fail with fmt.Sprintf formatting.
func Failf(format string, args ...any) Result {
	return Fail(fmt.Sprintf(format, args...))
}

// FailErr is Fail for a Go error, formatted as "prefix: err".
func FailErr(prefix string, err error) Result {
	return Fail(prefix + ": " + err.Error())
}

// Halt is Fail with StopTurn set, ending the turn instead of letting the
// model keep going (e.g. a hook halt or a denied permission).
func Halt(content string) Result {
	r := Fail(content)
	r.resp.StopTurn = true
	return r
}

// Image returns an image result.
func Image(data []byte, mediaType string) Result {
	return Result{resp: fantasy.NewImageResponse(data, mediaType)}
}

// Media returns a non-image media result (audio, video, and so on).
func Media(data []byte, mediaType string) Result {
	return Result{resp: fantasy.NewMediaResponse(data, mediaType)}
}

// FromResponse wraps an already-built fantasy.ToolResponse, for call sites
// that construct one directly (e.g. a permission decision response).
func FromResponse(resp fantasy.ToolResponse) Result {
	return Result{resp: resp}
}

// WithMetadata attaches response metadata, mirroring
// fantasy.WithResponseMetadata.
func (r Result) WithMetadata(meta any) Result {
	r.resp = fantasy.WithResponseMetadata(r.resp, meta)
	return r
}

// Response converts to the fantasy.ToolResponse the SDK expects.
func (r Result) Response() fantasy.ToolResponse {
	// NOTE: A Result{} zero value (a tool function with a missing return
	// path) would otherwise present as an empty success; surface it as a
	// visible tool error instead of silently returning nothing.
	if r.resp.Type == "" {
		return fantasy.NewTextErrorResponse("tool returned an empty result")
	}
	return r.resp
}

// NewTool creates a fantasy.AgentTool from a function returning Result.
// This, along with NewParallelTool, is the only place in Angela allowed
// to call fantasy.NewAgentTool / fantasy.NewParallelAgentTool directly
// (enforced by scripts/check_tool_results.sh): every other tool goes
// through here so a Go error can never leave tool code.
func NewTool[T any](
	name, description string,
	fn func(ctx context.Context, params T, call fantasy.ToolCall) Result,
) fantasy.AgentTool {
	return fantasy.NewAgentTool(name, description, adaptResultFunc(fn))
}

// NewParallelTool is NewTool for a tool safe to run in parallel with
// others.
func NewParallelTool[T any](
	name, description string,
	fn func(ctx context.Context, params T, call fantasy.ToolCall) Result,
) fantasy.AgentTool {
	return fantasy.NewParallelAgentTool(name, description, adaptResultFunc(fn))
}

// adaptResultFunc bridges a Result-returning tool function to the
// (fantasy.ToolResponse, error) signature fantasy.AgentTool requires.
// Cancellation is the one failure that must still leave as a Go error —
// fantasy treats it as ending the turn rather than a tool result — so it
// is read off ctx directly instead of trusting the tool function to
// notice and report it.
func adaptResultFunc[T any](
	fn func(ctx context.Context, params T, call fantasy.ToolCall) Result,
) func(ctx context.Context, params T, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	return func(ctx context.Context, params T, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
		result := fn(ctx, params, call)
		if err := ctx.Err(); err != nil {
			return fantasy.ToolResponse{}, err
		}
		return result.Response(), nil
	}
}
