package containermcp

import (
	"context"
	"maps"
	"slices"
	"sync"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/sandbox"
)

// NOTE: Process wide, like the sandbox it guards, because a process enters one sandbox.
var gate = &startGate{open: map[string]int{}, changed: make(chan struct{})}

// NOTE: Tracks sessions that are opening or open and the sandbox configs about to be entered, under one lock, so a start can never slip between the two.
type startGate struct {
	mu      sync.Mutex
	open    map[string]int
	changed chan struct{}
	holds   []*sandbox.Config
}

func (g *startGate) broadcastLocked() {
	close(g.changed)
	g.changed = make(chan struct{})
}

func (g *startGate) track(server string) func() {
	g.mu.Lock()
	g.open[server]++
	g.mu.Unlock()
	return sync.OnceFunc(func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.open[server]--
		if g.open[server] <= 0 {
			delete(g.open, server)
		}
		g.broadcastLocked()
	})
}

func (g *startGate) busy() ([]string, <-chan struct{}) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Sorted(maps.Keys(g.open)), g.changed
}

func (g *startGate) held() []sandbox.Config {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]sandbox.Config, 0, len(g.holds))
	for _, h := range g.holds {
		out = append(out, *h)
	}
	return out
}

// NOTE: Held while the sandbox is about to be entered, so a server that starts in that window is refused as if it started inside the sandbox.
func HoldSandbox(cfg sandbox.Config) (release func()) {
	h := &cfg
	gate.mu.Lock()
	gate.holds = append(gate.holds, h)
	gate.mu.Unlock()
	return sync.OnceFunc(func() {
		gate.mu.Lock()
		defer gate.mu.Unlock()
		gate.holds = slices.DeleteFunc(gate.holds, func(p *sandbox.Config) bool { return p == h })
	})
}

// NOTE: Returns the servers still opening or open when ctx ends, and nil once none are.
func WaitIdle(ctx context.Context) []string {
	for {
		busy, changed := gate.busy()
		if len(busy) == 0 {
			return nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			busy, _ = gate.busy()
			return busy
		}
	}
}

// NOTE: Returns nil when cfg leaves the engine reachable, so no container server has to stop.
func SandboxBlocker(store *config.ConfigStore, cfg sandbox.Config) func(server string) error {
	if !cfg.Restricts() {
		return nil
	}
	host, err := store.ContainerHost()
	if err != nil {
		return nil
	}
	ep, err := ResolveEndpoint(host)
	if err != nil {
		return nil
	}
	check := func(server string) error {
		return checkSandbox(server, ep, true, cfg.AllowsUnixSocket)
	}
	if check("") == nil {
		return nil
	}
	return check
}
