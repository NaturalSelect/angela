package containermcp

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolveEndpoint_Order(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dockerSock := filepath.Join(dir, "docker.sock")
	podmanSock := filepath.Join(dir, "podman.sock")
	discoveredDocker := filepath.Join(dir, "discovered-docker.sock")
	discoveredPodman := filepath.Join(dir, "discovered-podman.sock")

	discover := func() []string { return []string{discoveredDocker, discoveredPodman} }

	tests := []struct {
		name string
		env  map[string]string
		want Endpoint
	}{
		{
			name: "docker host before container host",
			env:  map[string]string{"DOCKER_HOST": "unix://" + dockerSock, "CONTAINER_HOST": "unix://" + podmanSock},
			want: Endpoint{Host: "unix://" + dockerSock, Socket: dockerSock},
		},
		{
			name: "container host",
			env:  map[string]string{"CONTAINER_HOST": "unix://" + podmanSock},
			want: Endpoint{Host: "unix://" + podmanSock, Socket: podmanSock},
		},
		{
			name: "first discovered socket",
			want: Endpoint{Host: "unix://" + discoveredDocker, Socket: discoveredDocker},
		},
		{
			name: "tcp has no socket",
			env:  map[string]string{"DOCKER_HOST": "tcp://127.0.0.1:2375"},
			want: Endpoint{Host: "tcp://127.0.0.1:2375"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveEndpoint(envMap(tc.env), discover)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestResolveEndpoint_NothingFound(t *testing.T) {
	t.Parallel()

	_, err := resolveEndpoint(envMap(nil), func() []string { return nil })
	require.ErrorContains(t, err, "no container engine found")
	require.ErrorContains(t, err, "DOCKER_HOST")
	require.NotContains(t, err.Error(), "container_host")
}

func TestParseEndpoint_Errors(t *testing.T) {
	t.Parallel()

	for _, host := range []string{"unix://relative.sock", "unix://", "http://x", "ssh://host", "/run/docker.sock"} {
		_, err := parseEndpoint(host)
		require.Error(t, err, host)
	}
}

func TestParseEndpoint_NamedPipeOnlyOnWindows(t *testing.T) {
	t.Parallel()

	_, err := parseEndpoint("npipe:////./pipe/docker_engine")
	if runtime.GOOS == "windows" {
		require.NoError(t, err)
		return
	}
	require.ErrorContains(t, err, "only supported on Windows")
}

func TestCheckSandbox(t *testing.T) {
	t.Parallel()

	unix := Endpoint{Host: "unix:///run/docker.sock", Socket: "/run/docker.sock"}
	tcp := Endpoint{Host: "tcp://127.0.0.1:2375"}
	allowAll := func(string) bool { return true }
	allowNone := func(string) bool { return false }

	require.NoError(t, checkSandbox("s", unix, false, allowNone))
	require.NoError(t, checkSandbox("s", tcp, false, allowNone))
	require.NoError(t, checkSandbox("s", unix, true, allowAll))

	err := checkSandbox("s", unix, true, allowNone)
	require.ErrorContains(t, err, "/run/docker.sock")
	require.ErrorContains(t, err, "--sandbox-allow-docker-socket")

	err = checkSandbox("s", tcp, true, allowAll)
	require.ErrorContains(t, err, "tcp://127.0.0.1:2375")
	require.ErrorContains(t, err, "unix sockets")
}
