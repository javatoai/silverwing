package application

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"

	"silverwing-go/internal/delivery"
	"silverwing-go/internal/domain"
)

type tagProbeFlight struct {
	identity string
	done     chan struct{}
	cancel   context.CancelFunc
	records  []TagRecord
	err      error
}

type tagJobProbeFunc func(context.Context, domain.AppConfig, []domain.TaskManifest, bool, []string, func(string, domain.TagOperation)) (bool, error)

func (a *App) DispatchTagProbe(ctx context.Context, action Action, onRecord func(TagRecord)) (ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	m := a.tagJobManager()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ActionResult{}, errors.New("应用正在关闭")
	}
	m.wg.Add(1)
	m.mu.Unlock()
	defer m.wg.Done()
	config, err := a.Config.Load()
	if err != nil {
		return ActionResult{}, err
	}
	_, tasks, err := a.tagHistoryActionScope(config, action)
	if err != nil {
		return ActionResult{}, err
	}
	items, err := a.readTagHistoryRecords(ctx, config, tasks)
	if err != nil {
		return ActionResult{}, err
	}
	ids := historyOperationIDs(action.Payload["operationIds"])
	if action.Kind == "tag.status" {
		ids = []string{textPayload(action, "operationId")}
		if ids[0] == "" {
			return ActionResult{}, errors.New("缺少Tag操作记录ID")
		}
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	found := map[string]bool{}
	selected := make([]TagRecord, 0, len(items))
	for _, item := range items {
		if len(wanted) == 0 || wanted[item.ID] {
			found[item.ID] = true
			selected = append(selected, item)
		}
	}
	for id := range wanted {
		if !found[id] {
			return ActionResult{}, errors.New("Tag状态记录不属于当前任务或已变化")
		}
	}
	emit := func(item TagRecord) {
		if ctx.Err() != nil {
			return
		}
		m.mergeProbeRecord(config, tasks, textPayload(action, "tagJobAccountIdentity"), item)
		if onRecord != nil {
			onRecord(item)
		}
	}
	if action.Kind == "tag.history" || action.Kind == "tag.history.refresh" {
		for _, item := range selected {
			emit(item)
		}
	}
	if action.Kind == "tag.history" {
		return ActionResult{Data: items}, nil
	}
	if action.Kind != "tag.history.refresh" && action.Kind != "tag.status" && action.Kind != "tag.genbu.refresh" {
		return ActionResult{}, errors.New("不支持的Tag探测动作")
	}
	force := action.Kind != "tag.genbu.refresh"
	if _, ok := action.Payload["force"]; ok {
		force = boolPayload(action, "force")
	}
	byLane := map[string][]TagRecord{}
	for _, item := range selected {
		if !domain.TagGenbuProbeEligible(domain.TagOperationState(item.State), item.Tag) {
			continue
		}
		lane := tagProbeLane(config, tasks, item)
		byLane[lane] = append(byLane[lane], item)
	}
	// Keep local history and the existing result shapes, but never schedule a
	// repository probe (or an empty whole-task probe) for unpublished Tags.
	if len(byLane) == 0 {
		if action.Kind == "tag.status" {
			return ActionResult{Data: selected[0]}, nil
		}
		if action.Kind == "tag.history.refresh" {
			return ActionResult{Data: TagHistoryRefreshResult{Records: items}}, nil
		}
		return ActionResult{Data: items}, nil
	}
	type outcome struct {
		records []TagRecord
		err     error
	}
	updates := make(chan outcome, len(byLane)+len(selected))
	done := make(chan struct{})
	go func() {
		var pending = make(chan outcome, len(byLane))
		for lane, records := range byLane {
			go func(lane string, records []TagRecord) {
				result, failure := a.runTagProbeLane(ctx, config, tasks, lane, records, force, func(record TagRecord) {
					select {
					case updates <- outcome{records: []TagRecord{record}}:
					case <-ctx.Done():
					}
				})
				pending <- outcome{result, failure}
			}(lane, records)
		}
		for range len(byLane) {
			result := <-pending
			updates <- outcome{err: result.err}
		}
		close(done)
	}()
	completed := 0
	var statusErr error
	// Only this aggregator calls the UI callback; slow repositories cannot hold
	// another repository's accepted record until the entire round completes.
	for completed < len(byLane) {
		item := <-updates
		if item.records != nil {
			for _, record := range item.records {
				emit(record)
			}
		} else {
			completed++
			statusErr = errors.Join(statusErr, item.err)
		}
	}
	<-done
	// All record sends precede their worker completion; drain any final records.
	for len(updates) > 0 {
		item := <-updates
		for _, record := range item.records {
			emit(record)
		}
	}
	final, readErr := a.readTagHistoryRecords(context.WithoutCancel(ctx), config, tasks)
	statusErr = errors.Join(statusErr, readErr)
	if action.Kind == "tag.history.refresh" {
		refresh := TagHistoryRefreshResult{Records: final}
		if statusErr != nil {
			refresh.StatusError = statusErr.Error()
		}
		return ActionResult{Data: refresh}, nil
	}
	if action.Kind == "tag.status" {
		for _, item := range final {
			if wanted[item.ID] {
				return ActionResult{Data: item}, statusErr
			}
		}
		return ActionResult{}, errors.New("Tag状态记录已移除")
	}
	return ActionResult{Data: final}, statusErr
}

func tagProbeLane(config domain.AppConfig, tasks []domain.TaskManifest, record TagRecord) string {
	for _, task := range tasks {
		if task.TaskDirectoryName != record.TaskID {
			continue
		}
		for _, w := range task.Services {
			if w.GroupServiceID != record.ServiceID || w.ModuleID != record.ModuleID {
				continue
			}
			if w.OriginURL != nil && *w.OriginURL != "" {
				return "remote:" + delivery.NormalizeTagRemote(*w.OriginURL)
			}
			for _, repo := range config.Repositories {
				if repo.ID == w.RepositoryID {
					if repo.OriginURL != nil && *repo.OriginURL != "" {
						return "remote:" + delivery.NormalizeTagRemote(*repo.OriginURL)
					}
					return "git:" + repo.GitCommonDirectory
				}
			}
			return "repository:" + w.RepositoryID
		}
	}
	return "task:" + record.TaskID
}

func (a *App) runTagProbeLane(ctx context.Context, config domain.AppConfig, tasks []domain.TaskManifest, lane string, records []TagRecord, force bool, onRecord func(TagRecord)) ([]TagRecord, error) {
	m := a.tagJobManager()
	ids := make([]string, len(records))
	for i, record := range records {
		ids[i] = record.ID
	}
	sort.Strings(ids)
	identityBytes, _ := json.Marshal(struct {
		Config string
		Force  bool
		Task   string
		IDs    []string
	}{configTagIdentity(config), force, records[0].TaskID, ids})
	identity := string(identityBytes)
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, errors.New("应用正在关闭")
		}
		if active := m.probeFlights[lane]; active != nil {
			m.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-active.done:
			}
			if active.identity == identity {
				for _, record := range active.records {
					onRecord(record)
				}
				return active.records, active.err
			}
			continue
		}
		probeCtx, cancel := context.WithCancel(ctx)
		flight := &tagProbeFlight{identity: identity, done: make(chan struct{}), cancel: cancel}
		m.probeFlights[lane] = flight
		m.wg.Add(1)
		m.mu.Unlock()
		_, failure := m.probe(probeCtx, config, tasks, force, ids, func(directory string, operation domain.TagOperation) {
			record := tagRecord(filepath.Base(directory), delivery.DescribeTagOperation(operation, filepath.Base(directory)))
			flight.records = append(flight.records, record)
			onRecord(record)
		})
		flight.err = failure
		cancel()
		m.mu.Lock()
		delete(m.probeFlights, lane)
		close(flight.done)
		m.mu.Unlock()
		m.wg.Done()
		return flight.records, failure
	}
}

func (m *tagJobManager) mergeProbeRecord(config domain.AppConfig, tasks []domain.TaskManifest, account string, incoming TagRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, job := range m.jobs {
		scope := job.state.Scope
		if scope.TaskID != incoming.TaskID || scope.ConfigIdentity != configTagIdentity(config) || scope.AccountIdentity != account || scope.Home != m.app.Paths.Home || !sameTagHistoryTaskPath(scope.TaskRoot, value(config.TaskRoot)) {
			continue
		}
		owned := false
		for _, task := range tasks {
			if task.TaskDirectoryName == scope.TaskID && task.CreatedAt == scope.TaskCreatedAt && sameTagHistoryTaskPath(scope.TaskPath, filepath.Join(value(config.TaskRoot), task.TaskDirectoryName)) {
				owned = true
				break
			}
		}
		if !owned {
			continue
		}
		changed := false
		for _, work := range job.works {
			current := work.view.Record
			if current != nil && current.ID == incoming.ID && current.Tag == incoming.Tag && current.Updated == incoming.Updated {
				if tagStatusNewer(current.GenbuStatus, incoming.GenbuStatus) {
					continue
				}
				current.GenbuStatus, current.RetryKind = incoming.GenbuStatus, incoming.RetryKind
				changed = true
			}
		}
		if changed {
			m.publishLocked(job)
		}
	}
}

var _ TagProbeBackend = (*App)(nil)
