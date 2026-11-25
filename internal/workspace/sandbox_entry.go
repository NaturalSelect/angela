package workspace

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// NOTE: A variable so tests can shorten it.
var sandboxStopTimeout = 30 * time.Second

// NOTE: Entering the sandbox cuts a running container server off from the engine socket, and its cleanup then fails and leaks containers. So the servers the sandbox would block are stopped, and their containers gone, before it is entered.
type sandboxEntry struct {
	// NOTE: Nil when the sandbox leaves every container server working.
	blocker  func(server string) error
	hold     func() (release func())
	stop     func(reason func(server string) error) []string
	waitIdle func(ctx context.Context) []string
	restart  func(servers []string)
	enter    func() error
}

func (e sandboxEntry) run(ctx context.Context) error {
	if e.blocker == nil {
		return e.enter()
	}

	release := e.hold()
	defer release()
	stopped := e.stop(e.blocker)

	waitCtx, cancel := context.WithTimeout(ctx, sandboxStopTimeout)
	busy := e.waitIdle(waitCtx)
	cancel()
	if len(busy) > 0 {
		// NOTE: The hold goes first, or the restarted servers would be refused by it.
		release()
		e.restart(stopped)
		return fmt.Errorf("container MCP servers %s did not stop within %s, so the sandbox was not entered", strings.Join(busy, ", "), sandboxStopTimeout)
	}
	if err := e.enter(); err != nil {
		release()
		e.restart(stopped)
		return err
	}
	return nil
}
