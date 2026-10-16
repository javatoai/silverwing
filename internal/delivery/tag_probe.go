package delivery

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"silverwing-go/internal/domain"
)

// ProbeWithProgress emits only accepted CAS writes. Different services run
// independently; operations of one service keep their newer-release ordering.
func (s *Service) ProbeWithProgress(ctx context.Context, config domain.AppConfig, tasks []domain.TaskManifest, force bool, ids []string, changedRecord func(string, domain.TagOperation)) (bool, error) {
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	byService := map[string][]genbuCandidate{}
	for _, task := range tasks {
		group, err := config.Group(task.GroupID)
		if err != nil {
			continue
		}
		services := map[string]domain.GroupServiceConfig{}
		for _, service := range group.Services {
			services[service.ID] = service
		}
		directory, err := historyTaskDirectory(config, task)
		if err != nil {
			return false, err
		}
		operations, err := s.store.list(directory)
		if err != nil {
			return false, err
		}
		for _, operation := range operations {
			service, ok := services[operation.GroupServiceID]
			if !ok || !service.GenbuProbeEnabled || !domain.TagGenbuProbeEligible(operation.State, value(operation.Tag)) {
				continue
			}
			byService[service.GenbuServiceName] = append(byService[service.GenbuServiceName], genbuCandidate{directory, operation, service.GenbuServiceName})
		}
	}
	type result struct {
		changed bool
		err     error
	}
	results := make(chan result, len(byService))
	var callbacks sync.Mutex
	emit := func(directory string, operation domain.TagOperation) {
		if changedRecord != nil {
			callbacks.Lock()
			changedRecord(directory, operation)
			callbacks.Unlock()
		}
	}
	workerCount := 0
	for _, candidates := range byService {
		if len(selected) > 0 {
			wanted := false
			for _, item := range candidates {
				wanted = wanted || selected[item.operation.OperationID]
			}
			if !wanted {
				continue
			}
		}
		workerCount++
		go func(candidates []genbuCandidate) {
			changed, err := s.probeCandidates(ctx, config, candidates, force, selected, emit)
			results <- result{changed, err}
		}(candidates)
	}
	changed := false
	var failure error
	for range workerCount {
		item := <-results
		changed = changed || item.changed
		if item.err != nil {
			failure = errors.Join(failure, item.err)
		}
	}
	return changed, failure
}

func (s *Service) probeCandidates(ctx context.Context, config domain.AppConfig, candidates []genbuCandidate, force bool, selected map[string]bool, emit func(string, domain.TagOperation)) (bool, error) {
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].operation.CreatedAt > candidates[j].operation.CreatedAt })
	changed := false
	// Deduplicate identical service/tag queries while still CAS-updating every
	// owning operation. Values are local to this service worker.
	type answer struct {
		value genbuTagQueryResult
		err   error
	}
	queried := map[string]answer{}
	newerReleased := false
	for _, item := range candidates {
		if err := ctx.Err(); err != nil {
			return changed, err
		}
		if !force && newerReleased && item.operation.GenbuStatus.UAT != domain.GenbuSuccess && !item.operation.GenbuStatus.StoppedByNewerRelease {
			current, didChange, err := s.store.updateIfUnchanged(ctx, item.directory, item.operation, func(current domain.TagOperation) domain.TagOperation {
				current.GenbuStatus.StoppedByNewerRelease = true
				return current
			})
			if err != nil {
				return changed, err
			}
			if didChange {
				changed = true
				emit(item.directory, current)
			}
			continue
		}
		if item.operation.GenbuStatus.UAT == domain.GenbuSuccess {
			newerReleased = true
		}
		if (len(selected) > 0 && !selected[item.operation.OperationID]) || (!force && !TagProbeDue(item.operation, time.Now())) {
			continue
		}
		key := item.service + "\x00" + value(item.operation.Tag)
		response, exists := queried[key]
		if !exists {
			response.value, response.err = s.queryGenbu(ctx, config, item.service, value(item.operation.Tag))
			queried[key] = response
		}
		if err := ctx.Err(); err != nil {
			return changed, err
		}
		status := refreshedGenbuStatus(item.operation.GenbuStatus, response.value, response.err)
		current, didChange, err := s.store.updateIfUnchanged(ctx, item.directory, item.operation, func(current domain.TagOperation) domain.TagOperation { current.GenbuStatus = status; return current })
		if err != nil {
			return changed, err
		}
		if didChange {
			changed = true
			emit(item.directory, current)
		}
		if !force && response.err == nil && status.UAT == domain.GenbuSuccess {
			newerReleased = true
		}
	}
	return changed, nil
}

// NotFound never permanently terminates polling. Recent confirmed pushes use
// the caller's active 5s cadence; after two minutes they use the 30s idle pace.
func TagProbeDue(operation domain.TagOperation, now time.Time) bool {
	if !domain.TagGenbuProbeEligible(operation.State, value(operation.Tag)) {
		return false
	}
	status := operation.GenbuStatus
	if status.UAT == domain.GenbuSuccess || status.Build == domain.GenbuFailed || status.StoppedByNewerRelease {
		return false
	}
	if !status.NotFound {
		return true
	}
	pushed, err := domain.ParseSilverWingTime(operation.UpdatedAt)
	if err == nil && now.Sub(pushed) < 2*time.Minute {
		return true
	}
	if status.CheckedAt == nil {
		return true
	}
	checked, err := domain.ParseSilverWingTime(*status.CheckedAt)
	return err != nil || now.Sub(checked) >= 30*time.Second
}
