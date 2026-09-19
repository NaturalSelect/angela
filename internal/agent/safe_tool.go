package agent

import (
	"context"
	"errors"
	"log/slog"

	"charm.land/fantasy"
)

// safeTool wraps a fantasy.AgentTool so a Go error escaping Run can never
// reach fantasy's step-execution loop. fantasy treats any non-nil error
// from Run as a critical, step-ending failure and classifies it with the
// same logic used for provider/network errors: a permission-denied error
// satisfies the net.Error interface only by syscall.Errno's coincidental
// Timeout()/Temporary() methods, which triggers the same
// exponential-backoff retry as a real rate limit, and anything else falls
// into the "Provider Error" catch-all when the step ends. Downgrading
// here keeps that distinction local to the tool layer regardless of
// whether the inner tool is a builtin, an MCP tool, or anything else
// implementing fantasy.AgentTool directly.
type safeTool struct {
	inner fantasy.AgentTool
}

func newSafeTool(inner fantasy.AgentTool) *safeTool {
	return &safeTool{inner: inner}
}

// wrapToolsWithSafety wraps every tool in the slice with safeTool. It
// must be the outermost wrapper: hookedTool and permissionedTool both
// pass through whatever their inner tool returns, so a stray error would
// otherwise reach fantasy before this decorator ever saw it.
func wrapToolsWithSafety(agentTools []fantasy.AgentTool) []fantasy.AgentTool {
	out := make([]fantasy.AgentTool, len(agentTools))
	for i, tool := range agentTools {
		out[i] = newSafeTool(tool)
	}
	return out
}

func (s *safeTool) Info() fantasy.ToolInfo {
	return s.inner.Info()
}

func (s *safeTool) ProviderOptions() fantasy.ProviderOptions {
	return s.inner.ProviderOptions()
}

func (s *safeTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	s.inner.SetProviderOptions(opts)
}

func (s *safeTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	resp, err := s.inner.Run(ctx, call)
	if err == nil {
		return resp, nil
	}
	// Cancellation is the one failure fantasy must still see as a Go
	// error: it ends the turn instead of being narrated back to the
	// model as a tool result.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return resp, err
	}
	slog.Warn("Tool returned a Go error, downgrading to a tool error result",
		"tool", call.Name, "error", err)
	return fantasy.NewTextErrorResponse(err.Error()), nil
}
