package config

import (
	"context"
)

// AuthGeneration reports how many times the provider's credentials have
// been replaced. Read it before triggering interactive re-authentication
// and pass the result to [ConfigStore.WaitForTokenChangeSince]: a
// completion landing between the two calls still satisfies the wait,
// while a completion from some earlier, unrelated login does not.
func (s *ConfigStore) AuthGeneration(providerID string) uint64 {
	s.authSignalMu.Lock()
	defer s.authSignalMu.Unlock()
	return s.authGen[providerID]
}

// authWaiterLocked returns the broadcast channel waiters block on,
// creating it for the first waiter. Caller must hold authSignalMu.
func (s *ConfigStore) authWaiterLocked(providerID string) chan struct{} {
	if s.authSignals == nil {
		s.authSignals = make(map[string]chan struct{})
	}
	ch, ok := s.authSignals[providerID]
	if !ok {
		ch = make(chan struct{})
		s.authSignals[providerID] = ch
	}
	return ch
}

// WaitForTokenChangeSince blocks until the provider's credentials have
// been replaced past the given generation, or ctx is done. It is used by
// OnAuthRefresh callbacks to wait for interactive re-authentication
// before retrying a failed request.
func (s *ConfigStore) WaitForTokenChangeSince(ctx context.Context, providerID string, since uint64) error {
	for {
		s.authSignalMu.Lock()
		if s.authGen[providerID] > since {
			s.authSignalMu.Unlock()
			return nil
		}
		ch := s.authWaiterLocked(providerID)
		s.authSignalMu.Unlock()

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
func (s *ConfigStore) SignalAuthComplete(providerID string) {
	s.authSignalMu.Lock()
	defer s.authSignalMu.Unlock()
	if s.authGen == nil {
		s.authGen = make(map[string]uint64)
	}
	s.authGen[providerID]++
	if ch, ok := s.authSignals[providerID]; ok {
		delete(s.authSignals, providerID)
		close(ch)
	}
}
