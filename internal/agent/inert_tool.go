package agent

import (
	"context"

	"charm.land/fantasy"
)

const inertToolMessage = "Tools are disabled while the conversation is being summarized."

// inertTool advertises the inner tool's definition and refuses to run
// it. A summary request sends the host's tool definitions so the
// provider sees the same prompt prefix as the turn it summarizes, and
// can serve it from cache. The summary itself must not act, so every
// call is answered with an error instead of reaching the inner tool.
type inertTool struct {
	inner fantasy.AgentTool
}

// inertTools wraps every tool in an inertTool.
func inertTools(ts []fantasy.AgentTool) []fantasy.AgentTool {
	out := make([]fantasy.AgentTool, len(ts))
	for i, t := range ts {
		out[i] = &inertTool{inner: t}
	}
	return out
}

func (t *inertTool) Info() fantasy.ToolInfo {
	return t.inner.Info()
}

func (t *inertTool) ProviderOptions() fantasy.ProviderOptions {
	return t.inner.ProviderOptions()
}

func (t *inertTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	t.inner.SetProviderOptions(opts)
}

func (t *inertTool) Run(context.Context, fantasy.ToolCall) (fantasy.ToolResponse, error) {
	resp := fantasy.NewTextErrorResponse(inertToolMessage)
	resp.StopTurn = true
	return resp, nil
}
