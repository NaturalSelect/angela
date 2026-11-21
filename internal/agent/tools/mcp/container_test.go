package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/containermcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type fakeContainerSession struct {
	transport mcp.Transport
	exitErr   error
	closed    chan struct{}
	once      sync.Once
}

func newFakeContainerSession(transport mcp.Transport, exitErr error) *fakeContainerSession {
	return &fakeContainerSession{transport: transport, exitErr: exitErr, closed: make(chan struct{})}
}

func (f *fakeContainerSession) Transport() mcp.Transport { return f.transport }
func (f *fakeContainerSession) ExitErr() error           { return f.exitErr }
func (f *fakeContainerSession) Close() error {
	f.once.Do(func() { close(f.closed) })
	return nil
}

func (f *fakeContainerSession) waitClosed(t *testing.T) {
	t.Helper()
	select {
	case <-f.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the container session was not closed")
	}
}

func (f *fakeContainerSession) isClosed() bool {
	select {
	case <-f.closed:
		return true
	default:
		return false
	}
}

// NOTE: Swaps a package variable, so callers must not run in parallel.
func stubOpenContainer(t *testing.T, fn func(containermcp.Endpoint, containermcp.Group) (containerSession, error)) {
	t.Helper()
	old := openContainer
	openContainer = func(_ context.Context, ep containermcp.Endpoint, g containermcp.Group) (containerSession, error) {
		return fn(ep, g)
	}
	t.Cleanup(func() { openContainer = old })
}

type discardCloser struct{}

func (discardCloser) Write(p []byte) (int, error) { return len(p), nil }
func (discardCloser) Close() error                { return nil }

func TestCreateTransport_Container(t *testing.T) {
	var gotEP containermcp.Endpoint
	var gotGroup containermcp.Group
	fake := newFakeContainerSession(&mcp.IOTransport{}, nil)
	stubOpenContainer(t, func(ep containermcp.Endpoint, g containermcp.Group) (containerSession, error) {
		gotEP, gotGroup = ep, g
		return fake, nil
	})

	sock := filepath.Join(t.TempDir(), "test-engine.sock")
	r := shellResolverWithPath(t, map[string]string{"TAG": "1.2"})
	m := config.MCPConfig{
		Type:          config.MCPContainer,
		Image:         "img:$TAG",
		Args:          []string{"--host", "$(echo example.com)"},
		Env:           map[string]string{"MODE": "test"},
		Network:       "none",
		ContainerHost: "unix://" + sock,
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	tr, oauth, err := createTransport(ctx, nil, "box", m, r)
	require.NoError(t, err)
	require.Nil(t, oauth)

	ct, ok := tr.(*containerTransport)
	require.True(t, ok, "expected containerTransport, got %T", tr)
	require.NotNil(t, ct.Transport)

	require.Equal(t, containermcp.Endpoint{Host: "unix://" + sock, Socket: sock}, gotEP)
	require.Equal(t, "box", gotGroup.Server)
	require.Equal(t, "img:1.2", gotGroup.Main.Image)
	require.Equal(t, []string{"--host", "example.com"}, gotGroup.Main.Cmd)
	require.Equal(t, map[string]string{"MODE": "test"}, gotGroup.Main.Env)
	require.Equal(t, "none", gotGroup.Network)

	require.False(t, fake.isClosed(), "the container keeps running while the context lives")
	cancel()
	fake.waitClosed(t)
}

func TestCreateTransport_ContainerErrors(t *testing.T) {
	r := shellResolverWithPath(t, nil)
	opened := false
	stubOpenContainer(t, func(containermcp.Endpoint, containermcp.Group) (containerSession, error) {
		opened = true
		return nil, errors.New("boom")
	})

	t.Run("foreign field", func(t *testing.T) {
		m := config.MCPConfig{Type: config.MCPContainer, Image: "img", Command: "docker"}
		tr, _, err := createTransport(t.Context(), nil, "box", m, r)
		require.ErrorContains(t, err, "invalid container mcp config")
		require.ErrorContains(t, err, "'command'")
		require.Nil(t, tr)
	})
	t.Run("missing image", func(t *testing.T) {
		tr, _, err := createTransport(t.Context(), nil, "box", config.MCPConfig{Type: config.MCPContainer}, r)
		require.ErrorContains(t, err, "has no image")
		require.Nil(t, tr)
	})
	t.Run("bad engine address", func(t *testing.T) {
		m := config.MCPConfig{Type: config.MCPContainer, Image: "img", ContainerHost: "ftp://x"}
		tr, _, err := createTransport(t.Context(), nil, "box", m, r)
		require.ErrorContains(t, err, "must start with unix://")
		require.Nil(t, tr)
	})
	require.False(t, opened, "no container starts when the config is rejected")

	t.Run("engine failure", func(t *testing.T) {
		sock := filepath.Join(t.TempDir(), "test-engine.sock")
		m := config.MCPConfig{Type: config.MCPContainer, Image: "img", ContainerHost: "unix://" + sock}
		tr, _, err := createTransport(t.Context(), nil, "box", m, r)
		require.ErrorContains(t, err, `start container mcp "box": boom`)
		require.Nil(t, tr)
		require.True(t, opened)
	})
}

func TestCreateSession_ContainerEndToEnd(t *testing.T) {
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	server := mcp.NewServer(&mcp.Implementation{Name: "in-container"}, nil)
	serverSession, err := server.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	defer serverSession.Close()

	fake := newFakeContainerSession(clientTransport, nil)
	stubOpenContainer(t, func(containermcp.Endpoint, containermcp.Group) (containerSession, error) {
		return fake, nil
	})

	const name = "box-e2e"
	states.Del(name)
	t.Cleanup(func() { states.Del(name) })

	sock := filepath.Join(t.TempDir(), "test-engine.sock")
	m := config.MCPConfig{Type: config.MCPContainer, Image: "img", ContainerHost: "unix://" + sock}
	sess, err := createSession(t.Context(), nil, name, m, currentGen(name), shellResolverWithPath(t, nil), false)
	require.NoError(t, err)
	require.Equal(t, "in-container", sess.InitializeResult().ServerInfo.Name)

	require.False(t, fake.isClosed())
	require.NoError(t, sess.Close())
	fake.waitClosed(t)
}

func TestCreateSession_ContainerThatExitsEarlyReportsWhy(t *testing.T) {
	fake := newFakeContainerSession(&mcp.IOTransport{
		Reader: io.NopCloser(strings.NewReader("")),
		Writer: discardCloser{},
	}, errors.New("container exited with code 9: bad config"))
	stubOpenContainer(t, func(containermcp.Endpoint, containermcp.Group) (containerSession, error) {
		return fake, nil
	})

	const name = "box-early-exit"
	states.Del(name)
	t.Cleanup(func() { states.Del(name) })

	sock := filepath.Join(t.TempDir(), "test-engine.sock")
	m := config.MCPConfig{Type: config.MCPContainer, Image: "img", ContainerHost: "unix://" + sock}
	sess, err := createSession(t.Context(), nil, name, m, currentGen(name), shellResolverWithPath(t, nil), false)
	require.Error(t, err)
	require.Nil(t, sess)
	require.ErrorContains(t, err, "code 9")
	require.ErrorContains(t, err, "bad config")
	fake.waitClosed(t)

	info, ok := GetState(name)
	require.True(t, ok)
	require.Equal(t, StateError, info.State)
	require.ErrorContains(t, info.Error, "code 9")
}

func TestMaybeContainerErr(t *testing.T) {
	t.Parallel()

	exitErr := errors.New("container exited with code 2")
	exited := &containerTransport{session: newFakeContainerSession(nil, exitErr)}
	running := &containerTransport{session: newFakeContainerSession(nil, nil)}

	t.Run("non-EOF error is unchanged", func(t *testing.T) {
		t.Parallel()
		orig := errors.New("boom")
		require.Equal(t, orig, maybeContainerErr(orig, exited))
	})
	t.Run("EOF from another transport is unchanged", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, io.EOF, maybeContainerErr(io.EOF, &mcp.IOTransport{}))
	})
	t.Run("EOF from a running container is unchanged", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, io.EOF, maybeContainerErr(io.EOF, running))
	})
	t.Run("EOF from an exited container carries the exit", func(t *testing.T) {
		t.Parallel()
		got := maybeContainerErr(io.EOF, exited)
		require.ErrorIs(t, got, io.EOF)
		require.ErrorIs(t, got, exitErr)
	})
	t.Run("closed connection from an exited container carries the exit", func(t *testing.T) {
		t.Parallel()
		got := maybeContainerErr(fmt.Errorf("%w: calling initialize", mcp.ErrConnectionClosed), exited)
		require.ErrorIs(t, got, mcp.ErrConnectionClosed)
		require.ErrorIs(t, got, exitErr)
	})
}

func TestMCPTimeout_Container(t *testing.T) {
	t.Parallel()

	require.Equal(t, 5*time.Minute, mcpTimeout(t.Context(), config.MCPConfig{Type: config.MCPContainer}))
	require.Equal(t, 20*time.Second, mcpTimeout(t.Context(), config.MCPConfig{Type: config.MCPContainer, Timeout: 20}))
	require.Equal(t, 10*time.Second, mcpTimeout(t.Context(), config.MCPConfig{Type: config.MCPStdio}))
}
