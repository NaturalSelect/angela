package sandbox

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContainerGrants_WithSocketsOptional(t *testing.T) {
	t.Parallel()

	g := ContainerGrants{
		ReadWrite:       []string{"/home/u/.docker"},
		UnixSockets:     []string{"/var/run/docker.sock", "/run/docker.sock"},
		OptionalSockets: []string{"/run/docker.sock", "/run/podman/podman.sock"},
	}
	got := g.withSocketsOptional()

	require.Empty(t, got.UnixSockets)
	require.Equal(t, []string{"/home/u/.docker"}, got.ReadWrite)
	require.Equal(t, []string{"/run/docker.sock", "/run/podman/podman.sock", "/var/run/docker.sock"}, sortedCopy(got.OptionalSockets))
	require.Equal(t, []string{"/var/run/docker.sock", "/run/docker.sock"}, g.UnixSockets, "receiver must not be modified")
}

func TestContainerGrants_EmptyIgnoresOptionalSockets(t *testing.T) {
	t.Parallel()

	require.True(t, ContainerGrants{OptionalSockets: []string{"/var/run/docker.sock"}}.Empty())
}

func TestConfig_WithContainerGrantsLeavesOptionalSocketsOut(t *testing.T) {
	t.Parallel()

	got := Config{}.WithContainerGrants(ContainerGrants{OptionalSockets: []string{"/var/run/docker.sock"}})
	require.Empty(t, got.UnixSockets)
}

func TestDetectContainerGrants_SocketsAreOptionalByDefault(t *testing.T) {
	t.Parallel()

	require.Empty(t, DetectContainerGrants(false, true).UnixSockets)
	require.Empty(t, DetectContainerGrants(true, true).OptionalSockets)
}

func TestContainerGrants_WithStorageOptional(t *testing.T) {
	t.Parallel()

	g := ContainerGrants{
		ReadWrite:       []string{"/home/u/.local/share/containers/storage"},
		UnixSockets:     []string{"/run/podman/podman.sock"},
		OptionalStorage: []string{"/var/lib/containers/storage"},
	}
	got := g.withStorageOptional()

	require.Empty(t, got.ReadWrite)
	require.Equal(t, []string{"/run/podman/podman.sock"}, got.UnixSockets)
	require.Equal(t, []string{"/var/lib/containers/storage", "/home/u/.local/share/containers/storage"}, got.OptionalStorage)
	require.Equal(t, []string{"/home/u/.local/share/containers/storage"}, g.ReadWrite, "receiver must not be modified")
}

func TestContainerGrants_EmptyIgnoresOptionalStorage(t *testing.T) {
	t.Parallel()

	require.True(t, ContainerGrants{OptionalStorage: []string{"/x"}}.Empty())
}

func TestConfig_WithContainerGrantsLeavesOptionalStorageOut(t *testing.T) {
	t.Parallel()

	got := Config{}.WithContainerGrants(ContainerGrants{OptionalStorage: []string{"/x"}})
	require.Empty(t, got.ReadWrite)
}

func TestDetectContainerGrants_StorageIsOptionalByDefault(t *testing.T) {
	t.Parallel()

	require.Empty(t, DetectContainerGrants(true, false).ReadWrite)
	require.Empty(t, DetectContainerGrants(true, true).OptionalStorage)
}

func sortedCopy(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return out
}
