package config

import (
	"context"
	"sync"
)

// authSignals lets a request that failed with an expired credential wait
// for interactive re-authentication to finish. The zero value is ready to
// use.
//
// gen counts how many times each provider's credentials have been
// replaced. waiters holds the broadcast channel current waiters block on,
// which SignalAuthComplete closes and drops. signalMu guards both maps.
type authSignals struct {
	signalMu sync.Mutex
	waiters  map[string]chan struct{}
	gen      map[string]uint64
}

// AuthGeneration reports how many times the provider's credentials have
// been replaced. Read it before triggering interactive re-authentication
// and pass the result to [ConfigStore.WaitForTokenChangeSince]: a
// completion landing between the two calls still satisfies the wait,
// while a completion from some earlier, unrelated login does not.
func (a *authSignals) AuthGeneration(providerID string) uint64 {
	a.signalMu.Lock()
	defer a.signalMu.Unlock()
	return a.gen[providerID]
}

// waiterLocked returns the broadcast channel waiters block on, creating
// it for the first waiter. Caller must hold signalMu.
func (a *authSignals) waiterLocked(providerID string) chan struct{} {
	if a.waiters == nil {
		a.waiters = make(map[string]chan struct{})
	}
	ch, ok := a.waiters[providerID]
	if !ok {
		ch = make(chan struct{})
		a.waiters[providerID] = ch
	}
	return ch
}

// WaitForTokenChangeSince blocks until the provider's credentials have
// been replaced past the given generation, or ctx is done. It is used by
// OnAuthRefresh callbacks to wait for interactive re-authentication
// before retrying a failed request.
func (a *authSignals) WaitForTokenChangeSince(ctx context.Context, providerID string, since uint64) error {
	for {
		a.signalMu.Lock()
		if a.gen[providerID] > since {
			a.signalMu.Unlock()
			return nil
		}
		ch := a.waiterLocked(providerID)
		a.signalMu.Unlock()

		select {
		case <-ch:
			// Re-check the generation rather than returning: the
			// wake-up says something changed, not that it was this
			// provider's credentials moving past `since`.
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// SignalAuthComplete records that the provider's credentials were
// replaced and wakes every waiter. Callers do not need to know whether
// anyone is waiting: the generation carries the fact forward, so a
// signal that arrives before its waiter is still observed.
func (a *authSignals) SignalAuthComplete(providerID string) {
	a.signalMu.Lock()
	defer a.signalMu.Unlock()
	if a.gen == nil {
		a.gen = make(map[string]uint64)
	}
	a.gen[providerID]++
	if ch, ok := a.waiters[providerID]; ok {
		delete(a.waiters, providerID)
		close(ch)
	}
}
