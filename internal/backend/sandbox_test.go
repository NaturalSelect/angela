package backend

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/sandbox"
	"github.com/stretchr/testify/require"
)

func TestBackendSandbox_WorkspaceNotFound(t *testing.T) {
	t.Parallel()
	b, _ := newTestBackend(t)

	tests := []struct {
		name string
		call func(t *testing.T) error
	}{
		{"IsInSandbox", func(t *testing.T) error {
			_, err := b.IsInSandbox("nope")
			return err
		}},
		{"EnterSandbox", func(t *testing.T) error {
			return b.EnterSandbox(t.Context(), "nope", sandbox.Config{})
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.ErrorIs(t, tc.call(t), ErrWorkspaceNotFound)
		})
	}
}

// TestBackendSandbox_IsInSandbox confirms IsInSandbox reaches the
// workspace's real sandbox.Sandbox instance without error. It does not
// assert a specific boolean: the value legitimately depends on whether
// the test process itself is already confined (e.g. running inside a
// container), and this test must stay meaningful either way.
func TestBackendSandbox_IsInSandbox(t *testing.T) {
	b, ws, _ := newPublishingWorkspace(t)

	_, err := b.IsInSandbox(ws.ID)
	require.NoError(t, err)
}

// TestBackendSandbox_EnterSandboxNotSupported confirms EnterSandbox
// refuses to restrict an existing workspace: a Backend hosts multiple
// workspaces in one OS process, and Landlock confinement is
// irreversible and process-wide, so entering it for one workspace
// would also restrict every other workspace the daemon serves.
func TestBackendSandbox_EnterSandboxNotSupported(t *testing.T) {
	b, ws, _ := newPublishingWorkspace(t)

	err := b.EnterSandbox(t.Context(), ws.ID, sandbox.Config{ReadWrite: []string{"/tmp"}})
	require.ErrorIs(t, err, sandbox.ErrNotSupported)
}
