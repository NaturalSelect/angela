package model

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/stretchr/testify/require"
)

// TestVariantHelpersSurviveUnreadyAgent pins that the variant shortcuts
// tolerate a session whose agent model has not been probed yet. The
// probe runs off-thread, so a session can be open and rendered while
// activeAgent still returns nil — dereferencing it there was a crash on
// a plain ctrl+e.
func TestVariantHelpersSurviveUnreadyAgent(t *testing.T) {
	pinTTLs(t)

	m := newBusyUI(&countingWorkspace{ready: true})
	m.session = &session.Session{ID: "session-1"}
	m.agentReady = false

	require.Nil(t, m.activeAgent(), "precondition: the model is not known yet")

	require.NotNil(t, m.cycleVariant(), "ctrl+e must warn, not panic")
	require.NotNil(t, m.openVariantsDialog(), "the picker must warn, not panic")
	require.False(t, m.dialog.ContainsDialog(dialog.VariantsID))
}
