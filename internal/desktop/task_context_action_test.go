package desktop

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestWaitForTaskMenuScopeReturnsWhenQueuedCallbackIsCanceledOrWindowCloses(t *testing.T) {
	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		queued := make(chan func(), 1)
		go func() {
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()
		err := waitForTaskMenuScope(ctx, func(update func()) { queued <- update }, func() bool { return true }, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued UI callback did not honor cancellation: %v", err)
		}
		select {
		case update := <-queued:
			update() // A late UI callback must not block its caller.
		default:
			t.Fatal("scope callback was not queued")
		}
	})

	t.Run("window closed", func(t *testing.T) {
		var closed atomic.Bool
		queued := make(chan func(), 1)
		go func() {
			time.Sleep(10 * time.Millisecond)
			closed.Store(true)
		}()
		err := waitForTaskMenuScope(context.Background(), func(update func()) { queued <- update },
			func() bool { return true }, closed.Load)
		if !errors.Is(err, errTaskContextChanged) {
			t.Fatalf("window destruction did not release the waiter: %v", err)
		}
		select {
		case update := <-queued:
			update()
		default:
			t.Fatal("scope callback was not queued")
		}
	})
}
