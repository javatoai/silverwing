package asyncjob_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"silverwing-go/internal/asyncjob"
)

func begin(t *testing.T, group *asyncjob.Group, cancelOnClose bool) *asyncjob.Lease {
	t.Helper()
	lease, err := group.Begin(context.Background(), asyncjob.Options{CancelOnClose: cancelOnClose})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lease.Cancel)
	return lease
}

func TestBeginPreservesParentContext(t *testing.T) {
	type key struct{}
	deadline := time.Now().Add(time.Hour)
	parent, cancel := context.WithDeadline(context.WithValue(context.Background(), key{}, "scope"), deadline)
	defer cancel()
	var group asyncjob.Group
	lease, err := group.Begin(parent, asyncjob.Options{CancelOnClose: true})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Cancel()
	if lease.Context().Value(key{}) != "scope" {
		t.Fatal("request lost its parent's scope")
	}
	if got, ok := lease.Context().Deadline(); !ok || !got.Equal(deadline) {
		t.Fatalf("deadline = %v, %v; want %v", got, ok, deadline)
	}
	cancel()
	if !errors.Is(lease.Context().Err(), context.Canceled) {
		t.Fatal("parent cancellation did not reach the request")
	}
	select {
	case <-lease.Done():
		t.Fatal("cancellation claimed backend cleanup was complete")
	default:
	}
	lease.Complete(nil)
}

func TestCloseRejectsWorkAndWaitsForPreservedRequest(t *testing.T) {
	var group asyncjob.Group
	read := begin(t, &group, true)
	mutation := begin(t, &group, false)
	closing := group.Closing()
	group.Close()
	group.Close()
	if !group.Closed() {
		t.Fatal("close did not reject future work")
	}
	select {
	case <-closing:
	default:
		t.Fatal("close did not release UI-callback waiters")
	}
	if _, err := group.Begin(context.Background(), asyncjob.Options{}); !errors.Is(err, asyncjob.ErrClosed) {
		t.Fatalf("begin after close = %v", err)
	}
	if !errors.Is(read.Context().Err(), context.Canceled) || mutation.Context().Err() != nil {
		t.Fatal("close did not respect the declared cancellation policies")
	}
	read.Complete(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := group.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait completed before the preserved backend result: %v", err)
	}
	select {
	case <-mutation.Done():
		t.Fatal("timing out a wait completed the request")
	default:
	}
	mutation.Complete(nil)
	if err := group.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestBeginDoesNotRetireAnotherRequest(t *testing.T) {
	var group asyncjob.Group
	mutation := begin(t, &group, false)
	replacementRead := begin(t, &group, true)
	replacementRead.Cancel()
	replacementRead.Complete(nil)
	if mutation.Context().Err() != nil {
		t.Fatal("beginning or retiring a read canceled an existing mutation")
	}
	select {
	case <-mutation.Done():
		t.Fatal("a newer request removed the mutation from cleanup tracking")
	default:
	}
	group.Close()
	mutation.Complete(nil)
	if err := group.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCompletePublishesErrorOnceAndRemovesRequest(t *testing.T) {
	var group asyncjob.Group
	lease := begin(t, &group, true)
	cleanupErr := errors.New("backend cleanup failed")
	ignoredErr := errors.New("late completion")
	lease.Complete(cleanupErr)
	lease.Complete(ignoredErr)
	<-lease.Done()
	if !errors.Is(lease.Err(), cleanupErr) || errors.Is(lease.Err(), ignoredErr) {
		t.Fatalf("completion error = %v", lease.Err())
	}
	if err := group.Wait(context.Background()); !errors.Is(err, cleanupErr) || errors.Is(err, ignoredErr) {
		t.Fatalf("group error = %v", err)
	}
	// A new busy period must remain tracked after the old lease was removed.
	second := begin(t, &group, true)
	group.Close()
	second.Complete(nil)
	if err := group.Wait(context.Background()); !errors.Is(err, cleanupErr) {
		t.Fatalf("cleanup evidence was discarded: %v", err)
	}
}

func TestSuccessfulCompletionsDoNotGrowRetainedCleanupErrorTree(t *testing.T) {
	var group asyncjob.Group
	complete := func(completionErr error) {
		t.Helper()
		lease, err := group.Begin(context.Background(), asyncjob.Options{CancelOnClose: true})
		if err != nil {
			t.Fatal(err)
		}
		lease.Complete(completionErr)
		lease.Cancel()
		select {
		case <-lease.Done():
		default:
			t.Fatal("completion did not publish the cleanup signal")
		}
	}
	cleanupErr := errors.New("retained process cleanup failure")
	complete(cleanupErr)
	retainedSize := errorTreeSize(group.Err())
	for range 256 {
		complete(nil)
	}
	if got := errorTreeSize(group.Err()); got != retainedSize {
		t.Fatalf("successful requests grew retained cleanup error tree from %d to %d nodes", retainedSize, got)
	}
	secondErr := errors.New("another process cleanup failure")
	complete(secondErr)
	group.Close()
	if err := group.Wait(context.Background()); !errors.Is(err, cleanupErr) || !errors.Is(err, secondErr) {
		t.Fatalf("successful requests or a later failure lost cleanup evidence: %v", err)
	}
}

// Traverse the public error tree so the regression checks retained structure,
// rather than depending on errors.Join's private implementation or allocations.
func errorTreeSize(err error) int {
	if err == nil {
		return 0
	}
	size := 1
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		for _, child := range wrapped.Unwrap() {
			size += errorTreeSize(child)
		}
	case interface{ Unwrap() error }:
		size += errorTreeSize(wrapped.Unwrap())
	}
	return size
}

func TestWaitPreservesContextAndCleanupErrors(t *testing.T) {
	var group asyncjob.Group
	first := begin(t, &group, true)
	second := begin(t, &group, true)
	cleanupErr := errors.New("first process did not exit")
	first.Complete(cleanupErr)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := group.Wait(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, cleanupErr) {
		t.Fatalf("wait lost context or completed cleanup evidence: %v", err)
	}
	if err := first.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, cleanupErr) {
		t.Fatalf("lease wait lost context or cleanup evidence: %v", err)
	}
	secondErr := errors.New("second process did not exit")
	second.Complete(secondErr)
	group.Close()
	if err := group.Wait(context.Background()); !errors.Is(err, cleanupErr) || !errors.Is(err, secondErr) {
		t.Fatalf("drain discarded a cleanup failure: %v", err)
	}
}

func TestBeginRacingCloseLeavesEveryAcceptedRequestTracked(t *testing.T) {
	const workers = 24
	for range 8 {
		var group asyncjob.Group
		start := make(chan struct{})
		accepted := make(chan *asyncjob.Lease, workers)
		failures := make(chan error, workers)
		var work sync.WaitGroup
		for range workers {
			work.Add(1)
			go func() {
				defer work.Done()
				<-start
				lease, err := group.Begin(context.Background(), asyncjob.Options{CancelOnClose: true})
				if err != nil {
					failures <- err
					return
				}
				accepted <- lease
			}()
		}
		close(start)
		group.Close()
		work.Wait()
		close(accepted)
		close(failures)
		for err := range failures {
			if !errors.Is(err, asyncjob.ErrClosed) {
				t.Fatal(err)
			}
		}
		for lease := range accepted {
			if !errors.Is(lease.Context().Err(), context.Canceled) {
				t.Fatal("accepted request escaped close cancellation")
			}
			select {
			case <-lease.Done():
				t.Fatal("close removed an accepted request before its cleanup")
			default:
			}
			lease.Complete(nil)
			lease.Cancel()
		}
		if err := group.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCancelCompleteAndCloseAreConcurrentAndIdempotent(t *testing.T) {
	var group asyncjob.Group
	lease := begin(t, &group, true)
	var work sync.WaitGroup
	for range 24 {
		work.Go(func() { lease.Cancel() })
		work.Go(func() { lease.Complete(nil) })
		work.Go(func() { group.Close() })
	}
	work.Wait()
	<-lease.Done()
	if err := group.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPostQueuesBeforeApplyAndSkipsQueuedResultsAfterClose(t *testing.T) {
	var group asyncjob.Group
	var queued func()
	ack := make(chan struct{})
	applied := false
	if !group.Post(func(callback func()) bool {
		queued = func() { defer close(ack); callback() }
		return true
	}, func() { applied = true }) {
		t.Fatal("open group rejected a UI post")
	}
	if applied {
		t.Fatal("post ran the result before its UI scheduler")
	}
	group.Close()
	queued()
	<-ack
	if applied {
		t.Fatal("a queued result changed state after shutdown")
	}
	if group.Post(func(func()) bool {
		t.Fatal("closed group called the UI scheduler")
		return true
	}, func() { t.Fatal("closed result applied") }) {
		t.Fatal("closed group accepted a UI post")
	}
}

func TestPostAndCloseSerializeQueueAdmission(t *testing.T) {
	var group asyncjob.Group
	entered := make(chan struct{})
	release := make(chan struct{})
	posted := make(chan bool, 1)
	queued := make(chan func(), 1)
	go func() {
		posted <- group.Post(func(callback func()) bool {
			close(entered)
			<-release
			queued <- callback
			return true
		}, func() { t.Error("callback applied after close") })
	}()
	<-entered
	closed := make(chan struct{})
	go func() { group.Close(); close(closed) }()
	select {
	case <-group.Closing():
		t.Fatal("close crossed an in-flight queue admission")
	default:
	}
	close(release)
	if !<-posted {
		t.Fatal("post admitted before close was rejected")
	}
	<-closed
	(<-queued)()
}

func TestPostSchedulerRejectionDoesNotRunApply(t *testing.T) {
	var group asyncjob.Group
	if group.Post(func(func()) bool { return false }, func() { t.Fatal("rejected result applied") }) {
		t.Fatal("scheduler rejection was hidden")
	}
}
