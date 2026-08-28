package model

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/stretchr/testify/require"
)

// branchTestConfig declares one agent of each mode, so a test can pick the
// mode it needs by naming the agent on the session.
func branchTestConfig() *config.Config {
	return &config.Config{
		Agents: map[string]config.Agent{
			"pairing": {ID: "pairing", Mode: config.AgentModeBranch},
			"task":    {ID: "task", Mode: config.AgentModeSubagent},
		},
	}
}

// TestIsBranchSession pins which sessions count as branches. The mode is
// read from config rather than stored on the row, so every case here is
// about resolving an agent name that may not resolve at all.
func TestIsBranchSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sess session.Session
		want bool
	}{
		{
			name: "a child running a branch-mode agent",
			sess: session.Session{ID: "c", ParentSessionID: "p", Agent: "pairing"},
			want: true,
		},
		{
			name: "a child running a sub-agent is not a branch",
			sess: session.Session{ID: "c", ParentSessionID: "p", Agent: "task"},
			want: false,
		},
		{
			name: "a top-level session is never a branch",
			sess: session.Session{ID: "c", Agent: "pairing"},
			want: false,
		},
		{
			name: "a child with no agent recorded",
			sess: session.Session{ID: "c", ParentSessionID: "p"},
			want: false,
		},
		{
			name: "a child naming an agent config no longer defines",
			sess: session.Session{ID: "c", ParentSessionID: "p", Agent: "deleted"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ui := newTestUIWithConfig(t, branchTestConfig())
			require.Equal(t, tt.want, ui.isBranchSession(tt.sess))
		})
	}
}

// TestIsBranchSessionWithoutConfig pins that an unresolved config reads as
// "not a branch" rather than panicking. The UI can render before the
// workspace has settled.
func TestIsBranchSessionWithoutConfig(t *testing.T) {
	t.Parallel()

	ui := newTestUIWithConfig(t, nil)
	require.False(t, ui.isBranchSession(session.Session{
		ID: "c", ParentSessionID: "p", Agent: "pairing",
	}))
}

// TestViewingSubAgentExcludesBranches is the guard that makes a branch
// usable. All five read-only gates hang off viewingSubAgent, so a branch
// answering true there would be locked out of its own editor.
func TestViewingSubAgentExcludesBranches(t *testing.T) {
	t.Parallel()

	t.Run("a branch is not treated as a sub-agent transcript", func(t *testing.T) {
		t.Parallel()

		ui := newTestUIWithConfig(t, branchTestConfig())
		ui.session = &session.Session{ID: "c", ParentSessionID: "p", Agent: "pairing"}
		ui.sessionIsBranch = true

		require.True(t, ui.viewingBranch())
		require.False(t, ui.viewingSubAgent(),
			"a branch must keep its editor: the user is the one driving it")
	})

	t.Run("an ordinary sub-agent stays read only", func(t *testing.T) {
		t.Parallel()

		ui := newTestUIWithConfig(t, branchTestConfig())
		ui.session = &session.Session{ID: "c", ParentSessionID: "p", Agent: "task"}
		ui.sessionIsBranch = false

		require.False(t, ui.viewingBranch())
		require.True(t, ui.viewingSubAgent())
	})

	t.Run("a top-level session is neither", func(t *testing.T) {
		t.Parallel()

		ui := newTestUIWithConfig(t, branchTestConfig())
		ui.session = &session.Session{ID: "root"}

		require.False(t, ui.viewingBranch())
		require.False(t, ui.viewingSubAgent())
	})

	t.Run("no session loaded is neither", func(t *testing.T) {
		t.Parallel()

		ui := newTestUIWithConfig(t, branchTestConfig())

		require.False(t, ui.viewingBranch())
		require.False(t, ui.viewingSubAgent())
	})
}

// TestViewingBranchReadsTheMemoizedFlag pins that the predicate answers from
// the flag settled at load time, not from a fresh config lookup. The status
// line calls it every frame and resolving it reaches through the workspace.
func TestViewingBranchReadsTheMemoizedFlag(t *testing.T) {
	t.Parallel()

	ui := newTestUIWithConfig(t, nil)
	ui.session = &session.Session{ID: "c", ParentSessionID: "p", Agent: "pairing"}
	ui.sessionIsBranch = true

	require.True(t, ui.viewingBranch(),
		"the memoized answer must stand on its own: config is not consulted per frame")
}

// TestSendMessageIsAllowedOnABranch pins the gate that would otherwise
// swallow the user's prompt. sendMessage refuses on a sub-agent transcript;
// a branch exists to be typed into.
func TestSendMessageIsAllowedOnABranch(t *testing.T) {
	t.Parallel()

	ui := newTestUIWithConfig(t, branchTestConfig())
	ui.session = &session.Session{ID: "c", ParentSessionID: "p", Agent: "pairing"}
	ui.sessionIsBranch = true

	require.False(t, ui.viewingSubAgent(),
		"sendMessage rejects on viewingSubAgent, so a branch must not answer true")
}
