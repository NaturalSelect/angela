package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/containermcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type containerSession interface {
	Transport() mcp.Transport
	ExitErr() error
	Close() error
}

// NOTE: A variable so tests can stand in for a real container engine.
var openContainer = func(ctx context.Context, ep containermcp.Endpoint, g containermcp.Group) (containerSession, error) {
	s, err := containermcp.Open(ctx, ep, g)
	if err != nil {
		return nil, err
	}
	return s, nil
}

type containerTransport struct {
	mcp.Transport
	session containerSession
}

func (t *containerTransport) ExitErr() error { return t.session.ExitErr() }

func createContainerTransport(ctx context.Context, cfg *config.ConfigStore, name string, m config.MCPConfig, resolver config.VariableResolver) (mcp.Transport, error) {
	fieldRes, workDir := resolver, ""
	lockDir, host := "", ""
	if cfg != nil {
		fieldRes, workDir, lockDir = cfg.ContainerFieldResolver(name), cfg.WorkingDir(), cfg.LockDir()
		var err error
		host, err = cfg.ContainerHost()
		if err != nil {
			return nil, err
		}
	}

	g, err := containermcp.FromConfig(ctx, name, m, workDir, fieldRes, resolver)
	if err != nil {
		return nil, fmt.Errorf("invalid container mcp config: %w", err)
	}
	g.LockDir = lockDir
	ep, err := containermcp.ResolveEndpoint(host)
	if err != nil {
		return nil, err
	}
	if err := containermcp.CheckSandbox(name, ep); err != nil {
		return nil, err
	}

	session, err := openContainer(ctx, ep, g)
	if err != nil {
		return nil, fmt.Errorf("start container mcp %q: %w", name, err)
	}
	// NOTE: Cancelling ctx is how a failed connect, a timeout and a teardown all end the server, so it also ends the container.
	context.AfterFunc(ctx, func() { _ = session.Close() })
	return &containerTransport{Transport: session.Transport(), session: session}, nil
}

// NOTE: A container that exits during startup only shows up as a closed connection, so its exit code and stderr tail are appended.
func maybeContainerErr(err error, transport mcp.Transport) error {
	if !errors.Is(err, io.EOF) && !errors.Is(err, mcp.ErrConnectionClosed) {
		return err
	}
	ct, ok := transport.(*containerTransport)
	if !ok {
		return err
	}
	if exitErr := ct.ExitErr(); exitErr != nil {
		err = errors.Join(err, exitErr)
	}
	return err
}
