package workspace

import (
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/containermcp"
	"github.com/NaturalSelect/angela/internal/sandbox"
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

// NOTE: Not parallel, because it sets an environment variable and the sandbox gate is process wide.
func TestAppWorkspace_EnterSandbox_ReleasesTheStartHoldOnceEntered(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "engine.sock")
	t.Setenv("DOCKER_HOST", "unix://"+sock)
	fx, _ := newAWFixtureWithStore(t)
	fake := &fakeSandbox{}
	fx.app.Sandbox = fake
	cfg := sandbox.Config{ReadWrite: []string{t.TempDir()}}

	require.NoError(t, fx.ws.EnterSandbox(t.Context(), cfg))

	require.True(t, fake.entered)
	require.Equal(t, cfg, fake.enterCfg)
	require.NoError(t, containermcp.CheckSandbox("box", containermcp.Endpoint{Host: "unix://" + sock, Socket: sock}),
		"the hold on new container servers ends with the call, since the fake sandbox records no rules")
}

// NOTE: Not parallel, for the same reasons.
func TestAppWorkspace_EnterSandbox_ReleasesTheStartHoldWhenEnteringFails(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "engine.sock")
	t.Setenv("DOCKER_HOST", "unix://"+sock)
	fx, _ := newAWFixtureWithStore(t)
	fx.app.Sandbox = &fakeSandbox{enterErr: sandbox.ErrNotSupported}

	err := fx.ws.EnterSandbox(t.Context(), sandbox.Config{ReadWrite: []string{t.TempDir()}})

	require.ErrorIs(t, err, sandbox.ErrNotSupported)
	require.NoError(t, containermcp.CheckSandbox("box", containermcp.Endpoint{Host: "unix://" + sock, Socket: sock}))
}
