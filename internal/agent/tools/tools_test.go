package tools

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/stretchr/testify/require"
)

// TestNewPermissionDeniedResponse pins the two shapes the model sees
// for a user denial: a bare notice when no reason was given, and the
// reason appended when one was.
func TestNewPermissionDeniedResponse(t *testing.T) {
	t.Parallel()

	t.Run("no reason", func(t *testing.T) {
		t.Parallel()
		resp := NewPermissionDeniedResponse("")
		require.True(t, resp.IsError)
		require.True(t, resp.StopTurn, "a user denial must end the turn")
		require.Equal(t, "User denied permission", resp.Content)
	})

	t.Run("with reason", func(t *testing.T) {
		t.Parallel()
		resp := NewPermissionDeniedResponse("not needed for this task")
		require.True(t, resp.IsError)
		require.True(t, resp.StopTurn)
		require.Equal(t, "User denied permission: not needed for this task", resp.Content)
	})
}

// TestDecisionResponse_UserDenyAndDefault covers the two
// DecisionResponse branches not already pinned elsewhere: an explicit
// user denial, and the fallback for any outcome the switch does not
// name. OutcomeAllow stands in for that fallback case here since the
// switch has no explicit case for it (an allowed call never reaches
// DecisionResponse in production).
func TestDecisionResponse_UserDenyAndDefault(t *testing.T) {
	t.Parallel()

	t.Run("user deny", func(t *testing.T) {
		t.Parallel()
		resp := DecisionResponse(permission.Decision{
			Outcome: permission.OutcomeUserDeny,
			Reason:  "not needed for this task",
		})
		require.True(t, resp.IsError)
		require.True(t, resp.StopTurn, "a user refusal must end the turn")
		require.Equal(t, "User denied permission: not needed for this task", resp.Content)
	})

	t.Run("unnamed outcome falls back to a plain denial", func(t *testing.T) {
		t.Parallel()
		resp := DecisionResponse(permission.Decision{Outcome: permission.OutcomeAllow})
		require.True(t, resp.IsError)
		require.True(t, resp.StopTurn)
		require.Equal(t, "User denied permission", resp.Content)
	})
}
