package tools

import (
	"context"
	"errors"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

// TestResultConstructors pins the fantasy.ToolResponse each constructor
// produces. Result's field is unexported specifically so tool code can
// only reach fantasy.ToolResponse through these constructors and
// Response(); this test is what actually pins their shape.
func TestResultConstructors(t *testing.T) {
	t.Parallel()

	t.Run("Ok", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, fantasy.ToolResponse{Type: "text", Content: "hi"}, Ok("hi").Response())
	})

	t.Run("Fail", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, fantasy.ToolResponse{Type: "text", Content: "boom", IsError: true}, Fail("boom").Response())
	})

	t.Run("Failf", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, fantasy.ToolResponse{Type: "text", Content: "boom 2", IsError: true}, Failf("boom %d", 2).Response())
	})

	t.Run("FailErr", func(t *testing.T) {
		t.Parallel()
		got := FailErr("error accessing file", errors.New("permission denied")).Response()
		require.Equal(t, fantasy.ToolResponse{Type: "text", Content: "error accessing file: permission denied", IsError: true}, got)
	})

	t.Run("Halt", func(t *testing.T) {
		t.Parallel()
		got := Halt("denied").Response()
		require.Equal(t, fantasy.ToolResponse{Type: "text", Content: "denied", IsError: true, StopTurn: true}, got)
	})

	t.Run("Image", func(t *testing.T) {
		t.Parallel()
		data := []byte{1, 2, 3}
		require.Equal(t, fantasy.ToolResponse{Type: "image", Data: data, MediaType: "image/png"}, Image(data, "image/png").Response())
	})

	t.Run("Media", func(t *testing.T) {
		t.Parallel()
		data := []byte{4, 5, 6}
		require.Equal(t, fantasy.ToolResponse{Type: "media", Data: data, MediaType: "audio/wav"}, Media(data, "audio/wav").Response())
	})

	t.Run("FromResponse", func(t *testing.T) {
		t.Parallel()
		resp := fantasy.NewTextResponse("passthrough")
		require.Equal(t, resp, FromResponse(resp).Response())
	})

	t.Run("WithMetadata", func(t *testing.T) {
		t.Parallel()
		got := Ok("hi").WithMetadata(map[string]int{"n": 1}).Response()
		require.Equal(t, `{"n":1}`, got.Metadata)
	})

	t.Run("zero value surfaces as a tool error instead of an empty success", func(t *testing.T) {
		t.Parallel()
		got := Result{}.Response()
		require.True(t, got.IsError)
		require.Equal(t, "tool returned an empty result", got.Content)
	})
}

// TestNewToolAdapter pins how NewTool bridges a Result-returning tool
// function to the (fantasy.ToolResponse, error) pair fantasy.AgentTool
// requires: cancellation is read off ctx after the tool function
// returns and surfaces as a Go error regardless of the Result the
// function returned, and everything else passes the Result through
// unchanged as a nil-error response.
func TestNewToolAdapter(t *testing.T) {
	t.Parallel()

	t.Run("a normal result passes through with a nil error", func(t *testing.T) {
		t.Parallel()
		tool := NewTool("t", "d", func(context.Context, struct{}, fantasy.ToolCall) Result {
			return Fail("business failure")
		})
		resp, err := tool.Run(t.Context(), fantasy.ToolCall{Name: "t", Input: "{}"})
		require.NoError(t, err)
		require.Equal(t, fantasy.NewTextErrorResponse("business failure"), resp)
	})

	t.Run("an already-canceled ctx surfaces as a Go error, not the tool's Result", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		tool := NewTool("t", "d", func(context.Context, struct{}, fantasy.ToolCall) Result {
			return Ok("this must never reach the model")
		})
		resp, err := tool.Run(ctx, fantasy.ToolCall{Name: "t", Input: "{}"})
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, fantasy.ToolResponse{}, resp)
	})

	t.Run("the tool function canceling ctx mid-execution is caught the same way", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		tool := NewTool("t", "d", func(context.Context, struct{}, fantasy.ToolCall) Result {
			cancel()
			return Ok("this must never reach the model")
		})
		resp, err := tool.Run(ctx, fantasy.ToolCall{Name: "t", Input: "{}"})
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, fantasy.ToolResponse{}, resp)
	})
}

// TestNewParallelToolAdapter pins that NewParallelTool applies the same
// cancellation bridging as NewTool while also marking the tool parallel.
func TestNewParallelToolAdapter(t *testing.T) {
	t.Parallel()

	tool := NewParallelTool("t", "d", func(context.Context, struct{}, fantasy.ToolCall) Result {
		return Ok("hi")
	})
	require.True(t, tool.Info().Parallel)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{Name: "t", Input: "{}"})
	require.NoError(t, err)
	require.Equal(t, fantasy.NewTextResponse("hi"), resp)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	resp, err = tool.Run(ctx, fantasy.ToolCall{Name: "t", Input: "{}"})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, fantasy.ToolResponse{}, resp)
}
