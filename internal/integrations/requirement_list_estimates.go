package integrations

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

const requirementEstimateWorkers = 4

// One list operation owns these workers. Results and progress are applied by
// the caller in list order, independent of CLI completion order. Cancellation
// stops claiming work and joins every active reader before returning.
func enrichRequirementPersonalEstimates(ctx context.Context, items []domain.ParticipatedWorkItem, progress *requirementListReporter,
	read func(context.Context, domain.ParticipatedWorkItem) (string, error)) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	workContext, cancel := context.WithCancel(ctx)
	defer cancel()
	type estimateResult struct {
		value string
		err   error
	}
	results := make([]estimateResult, len(items))
	ready := make([]chan struct{}, len(items))
	for i := range ready {
		ready[i] = make(chan struct{})
	}
	var workers sync.WaitGroup
	var claim sync.Mutex
	next := 0
	for worker := 0; worker < min(requirementEstimateWorkers, len(items)); worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				claim.Lock()
				if workContext.Err() != nil || next == len(items) {
					claim.Unlock()
					return
				}
				index := next
				next++
				claim.Unlock()
				if workContext.Err() != nil {
					return
				}
				value, err := read(workContext, items[index])
				results[index] = estimateResult{value: value, err: err}
				close(ready[index])
				if process.IsCleanupFailure(err) {
					cancel()
					return
				}
			}
		}()
	}
	joinError := func() error {
		cancel()
		workers.Wait()
		err := ctx.Err()
		for _, result := range results {
			if process.IsCleanupFailure(result.err) {
				err = errors.Join(err, result.err)
			}
		}
		return err
	}
	completed := 0
	for index := range items {
		progress.update("estimates", "running", fmt.Sprintf("正在读取第 %d 条工作项的个人估分…", index+1), completed, len(items))
		select {
		case <-workContext.Done():
			return completed, joinError()
		case <-ready[index]:
		}
		if workContext.Err() != nil {
			return completed, joinError()
		}
		result := results[index]
		if process.IsCleanupFailure(result.err) {
			return completed, joinError()
		}
		if result.err != nil {
			items[index].MetadataWarnings = append(items[index].MetadataWarnings, result.err.Error())
			continue
		}
		formatted := result.value
		items[index].MySprintEstimateDays = &formatted
		completed++
	}
	workers.Wait()
	if ctx.Err() != nil {
		return completed, joinError()
	}
	return completed, nil
}
