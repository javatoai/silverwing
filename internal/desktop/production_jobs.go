package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
)

type productionWindow interface {
	Update(func())
	Invalidate()
	IsDestroyed() bool
}

type productionView struct {
	Selection                               production.Selection
	BatchID                                 string
	Preview                                 *production.Preview
	ManualTag, Version, Branch, BranchQuery string
	SuggestedBranch                         string
	Error, Message                          string
	Busy                                    bool
	OpenedConflict                          string
	Generation                              uint64
	ActiveStep                              int
	Steps                                   map[int]productionStepView
	PreparationInvalid                      bool
	HistoryExpanded, BranchHistoryExpanded  bool
	TagHistoryExpanded                      bool
	BuildChecked                            bool
	ActiveAction, RefreshAfter              string
	request                                 *productionWorker
}

type productionWorker struct {
	cancel         context.CancelFunc
	done           chan struct{}
	identity       productionResultIdentity
	silentProgress bool
}

type productionServiceChoice struct {
	application.Service
	GroupName, RepositoryName, RepositoryPath string
	Historical                                bool
}

func productionChoice(service application.Service, snapshot application.Snapshot) productionServiceChoice {
	choice := productionServiceChoice{Service: service, GroupName: service.GroupID, RepositoryName: service.RepositoryID}
	for _, group := range snapshot.Groups {
		if group.ID == service.GroupID {
			choice.GroupName = firstNonEmpty(group.Name, group.ID)
			break
		}
	}
	for _, repository := range snapshot.Repositories {
		if repository.ID == service.RepositoryID {
			choice.RepositoryName, choice.RepositoryPath = firstNonEmpty(repository.Name, repository.ID), repository.RootPath
			break
		}
	}
	return choice
}

func (choice productionServiceChoice) matches(query string) bool {
	return strings.Contains(strings.ToLower(strings.Join([]string{choice.Name, choice.GroupID, choice.GroupName, choice.RepositoryID, choice.RepositoryName, choice.RepositoryPath, choice.MasterBranch}, " ")), strings.ToLower(strings.TrimSpace(query)))
}

// A removed service must not strand its active repository batch. These choices
// are for historical access and explicit ending; they never resume remote work.
func (state *productionUIState) addHistoricalChoices(snapshot application.Snapshot) {
	present := map[string]bool{}
	for _, choice := range state.choices {
		present[productionKey(production.Selection{GroupID: choice.GroupID, ServiceID: choice.ID})] = true
	}
	for _, b := range state.batches {
		key := productionKey(b.Target.Selection)
		if present[key] {
			continue
		}
		choice := productionChoice(application.Service{ID: b.Target.ServiceID, GroupID: b.Target.GroupID, Name: b.Target.ServiceName + "（历史）", RepositoryID: b.Target.RepositoryID, MasterBranch: b.Target.Remote + "/" + b.Target.MainBranch}, snapshot)
		choice.RepositoryPath, choice.Historical = b.Target.Repository, true
		state.choices = append(state.choices, choice)
		present[key] = true
		if state.views[key] == nil {
			state.views[key] = &productionView{Selection: b.Target.Selection}
		}
	}
	if len(state.choices) > 0 && (state.selected == "" || state.selected == "__history__") {
		choice := state.choices[0]
		state.selected = productionKey(production.Selection{GroupID: choice.GroupID, ServiceID: choice.ID})
	}
}

type productionUIState struct {
	selected             string
	views                map[string]*productionView
	choices              []productionServiceChoice
	batches              []production.Batch
	deletedBatches       map[string]bool
	loadedHome           string
	configScope          string
	window               productionWindow
	mu                   sync.Mutex
	closed               bool
	workers              map[*productionWorker]struct{}
	timers               map[string]*time.Timer
	baselineDialog       *productionBaselineDialog
	diffDialog           *productionDiffDialog
	scopeJSON, scopeMemo string
}

// Rendering compares strings; decoding configuration is only needed when it changes.
func (s *app) currentProductionScope() string {
	state := s.ensureProduction()
	if state.scopeJSON != s.configJSON || state.scopeMemo == "" {
		state.scopeJSON, state.scopeMemo = s.configJSON, productionConfigScope(s.configJSON)
	}
	return state.scopeMemo
}

func productionKey(selection production.Selection) string {
	return selection.GroupID + "\x00" + selection.ServiceID
}

func productionConfigScope(configJSON string) string {
	var c domain.AppConfig
	if json.Unmarshal([]byte(configJSON), &c) != nil {
		return configJSON
	}
	data, _ := json.Marshal([]any{c.TagEnabled, c.Repositories, c.Groups, c.GitExecutablePath, c.GenbuExecutablePath, c.CommandProxyURL, c.CommandProxyTargets, c.CommandProxyUsername, c.CommandProxyPassword, c.CommandProxyNoProxy, c.BlockedGitWriteBranches, c.DevelopmentTools})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (s *app) ensureProduction() *productionUIState {
	if s.production == nil {
		s.production = &productionUIState{views: map[string]*productionView{}, workers: map[*productionWorker]struct{}{}, timers: map[string]*time.Timer{}}
	}
	return s.production
}

func (s *app) productionView() *productionView {
	state := s.ensureProduction()
	return state.views[state.selected]
}

func (s *app) enterProductionPage() {
	state := s.ensureProduction()
	if state.loadedHome != "" && state.loadedHome != s.snapshot.Home {
		state.mu.Lock()
		for worker := range state.workers {
			worker.cancel()
		}
		for _, timer := range state.timers {
			timer.Stop()
		}
		state.mu.Unlock()
		state.views, state.batches, state.selected = map[string]*productionView{}, nil, ""
		state.deletedBatches = nil
	}
	if state.configScope != productionConfigScope(s.configJSON) {
		for _, view := range state.views {
			view.Preview = nil
			view.Steps = nil
			view.PreparationInvalid = true
			view.BuildChecked = false
		}
	}
	state.configScope = productionConfigScope(s.configJSON)
	var c domain.AppConfig
	_ = json.Unmarshal([]byte(s.configJSON), &c)
	state.choices = nil
	enabled := map[string]bool{}
	for _, group := range c.Groups {
		for _, service := range group.Services {
			enabled[productionKey(production.Selection{GroupID: group.ID, ServiceID: service.ID})] = service.Enabled
		}
	}
	for _, service := range s.snapshot.Services {
		selection := production.Selection{GroupID: service.GroupID, ServiceID: service.ID}
		key := productionKey(selection)
		if !enabled[key] {
			continue
		}
		state.choices = append(state.choices, productionChoice(service, s.snapshot))
		if state.views[key] == nil {
			state.views[key] = &productionView{Selection: selection}
		}
		if state.selected == "" {
			state.selected = key
		}
	}
	state.addHistoricalChoices(s.snapshot)
	if state.selected == "" {
		state.selected = "__history__"
		state.views[state.selected] = &productionView{}
	}
	state.loadedHome = s.snapshot.Home
	s.productionRequest(application.Action{Kind: "production.list"}, "enter")
}

func (s *app) productionBatch(v *productionView) *production.Batch {
	if v == nil || s.production == nil {
		return nil
	}
	for i := range s.production.batches {
		b := &s.production.batches[i]
		if b.ID == v.BatchID {
			return b
		}
	}
	for i := range s.production.batches {
		b := &s.production.batches[i]
		if b.Target.Selection == v.Selection && !b.Ended {
			v.BatchID = b.ID
			return b
		}
	}
	return nil
}

func (s *app) productionInspect(follow string) {
	v := s.productionView()
	if v == nil {
		return
	}
	payload := map[string]any{"groupId": v.Selection.GroupID, "productionTag": v.ManualTag}
	if b := s.productionBatch(v); b != nil && !b.Ended {
		payload["batchId"] = b.ID
	}
	s.productionRequest(application.Action{Kind: "production.inspect", ServiceID: v.Selection.ServiceID, Payload: payload}, follow)
}

func decodeProductionReport(data any) (production.Report, error) {
	if report, ok := data.(production.Report); ok {
		return report, nil
	}
	bytes, err := json.Marshal(data)
	if err != nil {
		return production.Report{}, err
	}
	var report production.Report
	err = json.Unmarshal(bytes, &report)
	return report, err
}

func (state *productionUIState) mergeBatch(batch production.Batch) {
	if state.deletedBatches[batch.ID] {
		return
	}
	for i, existing := range state.batches {
		if existing.ID != batch.ID {
			continue
		}
		oldTime, _ := time.Parse(time.RFC3339Nano, existing.UpdatedAt)
		newTime, _ := time.Parse(time.RFC3339Nano, batch.UpdatedAt)
		if !newTime.Before(oldTime) {
			state.batches[i] = batch
		}
		return
	}
	state.batches = append(state.batches, batch)
}

// Requests have their own lifetime and never take the global task/backend gate.
// Switching pages leaves them running; closing the window cancels and joins them.
func (s *app) productionRequest(action application.Action, follow string) {
	s.productionRequestView(s.productionView(), action, follow)
}

func (s *app) productionRequestView(v *productionView, action application.Action, follow string) {
	s.startProductionRequest(v, action, follow, 0, "")
}

func (s *app) startProductionRequest(v *productionView, action application.Action, follow string, originStep int, refreshAfter string) {
	state := s.ensureProduction()
	if v == nil || v.Busy || s.backend == nil {
		return
	}
	if action.Payload != nil && action.Payload["confirmed"] == true {
		if id, exists := action.Payload["batchId"]; exists && id != v.BatchID {
			v.Error = "批次已切换，请重新确认。"
			return
		}
	}
	window := state.window
	if s.win != nil {
		window = s.win
	}
	if window == nil || window.IsDestroyed() {
		return
	}
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	worker := &productionWorker{cancel: cancel, done: make(chan struct{})}
	state.workers[worker] = struct{}{}
	state.mu.Unlock()
	v.Busy, v.Error, v.Message = true, "", ""
	v.ActiveAction, v.RefreshAfter = action.Kind, refreshAfter
	v.Generation++
	generation := v.Generation
	v.ActiveStep = productionActionStep(action.Kind, follow, s.productionBatch(v))
	if originStep != 0 {
		v.ActiveStep = originStep
	}
	if v.ActiveStep == 5 {
		v.BuildChecked = true
	}
	if v.Steps == nil {
		v.Steps = map[int]productionStepView{}
	}
	priorResult := v.Steps[v.ActiveStep]
	if v.ActiveStep != 0 && refreshAfter == "" {
		v.Steps[v.ActiveStep] = productionStepView{State: "running"}
	}
	if action.Kind == "production.merge" || action.Kind == "production.sync" || action.Kind == "production.merge-main" || action.Kind == "production.merge-release" {
		delete(v.Steps, 5)
		delete(v.Steps, 6)
	}
	if action.Kind == "production.merge" {
		v.BuildChecked = false
	}
	if follow == "build-preview" {
		v.BuildChecked = true
		delete(v.Steps, 5)
		delete(v.Steps, 6)
	}
	if action.Kind == "production.inspect" && (follow == "sync-preview" || follow == "check-preview" || follow == "build-preview") {
		v.PreparationInvalid = true
	}
	home, scope, manual, branch := s.snapshot.Home, s.currentProductionScope(), v.ManualTag, v.Branch
	selectedBatch := v.BatchID
	selection := v.Selection
	identity := productionResultIdentity{Selection: selection, BatchID: selectedBatch, Branch: branch, Generation: generation, Home: home, ConfigScope: scope, ManualTag: manual}
	if source, ok := action.Payload["sourceSha"].(string); ok {
		identity.SourceSHA = source
	}
	if refreshAfter != "" && priorResult.Identity != nil {
		identity.SourceSHA, identity.TargetSHA = priorResult.Identity.SourceSHA, priorResult.Identity.TargetSHA
	}
	if preview, ok := action.Payload["preview"].(production.Preview); ok {
		identity.SourceSHA, identity.TargetSHA = preview.ProductionSHA, preview.MainSHA
		if action.Kind == "production.merge-release" {
			identity.SourceSHA, identity.TargetSHA = preview.MainSHA, preview.ReleaseSHA
		}
	}
	worker.identity, worker.silentProgress = identity, follow == "background-probe"
	v.request = worker
	if step := v.ActiveStep; step != 0 && refreshAfter == "" {
		observed := v.Steps[step]
		observed.Identity = &identity
		v.Steps[step] = observed
	}
	ctx = production.WithProgress(ctx, func(progress production.Progress) {
		if window.IsDestroyed() {
			return
		}
		window.Update(func() {
			state.mu.Lock()
			closed := state.closed
			state.mu.Unlock()
			if closed || !v.Busy || v.Steps == nil || v.Generation != generation || v.Selection != selection || v.BatchID != selectedBatch ||
				s.snapshot.Home != home || s.currentProductionScope() != scope || v.ManualTag != manual || v.Branch != branch ||
				(progress.Selection != selection) {
				return
			}
			if progress.BatchID != "" && action.Kind != "production.create" && !(action.Kind == "production.merge-main" && selectedBatch == "") && progress.BatchID != selectedBatch {
				return
			}
			step := progress.Step
			if step == 5 && (follow == "build-preview" || follow == "build-confirm") {
				step = 6
			}
			if step == 1 && v.ActiveStep != 1 {
				// Internal freshness reads belong to their enclosing operation.
				return
			}
			if step < 1 || step > 6 {
				return
			}
			v.ActiveStep = step
			progressIdentity := identity
			v.Steps[step] = productionStepView{State: progress.State, Message: progress.Message, Identity: &progressIdentity}
			window.Invalidate()
		})
	})
	backend := s.backend
	go func() {
		defer func() {
			cancel()
			state.mu.Lock()
			delete(state.workers, worker)
			close(worker.done)
			state.mu.Unlock()
		}()
		result, err := backend.Dispatch(ctx, action)
		report, decodeErr := decodeProductionReport(result.Data)
		if err == nil && result.Data != nil && action.Kind != "production.open-ide" {
			err = decodeErr
		}
		if window.IsDestroyed() {
			return
		}
		window.Update(func() {
			state.mu.Lock()
			closed := state.closed
			state.mu.Unlock()
			if closed {
				return
			}
			if action.Kind == "production.delete" && err == nil && s.production == state && s.snapshot.Home == home && state.loadedHome == home {
				if v.Generation == generation {
					v.Busy, v.request = false, nil
				}
				s.forgetProductionBatch(state, selectedBatch)
				s.lastMessage = result.Message
				window.Invalidate()
				return
			}
			if v.Generation != generation {
				return
			}
			v.Busy = false
			v.request = nil
			if s.snapshot.Home != home {
				return
			}
			if report.Batch != nil && state.deletedBatches[report.Batch.ID] {
				window.Invalidate()
				return
			}
			if action.Kind == "production.list" && err == nil {
				state.replaceBatches(report.Batches)
				state.addHistoricalChoices(s.snapshot)
			} else {
				if report.Batch != nil {
					state.mergeBatch(*report.Batch)
					if (action.Kind == "production.create" || action.Kind == "production.merge-main" && selectedBatch == "") && v.BatchID == selectedBatch {
						v.BatchID = report.Batch.ID
					}
				}
			}
			if v.Selection != selection || v.ManualTag != manual || v.Branch != branch || (v.BatchID != selectedBatch && (report.Batch == nil || v.BatchID != report.Batch.ID)) {
				v.Preview, v.PreparationInvalid = nil, true
				return
			}
			if s.currentProductionScope() != scope {
				v.Preview, v.Error = nil, "相关配置已变化，请重新检查生产发布目标。"
				v.Steps, v.PreparationInvalid = nil, true
				return
			}
			if err != nil {
				v.Error = err.Error()
				if action.Kind == "production.publish" {
					v.PreparationInvalid = true
				}
				if v.ActiveStep != 0 && v.Steps != nil {
					status, message := "failed", v.Error
					if refreshAfter != "" {
						status, message = "warning", productionRefreshWarning(refreshAfter)+"："+v.Error
					}
					v.Steps[v.ActiveStep] = productionStepView{State: status, Message: message}
				}
			} else {
				v.Message = result.Message
				if step := v.ActiveStep; step != 0 && v.Steps[step].State == "running" {
					// Final results supply the durable status; a busy observation
					// alone must never leave a step running or imply success.
					delete(v.Steps, step)
					if report.Batch != nil && report.Batch.Intent == nil && productionFollowupWrite(action.Kind) {
						v.Steps[step] = productionStepView{State: "complete", Message: report.Batch.Message}
					}
				}
			}
			// Local feedback keeps the immutable target used by this request. It is
			// neither inferred from a translated message nor reused as write authority.
			if step := v.ActiveStep; step != 0 {
				observed, exists := v.Steps[step]
				if exists {
					resultIdentity := identity
					resultIdentity.BatchID = v.BatchID
					observed.Identity = &resultIdentity
					if report.Batch != nil {
						observed.Identity.TargetSHA = report.Batch.ReleaseSHA
					}
					if report.Preview != nil && action.Kind == "production.feature-preview" {
						observed.Identity.SourceSHA, observed.Identity.TargetSHA = report.Preview.BranchHeads[branch], report.Preview.ReleaseSHA
					}
					v.Steps[step] = observed
				}
			}
			if report.Preview != nil && action.Kind != "production.feature-preview" && action.Kind != "production.pending-preview" && v.ManualTag == manual {
				if err == nil {
					v.invalidateProductionBranchResult(report.Preview)
					v.Preview = report.Preview
					v.updateSuggestedBranch(report.Preview.SuggestedVersion)
				}
				if b := s.productionBatch(v); b != nil && b.TagPlan != nil && !productionTagPlanCurrent(v, b) {
					v.PreparationInvalid = true
					delete(v.Steps, 5)
					delete(v.Steps, 6)
				}
			}
			if err == nil && action.Kind == "production.feature-preview" && follow == "feature-preview" && report.Batch != nil && report.Preview != nil && productionFeatureIncluded(*report.Batch, *report.Preview, branch) {
				v.Steps[4] = productionStepView{State: "skipped", Message: "已包含，无需合并", Identity: &productionResultIdentity{Selection: selection, BatchID: v.BatchID, Branch: branch, SourceSHA: report.Preview.BranchHeads[branch], TargetSHA: report.Preview.ReleaseSHA, Generation: generation, Home: home, ConfigScope: scope, ManualTag: manual}}
			}
			if report.Batch != nil {
				b := report.Batch
				if b.TagPlan != nil && v.BatchID == b.ID && v.ManualTag == manual && v.Branch == branch && err == nil &&
					(action.Kind == "production.sync" || action.Kind == "production.resume" || action.Kind == "production.rebase-pending") {
					v.PreparationInvalid = false
				}
				if b.Phase == "CONFLICT" && b.Intent != nil && b.Intent.Merge != nil && v.OpenedConflict != b.Intent.Merge.Worktree {
					v.OpenedConflict = b.Intent.Merge.Worktree
					s.productionRequestFor(v, application.Action{Kind: "production.open-ide", Payload: map[string]any{"batchId": b.ID}}, "")
					return
				}
				if err == nil {
					s.scheduleProductionProbe(v, *b, window)
				}
			}
			if err == nil && (follow == "sync-preview" || follow == "check-preview" || follow == "build-preview") && report.Preview != nil && v.ManualTag == manual {
				next := ""
				if follow == "build-preview" {
					next = "build-confirm"
				}
				s.productionRequestFor(v, application.Action{Kind: "production.sync", Payload: map[string]any{"batchId": v.BatchID, "preview": *report.Preview}}, next)
			}
			visible := s.page == "production" && s.productionView() == v
			if err == nil && visible {
				switch follow {
				case "build-confirm":
					if report.Batch != nil && report.Batch.Intent == nil && productionTagPlanCurrent(v, report.Batch) {
						s.confirmProductionTag(v, *report.Batch)
					}
				case "enter":
					if v == s.productionView() && v.Selection.ServiceID != "" {
						s.productionInspect("")
					}

				case "feature-preview":
					if report.Preview != nil && report.Batch != nil && v.Branch == branch && !productionFeatureIncluded(*report.Batch, *report.Preview, branch) {
						s.confirmProductionFeature(v, *report.Preview, branch)
					}
				case "pending-preview":
					if report.Batch != nil && report.Preview != nil {
						s.confirmProductionPending(v, *report.Batch, *report.Preview)
					}
				case "feature-diff":
					if report.Preview != nil && v.Branch == branch && report.Batch != nil {
						b, p := report.Batch, report.Preview
						s.openProductionDiff(v, "feature → release", branch, b.ReleaseBranch, p.BranchHeads[branch], p.ReleaseSHA, true)
					}
				}
			}
			if err == nil && report.Batch != nil && report.Batch.Intent == nil && v.BatchID == report.Batch.ID && productionFollowupWrite(action.Kind) {
				b := report.Batch
				s.startProductionRequest(v, application.Action{Kind: "production.inspect", Payload: map[string]any{"batchId": b.ID, "productionTag": v.ManualTag}}, "", v.ActiveStep, action.Kind)
			}
			window.Invalidate()
		})
	}()
}

func (s *app) productionRequestFor(v *productionView, action application.Action, follow string) {
	s.productionRequestView(v, action, follow)
}

func (s *app) scheduleProductionProbe(v *productionView, b production.Batch, window productionWindow) {
	if b.Ended || len(b.Tags) == 0 {
		return
	}
	pending := make([]production.Tag, 0, len(b.Tags))
	for _, tag := range b.Tags {
		if tag.Status.Build != domain.GenbuSuccess && tag.Status.Build != domain.GenbuFailed {
			pending = append(pending, tag)
		}
	}
	if len(pending) == 0 {
		return
	}
	state := s.ensureProduction()
	home, scope := s.snapshot.Home, s.currentProductionScope()
	delay := 5 * time.Second
	for _, tag := range pending {
		pushed, _ := time.Parse(time.RFC3339Nano, tag.PushedAt)
		if time.Since(pushed) > 2*time.Minute || tag.Status.FailureReason != nil {
			delay = 30 * time.Second
		}
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return
	}
	if old := state.timers[b.ID]; old != nil {
		old.Stop()
	}
	state.timers[b.ID] = time.AfterFunc(delay, func() {
		if window.IsDestroyed() {
			return
		}
		window.Update(func() {
			state.mu.Lock()
			closed := state.closed
			state.mu.Unlock()
			if closed || s.snapshot.Home != home || s.snapshot.Home != state.loadedHome || s.currentProductionScope() != scope || v.BatchID != b.ID || v.Selection != b.Target.Selection {
				return
			}
			if v.Busy || s.confirmationDialog != nil && s.confirmationDialog.Open {
				// A probe must not take the request gate while the user reads a
				// write confirmation; otherwise Continue could silently lose it.
				s.scheduleProductionProbe(v, b, window)
				return
			}
			s.productionRequestFor(v, application.Action{Kind: "production.refresh-build", Payload: map[string]any{"batchId": b.ID}}, "background-probe")
		})
	})
}

func (s *app) stopProductionJobs() {
	state := s.production
	if state == nil {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.closed = true
	for _, timer := range state.timers {
		timer.Stop()
	}
	for worker := range state.workers {
		worker.cancel()
	}
}

func (s *app) closeProductionJobs(ctx context.Context) error {
	s.stopProductionJobs()
	if s.production == nil {
		return nil
	}
	state := s.production
	state.mu.Lock()
	var done []chan struct{}
	for worker := range state.workers {
		done = append(done, worker.done)
	}
	state.mu.Unlock()
	for _, ch := range done {
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (v *productionView) updateSuggestedBranch(version string) {
	suggested := ""
	if version != "" {
		suggested = "release/" + version
	}
	if v.Version == "" || v.Version == v.SuggestedBranch {
		v.Version = suggested
	}
	v.SuggestedBranch = suggested
}
