package pubsub

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBroker_PublishDeliversToSubscribers(t *testing.T) {
	t.Parallel()

	b := NewBroker[string]()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	sub1 := b.Subscribe(ctx)
	sub2 := b.Subscribe(ctx)
	require.Equal(t, 2, b.GetSubscriberCount())

	b.Publish(CreatedEvent, "hello")

	for _, sub := range []<-chan Event[string]{sub1, sub2} {
		select {
		case ev := <-sub:
			require.Equal(t, CreatedEvent, ev.Type)
			require.Equal(t, "hello", ev.Payload)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for event")
		}
	}
}

func TestBroker_PublishDropsWhenBufferFull(t *testing.T) {
	t.Parallel()

	b := NewBrokerWithOptions[int](1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	_ = b.Subscribe(ctx)

	b.Publish(UpdatedEvent, 1) // Fills the buffer.
	b.Publish(UpdatedEvent, 2) // Dropped, buffer is full.

	require.Equal(t, uint64(1), b.DropCount())
}

func TestBroker_Unsubscribe(t *testing.T) {
	t.Parallel()

	b := NewBroker[int]()
	ctx, cancel := context.WithCancel(t.Context())

	sub := b.Subscribe(ctx)
	require.Equal(t, 1, b.GetSubscriberCount())

	cancel()

	require.Eventually(t, func() bool {
		return b.GetSubscriberCount() == 0
	}, time.Second, time.Millisecond)

	_, ok := <-sub
	require.False(t, ok, "channel should be closed after unsubscribe")
}

func TestBroker_Shutdown(t *testing.T) {
	t.Parallel()

	b := NewBroker[int]()
	ctx := t.Context()

	sub := b.Subscribe(ctx)
	b.Shutdown()

	_, ok := <-sub
	require.False(t, ok, "subscriber channel should be closed on shutdown")
	require.Equal(t, 0, b.GetSubscriberCount())

	// Subscribing after shutdown returns an already-closed channel.
	sub2 := b.Subscribe(ctx)
	_, ok = <-sub2
	require.False(t, ok)

	// Publishing after shutdown is a no-op and must not panic.
	b.Publish(CreatedEvent, 1)

	// Shutdown is idempotent.
	b.Shutdown()
}

func TestBroker_PublishMustDeliver(t *testing.T) {
	t.Parallel()

	b := NewBrokerWithOptions[int](1)
	ctx := t.Context()

	sub := b.Subscribe(ctx)

	b.PublishMustDeliver(ctx, CreatedEvent, 1)

	select {
	case ev := <-sub:
		require.Equal(t, 1, ev.Payload)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestBroker_PublishMustDeliverTimesOutAndCounts(t *testing.T) {
	t.Parallel()

	b := NewBrokerWithOptions[int](1)
	b.SetMustDeliverTimeout(10 * time.Millisecond)
	ctx := t.Context()

	_ = b.Subscribe(ctx) // Never drained.

	// First publish fills the buffer via the non-blocking fast path.
	b.PublishMustDeliver(ctx, CreatedEvent, 1)
	// Second publish must fall back to the blocking path and time out.
	b.PublishMustDeliver(ctx, CreatedEvent, 2)

	require.Equal(t, uint64(1), b.MustDeliverDropCount())
}

func TestBroker_PublishMustDeliverCancelledContext(t *testing.T) {
	t.Parallel()

	b := NewBrokerWithOptions[int](1)
	b.SetMustDeliverTimeout(time.Second)
	subCtx := t.Context()
	_ = b.Subscribe(subCtx) // Never drained, buffer size 1.

	b.PublishMustDeliver(subCtx, CreatedEvent, 1) // Fills the buffer.

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	done := make(chan struct{})
	go func() {
		b.PublishMustDeliver(ctx, CreatedEvent, 2)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("PublishMustDeliver should return promptly when context is cancelled")
	}
}

func TestBroker_SetMustDeliverTimeoutResetsOnNonPositive(t *testing.T) {
	t.Parallel()

	b := NewBroker[int]()
	b.SetMustDeliverTimeout(5 * time.Millisecond)
	require.Equal(t, 5*time.Millisecond, b.mustDeliverTimeout)

	b.SetMustDeliverTimeout(0)
	require.Equal(t, defaultMustDeliverTimeout, b.mustDeliverTimeout)

	b.SetMustDeliverTimeout(-1)
	require.Equal(t, defaultMustDeliverTimeout, b.mustDeliverTimeout)
}

func TestBroker_ConcurrentPublishAndSubscribe(t *testing.T) {
	t.Parallel()

	b := NewBroker[int]()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = b.Subscribe(ctx)
		}()
	}
	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b.Publish(UpdatedEvent, i)
		}(i)
	}
	wg.Wait()

	require.LessOrEqual(t, b.GetSubscriberCount(), 10)
}
