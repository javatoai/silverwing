package integrations

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"silverwing-go/internal/domain"
)

func estimateFixtureItems(count int) []domain.ParticipatedWorkItem {
	items := make([]domain.ParticipatedWorkItem, count)
	for i := range items {
		items[i] = domain.ParticipatedWorkItem{ID: strconv.Itoa(i), Title: fmt.Sprintf("item-%d", i)}
	}
	return items
}

func TestRequirementPersonalEstimatesBoundConcurrencyAndKeepOrderedProgress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	items := estimateFixtureItems(12)
	started := make(chan string, len(items))
	releaseWave := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstFollowers := make(chan struct{}, requirementEstimateWorkers-1)
	var active, maximum atomic.Int32
	var events []RequirementListProgress
	reporter := &requirementListReporter{ctx: ctx, callback: func(event RequirementListProgress) { events = append(events, event) }}
	type outcome struct {
		completed int
		err       error
	}
	done := make(chan outcome, 1)
	go func() {
		completed, err := enrichRequirementPersonalEstimates(ctx, items, reporter, func(ctx context.Context, item domain.ParticipatedWorkItem) (string, error) {
			now := active.Add(1)
			defer active.Add(-1)
			for prior := maximum.Load(); now > prior && !maximum.CompareAndSwap(prior, now); prior = maximum.Load() {
			}
			started <- item.ID
			index, _ := strconv.Atoi(item.ID)
			if index < requirementEstimateWorkers {
				select {
				case <-releaseWave:
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}
			if item.ID == "0" {
				select {
				case <-releaseFirst:
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}
			if index > 0 && index < requirementEstimateWorkers {
				firstFollowers <- struct{}{}
			}
			if item.ID == "0" || item.ID == "7" {
				return "", errors.New("个人估分：fixture unavailable " + item.ID)
			}
			return item.ID + ".5", nil
		})
		done <- outcome{completed, err}
	}()
	// Other workers must make progress while the first list item is still
	// blocked; its completion remains the first progress/application boundary.
	for i := 0; i < requirementEstimateWorkers; i++ {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("bounded workers did not start concurrently")
		}
	}
	close(releaseWave)
	for i := 1; i < requirementEstimateWorkers; i++ {
		select {
		case <-firstFollowers:
		case <-ctx.Done():
			t.Fatal("later list items did not complete before the first item")
		}
	}
	close(releaseFirst)
	var result outcome
	select {
	case result = <-done:
	case <-ctx.Done():
		t.Fatal("estimate workers did not settle")
	}
	if result.err != nil || result.completed != 10 || maximum.Load() != requirementEstimateWorkers {
		t.Fatalf("unexpected estimate concurrency/result: %+v max=%d", result, maximum.Load())
	}
	for i, item := range items {
		if item.ID != strconv.Itoa(i) || item.Title != fmt.Sprintf("item-%d", i) {
			t.Fatalf("list reordered: %+v", items)
		}
		if i == 0 || i == 7 {
			if item.MySprintEstimateDays != nil || len(item.MetadataWarnings) != 1 || !strings.Contains(item.MetadataWarnings[0], item.ID) {
				t.Fatalf("warning lost or attached to another item: %+v", item)
			}
		} else if item.MySprintEstimateDays == nil || *item.MySprintEstimateDays != item.ID+".5" {
			t.Fatalf("wrong item's estimate: %+v", item)
		}
	}
	if len(events) != len(items) || events[1].Completed != 0 || events[8].Completed != 6 {
		t.Fatalf("progress changed ordered success counts: %+v", events)
	}
	for i, event := range events {
		if event.Sequence != uint64(i+1) || !strings.Contains(event.Message, fmt.Sprintf("第 %d 条", i+1)) {
			t.Fatalf("progress order changed: %+v", events)
		}
	}
}

func TestRequirementPersonalEstimatesCancelStopsQueueAndJoinsReaders(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	items := estimateFixtureItems(100)
	started := make(chan struct{}, requirementEstimateWorkers)
	var calls, finished atomic.Int32
	done := make(chan error, 1)
	go func() {
		_, err := enrichRequirementPersonalEstimates(ctx, items, nil, func(ctx context.Context, item domain.ParticipatedWorkItem) (string, error) {
			calls.Add(1)
			started <- struct{}{}
			<-ctx.Done()
			finished.Add(1)
			return "", ctx.Err()
		})
		done <- err
	}()
	deadline := time.After(5 * time.Second)
	for i := 0; i < requirementEstimateWorkers; i++ {
		select {
		case <-started:
		case <-deadline:
			t.Fatal("workers did not start")
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation became optional metadata: %v", err)
		}
	case <-deadline:
		t.Fatal("cancellation did not join active readers")
	}
	if calls.Load() != requirementEstimateWorkers || finished.Load() != calls.Load() {
		t.Fatalf("queue continued or readers escaped: started=%d finished=%d", calls.Load(), finished.Load())
	}
	for _, item := range items {
		if item.MySprintEstimateDays != nil || len(item.MetadataWarnings) > 0 {
			t.Fatalf("canceled read published metadata: %+v", item)
		}
	}
}
