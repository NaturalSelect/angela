package agent

import (
	"context"
	"sync/atomic"
	"testing"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/stretchr/testify/require"
)

// newCountingTool builds a tool that counts how often it really runs.
func newCountingTool(name string, runs *atomic.Int32) fantasy.AgentTool {
	return tools.NewTool(name, "Counts its runs.",
		func(context.Context, struct{}, fantasy.ToolCall) tools.Result {
			runs.Add(1)
			return tools.Ok("ran")
		})
}

func TestInertToolNeverRunsTheInnerTool(t *testing.T) {
	t.Parallel()

	var runs atomic.Int32
	inner := newCountingTool("counter", &runs)
	wrapped := inertTools([]fantasy.AgentTool{inner})
	require.Len(t, wrapped, 1)

	resp, err := wrapped[0].Run(t.Context(), fantasy.ToolCall{ID: "call-1", Name: "counter", Input: "{}"})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.False(t, resp.StopTurn, "the model must get to read the refusal and answer")
	require.Equal(t, inertToolMessage, resp.Content)
	require.Zero(t, runs.Load(), "a summary request must not act through the host's tools")
}

func TestInertToolAdvertisesTheInnerDefinition(t *testing.T) {
	t.Parallel()

	var runs atomic.Int32
	inner := newCountingTool("counter", &runs)
	wrapped := inertTools([]fantasy.AgentTool{inner})[0]

	require.Equal(t, inner.Info(), wrapped.Info(),
		"the definition sent to the provider must match the host's, or the tools block stops being cached")

	opts := (&sessionAgent{}).getCacheControlOptions()
	wrapped.SetProviderOptions(opts)
	require.Equal(t, opts, inner.ProviderOptions())
	require.Equal(t, opts, wrapped.ProviderOptions())
}
