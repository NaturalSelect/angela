package agent

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// Reconcile skips only primary and hidden agents, so a branch reaches the
// dispatch table without any registration of its own. That is load-bearing
// rather than incidental: excluding branch here would leave a user's
// "subagent_type": "pairing" unroutable while everything else about the
// feature looked wired up. Angela ships no branch itself, so the only branch
// that can ever appear is one the user configured.
func TestReconcileLeavesBranchDispatchable(t *testing.T) {
	t.Parallel()

	r := newSubagentRegistry()
	r.Reconcile(map[string]config.Agent{
		config.AgentCoder:   {ID: config.AgentCoder, Mode: config.AgentModePrimary},
		config.AgentGeneral: {ID: config.AgentGeneral, Mode: config.AgentModeSubagent},
		"pairing":           {ID: "pairing", Mode: config.AgentModeBranch},
		"secret":            {ID: "secret", Mode: config.AgentModeBranch, Hidden: boolPtr(true)},
	}, nil)

	require.Contains(t, r.IDs(), "pairing")
	require.NotContains(t, r.IDs(), config.AgentCoder, "a primary agent is not dispatchable")
	require.NotContains(t, r.IDs(), "secret", "hidden still wins over branch")

	entry, ok := r.Get("pairing")
	require.True(t, ok)
	require.Equal(t, config.AgentModeBranch, entry.cfg.Mode,
		"the entry must carry the mode dispatch reads to pick the branch path")
}

func boolPtr(b bool) *bool { return &b }
