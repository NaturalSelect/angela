package mcp

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/containermcp"
)

// NOTE: Connects name to an in-memory container server and registers it as running. Swaps a package variable, so callers must not run in parallel.
func connectContainerServer(t *testing.T, name string) (*fakeContainerSession, config.MCPConfig) {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	server := mcp.NewServer(&mcp.Implementation{Name: "in-container"}, nil)
	serverSession, err := server.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })

	fake := newFakeContainerSession(clientTransport, nil)
	stubOpenContainer(t, func(containermcp.Endpoint, containermcp.Group) (containerSession, error) {
		return fake, nil
	})
	cleanupServer(t, name)

	sock := filepath.Join(t.TempDir(), "test-engine.sock")
	cfg := config.NewTestStore(&config.Config{Options: &config.Options{ContainerHost: "unix://" + sock}})
	m := config.MCPConfig{Type: config.MCPContainer, Image: "img"}
	sess, err := createSession(t.Context(), cfg, name, m, currentGen(name), shellResolverWithPath(t, nil), false)
	require.NoError(t, err)
	sessions.Set(name, sess)
	updateState(name, StateConnected, nil, sess, Counts{}, withConfig(m))
	return fake, m
}

func onlyFor(name string, err error) func(string) error {
	return func(got string) error {
		if got == name {
			return err
		}
		return nil
	}
}

func TestStopContainerServers_StopsAConnectedServerAndKeepsTheReason(t *testing.T) {
	const name = "box-stop-connected"
	fake, _ := connectContainerServer(t, name)
	reason := errors.New("needs the container engine socket")
	genBefore := currentGen(name)

	stopped := StopContainerServers(config.NewTestStore(&config.Config{}), onlyFor(name, reason))

	require.Equal(t, []string{name}, stopped)
	fake.waitClosed(t)
	info, ok := GetState(name)
	require.True(t, ok)
	require.Equal(t, StateError, info.State)
	require.Equal(t, reason, info.Error)
	require.Greater(t, currentGen(name), genBefore, "an attempt still in flight reads as stale")
	_, running := sessions.Get(name)
	require.False(t, running)
}

func TestStopContainerServers_LeavesAServerTheSandboxDoesNotBlock(t *testing.T) {
	const name = "box-stop-allowed"
	fake, _ := connectContainerServer(t, name)

	stopped := StopContainerServers(config.NewTestStore(&config.Config{}), onlyFor("another", errors.New("blocked")))

	require.Empty(t, stopped)
	require.False(t, fake.isClosed())
	info, _ := GetState(name)
	require.Equal(t, StateConnected, info.State)
}

func TestStopContainerServers_StopsAServerThatIsStillStarting(t *testing.T) {
	const name = "box-stop-starting"
	cleanupServer(t, name)
	m := config.MCPConfig{Type: config.MCPContainer, Image: "img"}
	updateState(name, StateStarting, nil, nil, Counts{}, withPending(m))
	genBefore := currentGen(name)

	reason := errors.New("blocked")
	stopped := StopContainerServers(config.NewTestStore(&config.Config{}), onlyFor(name, reason))

	require.Equal(t, []string{name}, stopped)
	info, _ := GetState(name)
	require.Equal(t, StateError, info.State)
	require.Equal(t, reason, info.Error)
	require.Greater(t, currentGen(name), genBefore)
}

func TestStopContainerServers_IgnoresOtherTypesAndStates(t *testing.T) {
	blocked := errors.New("blocked")
	// NOTE: Limited to this test's servers, because the state maps are shared with other tests.
	always := func(name string) error {
		if strings.HasPrefix(name, "box-stop-") {
			return blocked
		}
		return nil
	}

	const stdio = "box-stop-stdio"
	cleanupServer(t, stdio)
	updateState(stdio, StateConnected, nil, nil, Counts{}, withConfig(config.MCPConfig{Type: config.MCPStdio, Command: "x"}))

	const disabled = "box-stop-disabled"
	cleanupServer(t, disabled)
	updateState(disabled, StateDisabled, nil, nil, Counts{})

	const failed = "box-stop-failed"
	cleanupServer(t, failed)
	updateState(failed, StateError, errors.New("earlier failure"), nil, Counts{}, withConfig(config.MCPConfig{Type: config.MCPContainer, Image: "img"}))

	stopped := StopContainerServers(config.NewTestStore(&config.Config{}), always)

	require.NotContains(t, stopped, stdio)
	require.NotContains(t, stopped, disabled)
	require.NotContains(t, stopped, failed)
	info, _ := GetState(stdio)
	require.Equal(t, StateConnected, info.State)
	info, _ = GetState(failed)
	require.ErrorContains(t, info.Error, "earlier failure")
}

func TestStopContainerServers_NoReasonStopsNothing(t *testing.T) {
	const name = "box-stop-no-reason"
	fake, _ := connectContainerServer(t, name)

	require.Empty(t, StopContainerServers(config.NewTestStore(&config.Config{}), nil))
	require.False(t, fake.isClosed())
}
