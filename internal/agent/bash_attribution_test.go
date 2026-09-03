package agent

import (
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
)

// bashToolDescription returns the bash tool's rendered description,
// which is where the commit attribution template lands.
func bashToolDescription(t *testing.T, toolList []fantasy.AgentTool) string {
	t.Helper()
	for _, tool := range toolList {
		if tool.Info().Name == toolnames.Bash {
			return tool.Info().Description
		}
	}
	t.Fatal("the turn resolved no bash tool")
	return ""
}

// TestBashAttributionNamesTheModelThatRanTheTurn pins C7: the bash
// tool's commit attribution was re-read from the global model slot
// rather than taken from the model the turn had already resolved to.
// A session running its own model therefore signed its commits with
// whatever model the global config happened to name.
func TestBashAttributionNamesTheModelThatRanTheTurn(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	sess, err := coord.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	// The coder sits on the chore slot, so moving the session to the
	// large model makes the two disagree.
	require.NoError(t, editActive(t, coord, sess.ID, config.ActiveAgentEdit{
		Slot:  config.SlotChore,
		Model: &config.SelectedModel{Provider: "mock", Model: "large-model"},
	}))
	require.Equal(t, "small-model", coord.cfg.Config().Slots[config.SlotChore].Model,
		"precondition: the global slot still names the other model")

	active, err := coord.activeAgentFor(t.Context(), sess.ID)
	require.NoError(t, err)
	resolved, err := coord.resolveAgent(t.Context(), active, 0)
	require.NoError(t, err)
	require.Equal(t, "large-model", resolved.Model.ModelCfg.Model,
		"precondition: the turn runs the session's model")

	desc := bashToolDescription(t, resolved.Tools)
	require.Contains(t, desc, "large-model",
		"the commit trailer must credit the model that did the work")
	require.False(t, strings.Contains(desc, "small-model"),
		"the global slot's model must not be credited for a turn it did not run")
}
