package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
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
	Selection                    production.Selection
	BatchID                      string
	Preview                      *production.Preview
	Version, Branch, BranchQuery string
	SuggestedBranch              string
	Error, Message               string
	Busy                         bool
	Generation                   uint64
	ActiveStep                   int
	Steps                        map[int]productionStepView
	PreparationInvalid           bool
	ReleaseStarted               bool
	BuildChecked                 bool
	ActiveAction                 string
	request                      *productionWorker
}

type productionWorker struct {
	cancel          context.CancelFunc
	canceled        atomic.Bool
	done            chan struct{}
	identity        productionResultIdentity
	preview         *production.Preview
	silentProgress  bool
	cancelOnDisable bool
	// UI-thread only: navigation revokes the next automatic build request,
	// without canceling this request or discarding its eventual result.
	buildContinuation bool
}

type productionServiceChoice struct {
	application.Service
	RepositoryName, RepositoryPath string
	Historical                     bool
}

func productionChoice(service application.Service, snapshot application.Snapshot) productionServiceChoice {
	choice := productionServiceChoice{Service: service, RepositoryName: service.RepositoryID}
	for _, repository := range snapshot.Repositories {
		if repository.ID == service.RepositoryID {
			choice.RepositoryName, choice.RepositoryPath = firstNonEmpty(repository.Name, repository.ID), repository.RootPath
			break
		}
	}
	return choice
}

func (choice productionServiceChoice) matches(query string) bool {
	return strings.Contains(strings.ToLower(strings.Join([]string{choice.Name, choice.ID, choice.RepositoryID, choice.RepositoryName, choice.RepositoryPath, choice.MasterBranch}, " ")), strings.ToLower(strings.TrimSpace(query)))
}

// A removed service must not strand its active repository batch. These choices
// are for historical access and explicit ending; they never resume remote work.
func (state *productionUIState) addHistoricalChoices(snapshot application.Snapshot) {
	present := map[string]bool{}
	for _, choice := range state.choices {
		present[productionKey(production.Selection{ServiceID: choice.ID})] = true
	}
	for _, b := range state.batches {
		key := productionKey(b.Target.Selection)
		if present[key] {
			continue
		}
		choice := productionChoice(application.Service{ID: b.Target.ServiceID, Name: b.Target.ServiceName + "（历史）", RepositoryID: b.Target.RepositoryID, MasterBranch: b.Target.Remote + "/" + b.Target.MainBranch}, snapshot)
		choice.RepositoryPath, choice.Historical = b.Target.Repository, true
		state.choices = append(state.choices, choice)
		present[key] = true
		if state.views[key] == nil {
			state.views[key] = &productionView{Selection: b.Target.Selection}
		}
	}
	if !present[state.selected] && len(state.choices) > 0 {
		state.selected = productionKey(production.Selection{ServiceID: state.choices[0].ID})
	} else if !present[state.selected] {
		state.selected = "__history__"
		if state.views[state.selected] == nil {
			state.views[state.selected] = &productionView{}
		}
	}
}

type productionUIState struct {
	selected             string
	views                map[string]*productionView
	choices              []productionServiceChoice
	batches              []production.Batch
	batchRevision        uint64
	deletedBatches       map[string]bool
	entryRead            *productionWorker
	entryPending         bool
	entryError           string
	loadedHome           string
	configScope          string
	window               productionWindow
	mu                   sync.Mutex
	closed               bool
	workers              map[*productionWorker]struct{}
	timers               map[string]*time.Timer
	diffDialog           *productionDiffDialog
	worktreesDialog      *productionWorktreesDialog
	historyDialog        *productionHistoryDialog
	disclosures          productionDetailDisclosures
	scopeJSON, scopeMemo string
}

// Rendering compares strings; decoding configuration is only needed when it changes.
func (s *app) currentProductionScope() string {
	state := s.ensureProduction()
	if state.scopeJSON != s.settingsUnit.state.configJSON || state.scopeMemo == "" {
		state.scopeJSON, state.scopeMemo = s.settingsUnit.state.configJSON, productionConfigScope(s.settingsUnit.state.configJSON)
	}
	return state.scopeMemo
}

func productionKey(selection production.Selection) string {
	return "service:" + selection.ServiceID
}

func productionConfigScope(configJSON string) string {
	var c domain.AppConfig
	if json.Unmarshal([]byte(configJSON), &c) != nil {
		return configJSON
	}
	data, _ := json.Marshal([]any{c.TagEnabled, c.Repositories, c.ServiceCatalog(), c.TaskRoot, c.GitExecutablePath, c.CommandProxyURL, c.CommandProxyTargets, c.CommandProxyUsername, c.CommandProxyPassword, c.CommandProxyNoProxy, c.BlockedGitWriteBranches, c.DevelopmentTools})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (s *app) ensureProduction() *productionUIState {
	if s.releaseUnit.state.production == nil {
		s.releaseUnit.state.production = &productionUIState{views: map[string]*productionView{}, workers: map[*productionWorker]struct{}{}, timers: map[string]*time.Timer{}}
	}
	return s.releaseUnit.state.production
}

func (s *app) productionView() *productionView {
	state := s.ensureProduction()
	return state.views[state.selected]
}

func (s *app) observeProductionBuildNavigation() {
	state := s.releaseUnit.state.production
	if state == nil {
		return
	}
	visible := state.views[state.selected]
	for _, view := range state.views {
		if view != nil && view.request != nil && view.request.buildContinuation && (s.page != "production" || view != visible) {
			view.request.buildContinuation = false
			view.PreparationInvalid = true
		}
	}
}

func (s *app) enterProductionPage() {
	if !s.productionAvailable() {
		return
	}
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
		state.entryRead, state.entryPending, state.entryError = nil, false, ""
	}
	if state.configScope != productionConfigScope(s.settingsUnit.state.configJSON) {
		for _, view := range state.views {
			view.Preview = nil
			view.Steps = nil
			view.PreparationInvalid = true
			view.BuildChecked = false
		}
	}
	state.configScope = productionConfigScope(s.settingsUnit.state.configJSON)
	var c domain.AppConfig
	_ = json.Unmarshal([]byte(s.settingsUnit.state.configJSON), &c)
	state.choices = nil
	enabled := map[string]bool{}
	for _, service := range c.ServiceCatalog() {
		enabled[service.ID] = service.Enabled
	}
	for _, service := range s.snapshot.Services {
		selection := production.Selection{ServiceID: service.ID}
		key := productionKey(selection)
		if !enabled[service.ID] {
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
	state.loadedHome = s.snapshot.Home
	s.inspectProductionOnSelection(s.productionView())
}

func (s *app) productionBatch(v *productionView) *production.Batch {
	if v == nil || s.releaseUnit.state.production == nil {
		return nil
	}
	for i := range s.releaseUnit.state.production.batches {
		b := &s.releaseUnit.state.production.batches[i]
		if b.ID == v.BatchID && b.Target.Selection == v.Selection {
			return b
		}
	}
	if b := s.productionCurrentBatch(v); b != nil {
		v.BatchID = b.ID
		return b
	}
	return nil
}

func (s *app) productionInspect(follow string) {
	v := s.productionView()
	if v == nil {
		return
	}
	if follow == "build-preview" {
		b := s.productionBatch(v)
		if b == nil || b.Ended {
			return
		}
		expected := production.Preview{Target: b.Target}
		if v.Preview != nil {
			expected = *v.Preview
		}
		s.productionRequest(application.Action{Kind: "production.prepare-tag", ServiceID: v.Selection.ServiceID,
			Payload: map[string]any{"batchId": b.ID, "preview": expected}}, "build-publish")
		return
	}
	payload := map[string]any{}
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
		if existing.Ended && !batch.Ended {
			return
		}
		oldTime, _ := time.Parse(time.RFC3339Nano, existing.UpdatedAt)
		newTime, _ := time.Parse(time.RFC3339Nano, batch.UpdatedAt)
		if !newTime.Before(oldTime) || batch.Ended && !existing.Ended {
			state.batches[i] = batch
			state.batchRevision++
		}
		return
	}
	state.batches = append(state.batches, batch)
	state.batchRevision++
}

// Requests have their own lifetime and never take the global task/backend gate.
// Switching pages leaves them running; closing the window cancels and joins them.
func (s *app) productionRequest(action application.Action, follow string) {
	s.productionRequestView(s.productionView(), action, follow)
}

func (s *app) productionRequestView(v *productionView, action application.Action, follow string) {
	s.productionRequestViewFromHistory(v, action, follow, nil)
}

// History reads and row deletion share the normal worker lifetime and busy gate,
// but never replace the current operation's feedback or run a follow-up action.
func (s *app) productionRequestViewFromHistory(v *productionView, action application.Action, follow string, history *productionHistoryDialog) {
	if !s.productionAvailable() || v == nil || v.Busy || s.backend == nil {
		return
	}
	state := s.ensureProduction()
	entry := action.Kind == "production.list" && follow == "enter" && history == nil
	if entry && state.entryRead != nil {
		return
	}
	if history != nil && !s.productionHistoryRequestCurrent(history, action) {
		return
	}
	if action.Payload != nil && action.Payload["confirmed"] == true {
		if id, exists := action.Payload["batchId"]; exists && id != v.BatchID && history == nil {
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
	worker := &productionWorker{done: make(chan struct{}), buildContinuation: follow == "build-preview" || follow == "build-publish"}
	switch action.Kind {
	case "production.list", "production.inspect", "production.feature-preview", "production.pending-preview", "production.refresh-build":
		worker.cancelOnDisable = true
	}
	worker.cancel = func() {
		worker.canceled.Store(true)
		cancel()
	}
	state.workers[worker] = struct{}{}
	state.mu.Unlock()
	entryRevision := state.batchRevision
	if entry {
		state.entryRead, state.entryError = worker, ""
	}
	optionalCtx := s.integrationContext("genbu")
	v.Busy = true
	if history == nil && !entry {
		v.Generation++
	}
	generation := v.Generation
	if history == nil && !entry {
		v.Error, v.Message = "", ""
		v.ActiveAction = action.Kind
		v.ActiveStep = productionActionStep(action.Kind, follow, s.productionBatch(v))
		if v.ActiveStep == 5 {
			v.BuildChecked = true
		}
		if v.Steps == nil {
			v.Steps = map[int]productionStepView{}
		}
		if v.ActiveStep != 0 {
			v.Steps[v.ActiveStep] = productionStepView{State: "running"}
		}
		if action.Kind == "production.merge" || action.Kind == "production.sync" || action.Kind == "production.prepare-tag" || action.Kind == "production.merge-main" || action.Kind == "production.merge-release" {
			delete(v.Steps, 5)
			delete(v.Steps, 6)
		}
		if action.Kind == "production.merge" {
			v.BuildChecked = false
		}
		if action.Kind == "production.abort-merge" {
			v.PreparationInvalid, v.BuildChecked = true, false
			delete(v.Steps, 5)
			delete(v.Steps, 6)
		}
		if follow == "build-preview" || action.Kind == "production.prepare-tag" {
			v.BuildChecked = true
			delete(v.Steps, 5)
			delete(v.Steps, 6)
		}
		if action.Kind == "production.prepare-tag" || action.Kind == "production.inspect" && (follow == "sync-preview" || follow == "check-preview" || follow == "build-preview") {
			v.PreparationInvalid = true
		}
	}
	home, scope, branch, previewAtStart := s.snapshot.Home, s.currentProductionScope(), v.Branch, v.Preview
	selectedBatch := v.BatchID
	selection := v.Selection
	completedAction := ""
	if history == nil && !entry {
		completedAction = productionCompletedAction(action.Kind, s.productionBatch(v))
	}
	identity := productionResultIdentity{Selection: selection, BatchID: selectedBatch, Branch: branch, Generation: generation, Home: home, ConfigScope: scope}
	if source, ok := action.Payload["sourceSha"].(string); ok {
		identity.SourceSHA = source
	}
	if preview, ok := action.Payload["preview"].(production.Preview); ok {
		identity.SourceSHA, identity.TargetSHA = preview.ProductionSHA, preview.MainSHA
		if action.Kind == "production.merge-release" {
			identity.SourceSHA, identity.TargetSHA = preview.MainSHA, preview.ReleaseSHA
		}
	}
	worker.identity, worker.preview = identity, previewAtStart
	worker.silentProgress = entry || follow == "background-probe" || history != nil
	v.request = worker
	if history != nil {
		history.request = worker
	}
	if step := v.ActiveStep; step != 0 && history == nil && !entry {
		observed := v.Steps[step]
		observed.Identity = &identity
		v.Steps[step] = observed
	}
	ctx = production.WithProgress(ctx, func(progress production.Progress) {
		if window.IsDestroyed() || history != nil || entry {
			return
		}
		window.Update(func() {
			if optionalCtx.Err() != nil || v.Steps == nil || !s.productionProgressCurrent(state, v, worker) || progress.Selection != selection {
				return
			}
			if progress.BatchID != "" && action.Kind != "production.create" && !(action.Kind == "production.merge-main" && selectedBatch == "") && progress.BatchID != selectedBatch {
				return
			}
			step := progress.Step
			if step == 5 && (follow == "build-preview" || follow == "build-publish") {
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
			defer func() {
				if state.entryPending && s.releaseUnit.state.production == state && s.page == "production" {
					if current := s.productionView(); current != nil && !current.Busy {
						s.inspectProductionOnSelection(current)
					}
				}
			}()
			if entry {
				if err == nil && result.Data == nil {
					err = fmt.Errorf("未读取到批次列表")
				}
				s.finishProductionEntryRead(state, v, worker, entryRevision, report, err, optionalCtx)
				window.Invalidate()
				return
			}
			if optionalCtx.Err() != nil || !s.productionAvailable() {
				if v.request == worker {
					v.Busy, v.request = false, nil
					if s.productionAvailable() && s.page == "production" && s.productionView() == v {
						s.productionRequestFor(v, application.Action{Kind: "production.list"}, "enter")
					}
				}
				window.Invalidate()
				return
			}
			if history != nil {
				s.finishProductionHistoryRequest(history, action, worker, generation, report, result, err)
				window.Invalidate()
				return
			}
			s.observeProductionBuildNavigation()
			if err == nil && (follow == "build-preview" || follow == "build-publish") && worker.canceled.Load() {
				// The read may have completed just before Cancel. Its queued
				// result must not authorize the next build request.
				err = context.Canceled
			}
			if action.Kind == "production.delete" && err == nil && s.releaseUnit.state.production == state && s.snapshot.Home == home && state.loadedHome == home && s.currentProductionScope() == scope {
				if v.Generation == generation {
					v.Busy, v.request = false, nil
				}
				if !state.productionDeleteRecordUnchanged(action) {
					if v.Generation == generation {
						v.Error = "批次记录已变化，请重新读取后核对。"
					}
					window.Invalidate()
					return
				}
				deletedID, _ := action.Payload["batchId"].(string)
				s.forgetProductionBatch(state, deletedID)
				s.lastMessage = result.Message
				window.Invalidate()
				return
			}
			if v.Generation != generation || v.request != worker {
				return
			}
			v.Busy = false
			v.request = nil
			if s.releaseUnit.state.production != state || s.snapshot.Home != home {
				return
			}
			if action.Kind == "production.abort-merge" {
				// An abort completes one exact merge, never a newer pending merge
				// that a refreshed snapshot or another view has since installed.
				if s.currentProductionScope() != scope || v.Selection != selection ||
					v.BatchID != selectedBatch || v.Preview != previewAtStart || v.Branch != branch || !s.productionAbortResultCurrent(v, action, report.Batch) {
					window.Invalidate()
					return
				}
			}
			if report.Batch != nil && state.deletedBatches[report.Batch.ID] {
				window.Invalidate()
				return
			}
			if action.Kind == "production.end" {
				if s.releaseUnit.state.production != state {
					return
				}
				id, _ := action.Payload["batchId"].(string)
				if err == nil && (id == "" || id != selectedBatch || report.Batch == nil || report.Batch.ID != id || !report.Batch.Ended || report.Batch.Target.ServiceID != selection.ServiceID) {
					err = fmt.Errorf("未能确认批次已结束，请重新读取后核对。")
				}
				if err == nil {
					state.mergeBatch(*report.Batch)
					state.stopProductionProbe(id)
					stillSelectedBatch := v.BatchID == id && v.Selection == selection
					if stillSelectedBatch {
						v.resetProductionRelease()
					}
					if s.currentProductionScope() == scope {
						s.lastMessage = result.Message
						if follow == "end-start" && stillSelectedBatch {
							s.inspectProductionOnSelection(v)
						}
					}
					window.Invalidate()
					return
				}
				// End can return an attempted state when saving fails. Keep the
				// confirmed active batch and surface the error until a disk read.
				report.Batch = nil
			}
			if action.Kind == "production.list" && err == nil {
				state.replaceBatches(report.Batches)
				state.addHistoricalChoices(s.snapshot)
			} else {
				if report.Batch != nil {
					incoming := *report.Batch
					if err != nil {
						// A returned batch can contain a merge appended before Save
						// failed. Only successful results or a subsequent list read
						// confirm new history; retain previously confirmed records.
						incoming.Branches = nil
						for _, existing := range state.batches {
							if existing.ID == incoming.ID {
								incoming.Branches = existing.Branches
								break
							}
						}
					}
					state.mergeBatch(incoming)
					if (action.Kind == "production.create" || action.Kind == "production.merge-main" && selectedBatch == "") && v.BatchID == selectedBatch && v.Preview == previewAtStart {
						v.BatchID = report.Batch.ID
					}
				}
			}
			if state.views[productionKey(selection)] != v || v.Preview != previewAtStart {
				window.Invalidate()
				return
			}
			if v.Selection != selection || v.Branch != branch || (v.BatchID != selectedBatch && (report.Batch == nil || v.BatchID != report.Batch.ID)) {
				v.Preview, v.PreparationInvalid = nil, true
				return
			}
			if s.currentProductionScope() != scope {
				v.Preview, v.Error = nil, "相关配置已变化，请重新检查生产发布目标。"
				v.Steps, v.PreparationInvalid = nil, true
				return
			}
			baselineFailed := err != nil && (errors.Is(err, production.ErrProductionBaselineUnavailable) || action.Kind == "production.inspect" ||
				action.Kind == "production.prepare-tag" && (report.Preview == nil || report.Preview.ProductionSHA == ""))
			if baselineFailed {
				v.invalidateProductionBaseline(report.Preview)
			}
			if err != nil {
				v.Error = err.Error()
				if action.Kind == "production.open-ide" {
					s.recordDevelopmentToolUnavailable(err)
				}
				if action.Kind == "production.publish" {
					v.PreparationInvalid = true
				}
				if v.ActiveStep != 0 && v.Steps != nil {
					v.Steps[v.ActiveStep] = productionStepView{State: "failed", Message: v.Error}
				}
			} else {
				v.Message = result.Message
				if report.Batch != nil && report.Batch.Intent == nil {
					applyProductionWritePreview(v, *report.Batch, completedAction)
				}
				if action.Kind == "production.abort-merge" && report.Batch != nil && report.Batch.Intent == nil {
					delete(v.Steps, v.ActiveStep)
					delete(v.Steps, 5)
					delete(v.Steps, 6)
					s.lastMessage = result.Message
				}
				if step := v.ActiveStep; step != 0 && v.Steps[step].State == "running" {
					// Final results supply the durable status; a busy observation
					// alone must never leave a step running or imply success.
					delete(v.Steps, step)
					if report.Batch != nil && report.Batch.Intent == nil && productionBranchWrite(action.Kind) {
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
			if !baselineFailed && report.Preview != nil && action.Kind != "production.feature-preview" && action.Kind != "production.pending-preview" {
				if err == nil || action.Kind == "production.prepare-tag" {
					v.invalidateProductionBranchResult(report.Preview)
					preview := *report.Preview
					v.Preview = &preview
					v.updateSuggestedBranch(report.Preview.SuggestedVersion)
				}
				if b := s.productionBatch(v); b != nil && b.TagPlan != nil && !productionTagPlanCurrent(v, b) {
					v.PreparationInvalid = true
					delete(v.Steps, 5)
					delete(v.Steps, 6)
				}
			}
			if err == nil && action.Kind == "production.feature-preview" && follow == "feature-preview" && report.Batch != nil && report.Preview != nil && productionFeatureIncluded(*report.Batch, *report.Preview, branch) {
				v.Steps[4] = productionStepView{State: "skipped", Message: "已包含，无需合并", Identity: &productionResultIdentity{Selection: selection, BatchID: v.BatchID, Branch: branch, SourceSHA: report.Preview.BranchHeads[branch], TargetSHA: report.Preview.ReleaseSHA, Generation: generation, Home: home, ConfigScope: scope}}
			}
			if report.Batch != nil {
				b := report.Batch
				if b.TagPlan != nil && v.BatchID == b.ID && err == nil &&
					(action.Kind == "production.prepare-tag" || action.Kind == "production.sync" || action.Kind == "production.resume" || action.Kind == "production.rebase-pending") &&
					(follow != "build-publish" || worker.buildContinuation) {
					v.PreparationInvalid = false
				}
				if err == nil {
					s.scheduleProductionProbe(v, *b, window)
				}
			}
			if err == nil && (follow == "sync-preview" || follow == "check-preview" || follow == "build-preview" && worker.buildContinuation) && report.Preview != nil {
				next := ""
				if follow == "build-preview" {
					next = "build-publish"
				}
				s.productionRequestFor(v, application.Action{Kind: "production.sync", Payload: map[string]any{"batchId": v.BatchID, "preview": *report.Preview}}, next)
			}
			if err == nil && action.Kind == "production.list" {
				state.detachEndedProductionBatches()
			}
			visible := s.page == "production" && s.productionView() == v
			if err == nil && visible {
				switch follow {
				case "build-publish":
					if worker.buildContinuation && report.Batch != nil && report.Batch.Intent == nil && productionTagPlanCurrent(v, report.Batch) {
						s.publishProductionTag(v, *report.Batch)
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
			window.Invalidate()
		})
	}()
}

func (s *app) productionRequestFor(v *productionView, action application.Action, follow string) {
	s.productionRequestView(v, action, follow)
}

func (s *app) scheduleProductionProbe(v *productionView, b production.Batch, window productionWindow) {
	if !s.productionAvailable() || b.Ended || len(b.Tags) == 0 {
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
	optionalCtx := s.integrationContext("genbu")
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
		if optionalCtx.Err() != nil || window.IsDestroyed() {
			return
		}
		window.Update(func() {
			state.mu.Lock()
			closed := state.closed
			state.mu.Unlock()
			if closed || optionalCtx.Err() != nil || !s.productionAvailable() || s.snapshot.Home != home || s.snapshot.Home != state.loadedHome || s.currentProductionScope() != scope || v.BatchID != b.ID || v.Selection != b.Target.Selection {
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

func (s *app) suspendProductionUI() {
	state := s.releaseUnit.state.production
	if state == nil {
		return
	}
	s.closeProductionDiff()
	state.worktreesDialog, state.historyDialog = nil, nil
	state.mu.Lock()
	for id, timer := range state.timers {
		timer.Stop()
		delete(state.timers, id)
	}
	state.mu.Unlock()
	for _, view := range state.views {
		view.Preview, view.Steps = nil, nil
		view.PreparationInvalid, view.BuildChecked = true, false
		if worker := view.request; worker != nil {
			worker.buildContinuation = false
			if worker.cancelOnDisable {
				worker.cancel()
			}
		}
	}
}

func (s *releaseController) stopProductionJobs() {
	state := s.state.production
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

func (s *releaseController) closeProductionJobs(ctx context.Context) error {
	s.stopProductionJobs()
	if s.state.production == nil {
		return nil
	}
	state := s.state.production
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
