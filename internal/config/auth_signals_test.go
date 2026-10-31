package config

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAuthSignals_SignalBeforeWaitReturnsImmediately(t *testing.T) {
	t.Parallel()

	store := &ConfigStore{}
	since := store.AuthGeneration("p")
	store.SignalAuthComplete("p")

	require.NoError(t, store.WaitForTokenChangeSince(t.Context(), "p", since))
}

func TestAuthSignals_SignalWakesBlockedWaiter(t *testing.T) {
	t.Parallel()

	store := &ConfigStore{}
	since := store.AuthGeneration("p")

	done := make(chan error, 1)
	go func() {
		done <- store.WaitForTokenChangeSince(t.Context(), "p", since)
	}()

	select {
	case err := <-done:
		t.Fatalf("wait returned before any signal: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	store.SignalAuthComplete("p")

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("waiter was not woken by the signal")
	}
}

func TestAuthSignals_ContextCancelReturnsError(t *testing.T) {
	t.Parallel()

	store := &ConfigStore{}
	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)
	go func() {
		done <- store.WaitForTokenChangeSince(ctx, "p", store.AuthGeneration("p"))
	}()

	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("waiter ignored context cancellation")
	}
}

func TestAuthSignals_OtherProviderDoesNotWake(t *testing.T) {
	t.Parallel()

	store := &ConfigStore{}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- store.WaitForTokenChangeSince(ctx, "a", store.AuthGeneration("a"))
	}()

	store.SignalAuthComplete("b")

	require.ErrorIs(t, <-done, context.DeadlineExceeded)
}

func TestAuthSignals_EarlierSignalDoesNotSatisfyNewWait(t *testing.T) {
	t.Parallel()

	store := &ConfigStore{}
	store.SignalAuthComplete("p")
	since := store.AuthGeneration("p")
	require.Equal(t, uint64(1), since)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	require.ErrorIs(t, store.WaitForTokenChangeSince(ctx, "p", since), context.DeadlineExceeded)
}

func TestAuthSignals_SignalWakesEveryWaiter(t *testing.T) {
	t.Parallel()

	store := &ConfigStore{}
	since := store.AuthGeneration("p")

	const waiters = 4
	done := make(chan error, waiters)
	for range waiters {
		go func() {
			done <- store.WaitForTokenChangeSince(t.Context(), "p", since)
		}()
	}

	time.Sleep(50 * time.Millisecond)
	store.SignalAuthComplete("p")

	for range waiters {
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("a waiter was not woken by the signal")
		}
	}
}
