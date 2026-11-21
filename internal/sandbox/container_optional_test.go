package sandbox

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContainerGrants_WithSocketsOptional(t *testing.T) {
	t.Parallel()

	g := ContainerGrants{
		UnixSockets:     []string{"/var/run/docker.sock", "/run/docker.sock"},
		OptionalSockets: []string{"/run/docker.sock", "/run/podman/podman.sock"},
	}
	got := g.withSocketsOptional()

	require.Empty(t, got.UnixSockets)
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

	require.Empty(t, DetectContainerGrants(false).UnixSockets)
	require.Empty(t, DetectContainerGrants(true).OptionalSockets)
}

func TestContainerGrants_WithSockets(t *testing.T) {
	t.Parallel()

	base := ContainerGrants{UnixSockets: []string{"/run/docker.sock"}, OptionalSockets: []string{"/run/podman/podman.sock"}}

	granted := base.WithSockets([]string{"/run/docker.sock", "/tmp/engine.sock"}, true)
	require.Equal(t, []string{"/run/docker.sock", "/tmp/engine.sock"}, sortedCopy(granted.UnixSockets))
	require.Equal(t, base.OptionalSockets, granted.OptionalSockets)

	offered := base.WithSockets([]string{"/tmp/engine.sock"}, false)
	require.Equal(t, base.UnixSockets, offered.UnixSockets, "without the opt-in nothing new is granted")
	require.Equal(t, []string{"/run/podman/podman.sock", "/tmp/engine.sock"}, sortedCopy(offered.OptionalSockets))

	require.Equal(t, base, base.WithSockets(nil, true))
	require.Equal(t, []string{"/run/docker.sock"}, base.UnixSockets, "receiver must not be modified")
}

func sortedCopy(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return out
}
