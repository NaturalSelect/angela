package workspace

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// NOTE: Not parallel, because newTestConfigStore calls t.Setenv. The grants
// depend on which container tools the host has, so these assertions hold on
// any machine: they check where sockets land, not whether any were found.
func TestAppWorkspace_SandboxContainerGrants_SocketsFollowOverride(t *testing.T) {
	store := newTestConfigStore(t)
	ws := NewAppWorkspace(nil, store)

	got := ws.SandboxContainerGrants()
	require.Empty(t, got.UnixSockets, "a daemon socket must not be granted by default")

	store.Overrides().SandboxAllowDockerSocket = true
	got = ws.SandboxContainerGrants()
	require.Empty(t, got.OptionalSockets, "an opted-in socket is granted, not merely offered")
}

// NOTE: Not parallel, because newTestConfigStore calls t.Setenv.
func TestAppWorkspace_SandboxContainerGrants_OptOutBeatsAllowDockerSocket(t *testing.T) {
	store := newTestConfigStore(t)
	disabled := true
	store.Overrides().NoSandboxContainerPermission = &disabled
	store.Overrides().SandboxAllowDockerSocket = true

	got := NewAppWorkspace(nil, store).SandboxContainerGrants()
	require.Empty(t, got.UnixSockets)
	require.Empty(t, got.OptionalSockets)
}
