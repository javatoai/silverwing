package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"

	"github.com/egoist/mygo/ui"
)

type requirementSprint struct {
	ProjectKey  string `json:"projectKey"`
	ProjectName string `json:"projectName"`
	ID          string `json:"id"`
	Title       string `json:"title"`
	Status      string `json:"status"`
}

func (s requirementSprint) key() string { return s.ProjectKey + ":" + s.ID }

type requirementMetadataState string

const (
	requirementMetadataNotLoaded requirementMetadataState = "not-loaded"
	requirementMetadataLoading   requirementMetadataState = "loading"
	requirementMetadataLoaded    requirementMetadataState = "loaded"
	requirementMetadataFailed    requirementMetadataState = "failed"
)

type requirementMetadata struct {
	ID           string                         `json:"id"`
	ProjectKey   string                         `json:"projectKey"`
	Type         string                         `json:"type"`
	Title        *string                        `json:"title"`
	Status       *string                        `json:"status"`
	Participants domain.RequirementParticipants `json:"participants"`
	Stale        bool                           `json:"stale"`
	RefreshError string                         `json:"refreshError,omitempty"`
	CacheError   string                         `json:"cacheError,omitempty"`
	WorkItem     *domain.ParticipatedWorkItem   `json:"workItem,omitempty"`
}

type taskRequirementMetadataState struct {
	URL      string
	Status   requirementMetadataState
	Metadata requirementMetadata
	Error    string
}

func requirementTypeGroupLabel(item application.Requirement) string {
	switch strings.ToLower(item.Type) {
	case "userstory":
		return "User Story"
	case "technical":
		return "Tech Improvement"
	case "othertask":
		return "Task"
	case "bug":
		return "Bug"
	default:
		return firstNonEmpty(item.TypeLabel, item.Type, "其他")
	}
}
func (s *app) requestRequirements(sprintKey string, refresh bool) {
	if len(s.readConfigArray("meegleProjects")) == 0 {
		return
	}
	if sprintKey == "" {
		sprintKey = s.requirementUnit.state.requirementsSelectedSprintKey
	}
	// Re-entering the page must not replace a manual refresh's pending identity
	// intent or restart its already-running list query.
	if p := s.requirementUnit.state.requirementListProgress; s.requirementListProgressCurrent(p) && p.running && s.requirementUnit.state.requirementsLoading && p.sprintKey == sprintKey {
		return
	}
	if s.queueRequirementListIdentity(sprintKey, refresh) {
		s.activateRequirementSprint(sprintKey)
		return
	}
	s.activateRequirementSprint(sprintKey)
	s.startRequirementListWithKnownIdentity(sprintKey, refresh)
}

func (s *app) startRequirementListWithKnownIdentity(sprintKey string, refresh bool) {
	state := s.ensureRequirementListJobs()
	identity := s.currentRequirementListIdentity()
	if !state.hasIdentity {
		state.identity, state.hasIdentity = identity, true
	} else if state.identity != identity {
		s.resetRequirementListForIdentity(state, identity)
		sprintKey = ""
	}
	s.startRequirementListJob(sprintKey, refresh)
}

func (s *app) applyRequirementsResult(result application.ActionResult) {
	previousKey := s.requirementUnit.state.selectedRequirementKey
	data := mapData(result.Data)
	items, err := decodeData[application.Requirement](data["items"])
	if err != nil {
		s.requirementUnit.state.requirementsLoading = false
		s.requirementUnit.state.requirementsLoadError = "读取需求列表格式失败：" + err.Error()
		return
	}
	s.requirementUnit.state.requirements = items
	s.requirementUnit.state.requirementsFetchedAt = dataInt64(data, "fetchedAt")
	s.requirementUnit.state.requirementsLoaded = true
	s.requirementUnit.state.requirementsLoading = false
	s.requirementUnit.state.requirementsLoadError = ""
	s.requirementUnit.state.requirementsFailures = mapsString(data["failures"])
	s.requirementUnit.state.requirementsCompletedQueries = int(dataInt64(data, "completedQueries"))
	s.requirementUnit.state.requirementsSprints, err = decodeData[requirementSprint](data["sprints"])
	if err != nil {
		s.requirementUnit.state.requirementsSprints = nil
		s.requirementUnit.state.requirementsLoadError = "读取 Sprint 列表格式失败：" + err.Error()
	}
	s.requirementUnit.state.requirementsSelectedSprintKey = dataString(data, "selectedSprintKey")
	s.overlayRefreshedRequirements(s.requirementUnit.state.requirements, s.requirementUnit.state.requirementsSelectedSprintKey, s.requirementUnit.state.requirementsFetchedAt)
	if selected, ok := s.selectedRequirementByKey(s.requirementUnit.state.selectedRequirementKey); ok {
		s.requirementUnit.state.selectedRequirement = selected
	} else if len(s.requirementUnit.state.requirements) > 0 {
		s.requirementUnit.state.selectedRequirement = 0
		s.requirementUnit.state.selectedRequirementKey = requirementKey(s.requirementUnit.state.requirements[0])
	} else {
		s.requirementUnit.state.selectedRequirement = 0
		s.requirementUnit.state.selectedRequirementKey = ""
	}
	if previousKey != s.requirementUnit.state.selectedRequirementKey {
		if s.page == "requirements" {
			s.dismissMarkdownBitmapViewer()
		}
		s.lastResult = ""
	}
}

func requirementKey(item application.Requirement) string {
	if item.WorkItem.ProjectKey != "" || item.WorkItem.Type != "" || item.WorkItem.ID != "" {
		return item.WorkItem.Key()
	}
	return strings.Join([]string{item.ProjectKey, item.Type, item.ID, item.URL}, ":")
}

func (s *app) selectedRequirementByKey(key string) (int, bool) {
	if key == "" {
		return 0, false
	}
	for index, item := range s.requirementUnit.state.requirements {
		if requirementKey(item) == key {
			return index, true
		}
	}
	return 0, false
}

func (s *app) ensureTaskRequirementMetadata() {
	if s.loading || len(s.taskUnit.state.tasks) == 0 {
		return
	}
	key := taskMetadataDiscoveryKey{tasks: s.taskUnit.state.tasksRevision, metadata: s.taskUnit.state.metadataRevision, config: s.settingsUnit.state.configJSON}
	if s.taskUnit.state.metadataDiscoveryValid && s.taskUnit.state.metadataDiscovery == key {
		// A force-refresh can temporarily own a missing metadata read. Poll
		// only those deferred identities, rather than rescan the task catalog.
		for id, item := range s.taskUnit.state.metadataDiscoveryRetry {
			if !s.requirementRefreshing(item.requirementURL) {
				delete(s.taskUnit.state.metadataDiscoveryRetry, id)
				s.requestTaskRequirementMetadata(item, false)
			}
		}
		return
	}
	s.taskUnit.state.metadataDiscoveryRetry = make(map[string]task)
	defer func() {
		s.taskUnit.state.metadataDiscovery = taskMetadataDiscoveryKey{tasks: s.taskUnit.state.tasksRevision, metadata: s.taskUnit.state.metadataRevision, config: s.settingsUnit.state.configJSON}
		s.taskUnit.state.metadataDiscoveryValid = true
	}()
	known := make(map[string]string, len(s.taskUnit.state.tasks))
	for _, item := range s.taskUnit.state.tasks {
		if domain.ParseFeishuWorkItemLink(item.requirementURL) == nil {
			if cancel := s.requirementUnit.state.taskRequirementMetadataCancels[item.id]; cancel != nil {
				cancel()
				delete(s.requirementUnit.state.taskRequirementMetadataCancels, item.id)
				if s.requirementUnit.state.taskRequirementMetadataGeneration != nil {
					s.requirementUnit.state.taskRequirementMetadataGeneration[item.id]++
				}
			}
			s.deleteTaskRequirementMetadata(item.id)
			continue
		}
		known[item.id] = item.requirementURL
		state, exists := s.requirementUnit.state.taskRequirementMetadata[item.id]
		if !exists || state.URL != item.requirementURL {
			s.requestTaskRequirementMetadata(item, false)
			if s.requirementRefreshing(item.requirementURL) {
				s.taskUnit.state.metadataDiscoveryRetry[item.id] = item
			}
		}
	}
	for taskID, state := range s.requirementUnit.state.taskRequirementMetadata {
		if _, exists := known[taskID]; !exists {
			if cancel := s.requirementUnit.state.taskRequirementMetadataCancels[taskID]; cancel != nil {
				cancel()
				delete(s.requirementUnit.state.taskRequirementMetadataCancels, taskID)
			}
			s.deleteTaskRequirementMetadata(taskID)
			continue
		}
		if state.URL != known[taskID] {
			if cancel := s.requirementUnit.state.taskRequirementMetadataCancels[taskID]; cancel != nil {
				cancel()
				delete(s.requirementUnit.state.taskRequirementMetadataCancels, taskID)
			}
			s.deleteTaskRequirementMetadata(taskID)
		}
	}
}

func (s *app) requestTaskRequirementMetadata(item task, force bool) {
	url := item.requirementURL
	if s.requirementRefreshing(url) {
		return
	}
	if item.id == "" {
		return
	}
	if domain.ParseFeishuWorkItemLink(url) == nil {
		cancel := s.requirementUnit.state.taskRequirementMetadataCancels[item.id]
		if cancel != nil {
			cancel()
			delete(s.requirementUnit.state.taskRequirementMetadataCancels, item.id)
		}
		previous, exists := s.requirementUnit.state.taskRequirementMetadata[item.id]
		changed := !exists || previous.URL != url || previous.Status != requirementMetadataNotLoaded
		if s.requirementUnit.state.taskRequirementMetadataGeneration != nil && (cancel != nil || changed) {
			s.requirementUnit.state.taskRequirementMetadataGeneration[item.id]++
		}
		if s.requirementUnit.state.taskRequirementMetadata == nil {
			s.requirementUnit.state.taskRequirementMetadata = make(map[string]taskRequirementMetadataState)
		}
		s.setTaskRequirementMetadata(item.id, taskRequirementMetadataState{URL: url, Status: requirementMetadataNotLoaded})
		if (cancel != nil || changed) && s.win != nil {
			s.win.Invalidate()
		}
		return
	}
	if s.backend == nil || s.win == nil {
		return
	}
	if s.requirementUnit.state.taskRequirementMetadata == nil {
		s.requirementUnit.state.taskRequirementMetadata = make(map[string]taskRequirementMetadataState)
	}
	if s.requirementUnit.state.taskRequirementMetadataCancels == nil {
		s.requirementUnit.state.taskRequirementMetadataCancels = make(map[string]context.CancelFunc)
	}
	if s.requirementUnit.state.taskRequirementMetadataGeneration == nil {
		s.requirementUnit.state.taskRequirementMetadataGeneration = make(map[string]uint64)
	}
	previous, exists := s.requirementUnit.state.taskRequirementMetadata[item.id]
	if !force && exists && previous.URL == url && (previous.Status == requirementMetadataLoading || previous.Status == requirementMetadataLoaded) {
		return
	}
	if cancel := s.requirementUnit.state.taskRequirementMetadataCancels[item.id]; cancel != nil {
		cancel()
	}
	generation := s.requirementUnit.state.taskRequirementMetadataGeneration[item.id] + 1
	s.requirementUnit.state.taskRequirementMetadataGeneration[item.id] = generation
	ctx, cancel := context.WithCancel(context.Background())
	s.requirementUnit.state.taskRequirementMetadataCancels[item.id] = cancel
	retained := requirementMetadataForRefresh(previous, exists && previous.URL == url)
	retained.URL, retained.Status, retained.Error = url, requirementMetadataLoading, ""
	s.setTaskRequirementMetadata(item.id, retained)
	s.win.Invalidate()
	payload := s.bindRequirementAccount(map[string]any{"force": force})

	go func() {
		var result application.ActionResult
		err := s.acquireBackend(ctx)
		if err == nil {
			result, err = s.backend.Dispatch(ctx, application.Action{Kind: "requirements.metadata", TaskID: item.id, Path: url,
				Payload: payload})
			s.releaseBackend()
		}
		if s.win == nil || s.win.IsDestroyed() {
			return
		}
		s.win.Update(func() {
			if s.requirementUnit.state.taskRequirementMetadataGeneration[item.id] != generation {
				return
			}
			delete(s.requirementUnit.state.taskRequirementMetadataCancels, item.id)
			if !s.hasTaskRequirementURL(item.id, url) {
				s.deleteTaskRequirementMetadata(item.id)
				return
			}
			if ctx.Err() != nil {
				if requirementMetadataHasValues(retained.Metadata) {
					retained.URL, retained.Status = url, requirementMetadataLoaded
					s.setTaskRequirementMetadata(item.id, retained)
				} else {
					s.setTaskRequirementMetadata(item.id, taskRequirementMetadataState{URL: url, Status: requirementMetadataNotLoaded})
				}
				return
			}
			if err != nil {
				if requirementMetadataHasValues(retained.Metadata) {
					retained.URL, retained.Status, retained.Error = url, requirementMetadataLoaded, err.Error()
					s.setTaskRequirementMetadata(item.id, retained)
				} else {
					s.setTaskRequirementMetadata(item.id, taskRequirementMetadataState{URL: url, Status: requirementMetadataFailed, Error: err.Error()})
				}
				return
			}
			var metadata requirementMetadata
			encoded, marshalErr := json.Marshal(result.Data)
			if marshalErr == nil {
				marshalErr = json.Unmarshal(encoded, &metadata)
			}
			if marshalErr != nil {
				failure := "需求元数据格式无效：" + marshalErr.Error()
				if requirementMetadataHasValues(retained.Metadata) {
					retained.URL, retained.Status, retained.Error = url, requirementMetadataLoaded, failure
					s.setTaskRequirementMetadata(item.id, retained)
				} else {
					s.setTaskRequirementMetadata(item.id, taskRequirementMetadataState{URL: url, Status: requirementMetadataFailed, Error: failure})
				}
				return
			}
			s.setTaskRequirementMetadata(item.id, taskRequirementMetadataState{URL: url, Status: requirementMetadataLoaded, Metadata: metadata, Error: metadata.RefreshError})
		})
	}()
}

func requirementMetadataForRefresh(previous taskRequirementMetadataState, sameURL bool) taskRequirementMetadataState {
	if !sameURL {
		return taskRequirementMetadataState{}
	}
	return previous
}

func requirementMetadataHasValues(metadata requirementMetadata) bool {
	if metadata.Title != nil && strings.TrimSpace(*metadata.Title) != "" || metadata.Status != nil && strings.TrimSpace(*metadata.Status) != "" {
		return true
	}
	return len(metadata.Participants.QCOwners) > 0 || len(metadata.Participants.ProductManagers) > 0
}

func (s *app) hasTaskRequirementURL(taskID, url string) bool {
	for _, item := range s.taskUnit.state.tasks {
		if item.id == taskID {
			return strings.TrimSpace(item.requirementURL) == strings.TrimSpace(url)
		}
	}
	return false
}

func (s *app) cancelTaskRequirementMetadata(taskID string) {
	if cancel := s.requirementUnit.state.taskRequirementMetadataCancels[taskID]; cancel != nil {
		cancel()
		delete(s.requirementUnit.state.taskRequirementMetadataCancels, taskID)
	}
	s.requirementUnit.state.taskRequirementMetadataGeneration[taskID]++
	if state, ok := s.requirementUnit.state.taskRequirementMetadata[taskID]; ok {
		s.setTaskRequirementMetadata(taskID, taskRequirementMetadataState{URL: state.URL, Status: requirementMetadataNotLoaded})
	}
	if s.win != nil {
		s.win.Invalidate()
	}
}

func (s *requirementController) cancelTaskRequirementReads() {
	for taskID, cancel := range s.state.taskRequirementMetadataCancels {
		cancel()
		delete(s.state.taskRequirementMetadataCancels, taskID)
	}
}

func (s *app) taskRequirementMetadataFor(item task) taskRequirementMetadataState {
	state, ok := s.requirementUnit.state.taskRequirementMetadata[item.id]
	if !ok || state.URL != item.requirementURL {
		return taskRequirementMetadataState{URL: item.requirementURL, Status: requirementMetadataNotLoaded}
	}
	return state
}

func (s *app) renderTaskRequirementMetadata(c *ui.Context, p palette, item task) {
	if strings.TrimSpace(item.requirementURL) == "" {
		return
	}
	state := s.taskRequirementMetadataFor(item)
	hasMetadata := requirementMetadataHasValues(state.Metadata)
	title := ""
	copyTitle := ""
	color := p.muted
	if state.Metadata.Title != nil {
		copyTitle = *state.Metadata.Title
		title = strings.TrimSpace(copyTitle)
	}
	if title == "" && !hasMetadata {
		switch state.Status {
		case requirementMetadataLoading:
			title = "获取中"
		case requirementMetadataFailed:
			title, color = "未获取需求信息", p.amber
		}
	}
	if title != "" {
		tip := title
		canCopy := strings.TrimSpace(copyTitle) != ""
		if canCopy {
			tip = "点击复制需求名称\n" + tip
		}
		if state.Error != "" {
			tip += "\n刷新失败：" + state.Error
		}
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			button := ui.ButtonBase(c.Key("copy-task-requirement-title")).Grow(1).Shrink(1).MinWidth(0).Justify(ui.Start).
				Label("复制需求名称").Tooltip(tip).FocusRing(true).Disabled(!canCopy)
			button.Children(func() { label(c, title, 14, color).FontWeight(500).Grow(1).SingleLine() })
			if button.Clicked() && canCopy {
				s.copyTextToClipboard(c.Services(), copyTitle, "已复制需求名称")
			}
		})
	}
}

func (s *app) renderTaskRequirementParticipants(c *ui.Context, p palette, item task) {
	if strings.TrimSpace(item.requirementURL) == "" {
		return
	}
	metadata := s.taskRequirementMetadataFor(item).Metadata
	participants := metadata.Participants
	var developers []domain.RequirementPerson
	if metadata.WorkItem != nil {
		developers = metadata.WorkItem.Developers
		participants.QCOwners, participants.ProductManagers = metadata.WorkItem.QCOwners, metadata.WorkItem.ProductManagers
	}
	values := make([]string, 0, 3)
	for _, role := range []struct {
		label  string
		people []domain.RequirementPerson
	}{
		{"研发", developers},
		{"测试", participants.QCOwners},
		{"产品", participants.ProductManagers},
	} {
		if summary := requirementPeopleSummary(role.label, role.people); summary != "" {
			values = append(values, summary)
		}
	}
	if len(values) == 0 {
		return
	}
	ui.Row(c).FillWidth().MinWidth(0).Gap(6).Wrap().Children(func() {
		for _, value := range values {
			ui.Box(c).Padding(3, 7).Radius(6).Background(p.soft).Children(func() { label(c, value, 11, p.muted).NoWrap() })
		}
	})
}

func requirementPeopleSummary(role string, people []domain.RequirementPerson) string {
	names := make([]string, 0, len(people))
	for _, person := range people {
		name := strings.TrimSpace(person.Name)
		if name == "" && person.Email != nil {
			name = strings.TrimSpace(*person.Email)
		}
		if name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	if len(names) > 2 {
		return fmt.Sprintf("%s %d 人", role, len(names))
	}
	return role + "：" + strings.Join(names, "、")
}

func (s *app) renderRequirementMetadataSummary(c *ui.Context, p palette, item application.Requirement) {
	roles := []struct {
		label  string
		people []domain.RequirementPerson
	}{
		{"研发", item.WorkItem.Developers},
		{"测试", item.WorkItem.QCOwners},
		{"产品", item.WorkItem.ProductManagers},
	}
	values := make([]string, 0, len(roles))
	for _, role := range roles {
		value := requirementPeopleSummary(role.label, role.people)
		if value != "" {
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		return
	}
	label(c, strings.Join(values, " · "), 12, p.muted).Wrap()
}

func (s *app) replicaRequirements(c *ui.Context, p palette) {
	s.observeRequirementListIdentity()
	if !s.loading && !s.requirementUnit.state.requirementsRequested && len(s.readConfigArray("meegleProjects")) > 0 {
		s.requestRequirements("", false)
	}
	width, _ := c.Size()
	focused := width < 800
	if s.loading {
		s.renderRequirementLoadingShell(c, p)
		return
	}
	if focused && s.requirementUnit.state.requirementsFocusedDetail && len(s.requirementUnit.state.requirements) > 0 {
		selected := s.requirementUnit.state.selectedRequirement
		if selected < 0 || selected >= len(s.requirementUnit.state.requirements) {
			selected = 0
		}
		ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(8).Gap(8).Background(p.bg).Children(func() {
			if iconButton(c, "requirements-back-to-list", "chevron-left", "返回需求列表", p).Clicked() {
				s.requirementUnit.state.requirementsFocusedDetail = false
			}
			s.requirementDetail(c, p, s.requirementUnit.state.requirements[selected])
		})
		return
	}
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(8).Background(p.bg).Children(func() {
		if len(s.readConfigArray("meegleProjects")) == 0 {
			ui.Column(c).Grow(1).Center().Gap(10).Padding(24).Background(p.surface).Border(1, p.line).Radius(16).Children(func() {
				icon(c, "requirements", 24, p.muted)
				label(c, "尚未配置 Meegle 项目", 15, p.text).FontWeight(600)
				label(c, "先在 Meegle CLI 设置中选择要展示的项目。", 12, p.muted)
				if action(c, "requirements-open-meegle-settings", "settings", "前往 Meegle CLI 设置", p, false) {
					s.openMeegleSettings()
				}
			})
			return
		}
		if focused {
			s.requirementList(c, p, true)
			return
		}
		ui.Split(c.Key("requirements-list-detail-split"), &s.requirementUnit.state.requirementWidth, func() {
			s.requirementList(c, p, false)
		}, func() {
			ui.Box(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
				if len(s.requirementUnit.state.requirements) == 0 {
					if s.requirementUnit.state.requirementsLoading {
						s.renderRequirementDetailSkeleton(c, p)
					} else if !s.requirementUnit.state.requirementsLoaded {
						ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Center().Background(p.surface).Border(1, p.line).Radius(14).Children(func() {
							label(c, "正在读取所选 Sprint…", 13, p.muted)
						})
					} else {
						ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Center().Background(p.surface).Border(1, p.line).Radius(14).Children(func() {
							if len(s.requirementUnit.state.requirementsSprints) == 0 && len(s.requirementUnit.state.requirementsFailures) == 0 {
								label(c, "没有进行中或未开始的可选 Sprint", 13, p.muted)
							} else {
								label(c, "该 Sprint 中没有我参与的工作项", 13, p.muted)
							}
						})
					}
				} else {
					selected := s.requirementUnit.state.selectedRequirement
					if selected < 0 || selected >= len(s.requirementUnit.state.requirements) {
						selected = 0
					}
					s.requirementDetail(c, p, s.requirementUnit.state.requirements[selected])
				}
			})
		}).Fill().MinWidth(0).MinHeight(0)
	})
}

func (s *app) renderRequirementLoadingShell(c *ui.Context, p palette) {
	width, _ := c.Size()
	focused := width < 800
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(8).Background(p.bg).Children(func() {
		if focused {
			if s.requirementUnit.state.requirementsFocusedDetail {
				ui.Row(c).Height(34).Children(func() {
					if iconButton(c, "requirements-back-to-list", "chevron-left", "返回需求列表", p).Clicked() {
						s.requirementUnit.state.requirementsFocusedDetail = false
					}
				})
				s.renderRequirementDetailSkeleton(c, p)
			} else {
				ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(10).Gap(10).Background(p.surface).Border(1, p.line).Radius(14).Children(func() {
					ui.Row(c).Height(34).Gap(8).Children(func() {
						ui.Box(c).Grow(1).Height(34).Radius(9).Background(p.soft)
						ui.Box(c).Width(34).Height(34).Radius(9).Background(p.soft)
					})
					s.renderRequirementListSkeleton(c, p, 6)
				})
			}
			return
		}
		ui.Split(c.Key("requirements-loading-split"), &s.requirementUnit.state.requirementWidth, func() {
			ui.Column(c).FillWidth().FillHeight().MinWidth(0).MinHeight(0).Padding(10).Gap(10).Background(p.surface).Border(1, p.line).Radius(14).Children(func() {
				ui.Row(c).Height(34).Gap(8).Children(func() {
					ui.Box(c).Grow(1).Height(34).Radius(9).Background(p.soft)
					ui.Box(c).Width(34).Height(34).Radius(9).Background(p.soft)
				})
				s.renderRequirementListSkeleton(c, p, 5)
			})
		}, func() { s.renderRequirementDetailSkeleton(c, p) }).Fill().MinWidth(0).MinHeight(0)
	})
}

func (s *app) renderRequirementListSkeleton(c *ui.Context, p palette, count int) {
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Gap(9).Children(func() {
		for index := 0; index < count; index++ {
			lineWidth := float32(130 + index%3*34)
			ui.Column(c).FillWidth().Padding(12).Gap(9).Background(p.surface).Border(1, p.line).Radius(11).Children(func() {
				ui.Box(c).Width(lineWidth).Height(12).Radius(6).Background(p.soft)
				ui.Box(c).FillWidth().Height(9).Radius(5).Background(p.soft)
				ui.Box(c).Width(108).Height(9).Radius(5).Background(p.soft)
			})
		}
	})
}

func (s *app) renderRequirementDetailSkeleton(c *ui.Context, p palette) {
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(18).Gap(14).Background(p.surface).Border(1, p.line).Radius(14).Children(func() {
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Width(260).Height(20).Radius(7).Background(p.soft)
			ui.Spacer(c)
			ui.Box(c).Width(72).Height(24).Radius(12).Background(p.soft)
		})
		ui.Box(c).Width(360).Height(10).Radius(5).Background(p.soft)
		ui.Box(c).FillWidth().Height(1).Background(p.line)
		ui.Box(c).Width(220).Height(12).Radius(6).Background(p.soft)
		ui.Box(c).FillWidth().Height(10).Radius(5).Background(p.soft)
		ui.Box(c).Width(92).Height(10).Radius(5).Background(p.soft)
		ui.Box(c).FillWidth().Grow(1).MinHeight(130).Radius(10).Background(p.soft)
	})
}

func (s *app) openMeegleSettings() {
	s.page, s.settingsUnit.state.settingsTab, s.settingsUnit.state.settingsPage, s.settingsUnit.state.settingsLoaded = "settings", "CLI", "cli-meegle", ""
}

func (s *app) selectedRequirementSprint() requirementSprint {
	for _, sprint := range s.requirementUnit.state.requirementsSprints {
		if sprint.key() == s.requirementUnit.state.requirementsSelectedSprintKey {
			return sprint
		}
	}
	if len(s.requirementUnit.state.requirementsSprints) > 0 {
		return s.requirementUnit.state.requirementsSprints[0]
	}
	return requirementSprint{}
}

func (s *app) requirementSprintLabel(sprint requirementSprint) string {
	if sprint.ID == "" {
		if s.requirementUnit.state.requirementsLoading {
			return "正在读取 Sprint…"
		}
		return "选择 Sprint"
	}
	return firstNonEmpty(sprint.Title, sprint.ID) + " · " + firstNonEmpty(sprint.Status, sprint.ProjectName)
}

func (s *app) requirementFailurePanel(c *ui.Context, p palette, failures []string, requestError bool) {
	ui.Column(c).FillWidth().Padding(10, 12).Gap(6).Background(p.amberBg).Border(1, p.amber).Radius(10).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			icon(c, "warning", 16, p.amber)
			label(c, "部分需求查询未完成", 12, p.amber).FontWeight(600).Grow(1)
			if s.requirementUnit.state.requirementsCompletedQueries > 0 {
				label(c, fmt.Sprintf("已完成 %d 项查询", s.requirementUnit.state.requirementsCompletedQueries), 11, p.muted)
			}
			if !s.requirementUnit.state.requirementsLoading && iconButton(c, "requirements-retry-failures", "refresh", "重试需求查询", p).Clicked() {
				s.refreshRequirementListWithProgress(s.requirementUnit.state.requirementsSelectedSprintKey)
			}
		})
		for _, failure := range failures {
			if strings.TrimSpace(failure) != "" {
				label(c, failure, 12, p.text).Wrap()
			}
		}
		if action(c, "requirements-open-meegle-settings", "settings", "Meegle 设置", p, false) {
			s.openMeegleSettings()
		}
		if requestError && s.requirementUnit.state.requirementsLoadError == "" {
			label(c, "查询失败；已有内容已保留。", 11, p.muted)
		}
	})
}

func (s *app) requirementList(c *ui.Context, p palette, focused bool) {
	list := ui.Column(c).Grow(1).FillWidth().MinWidth(0).MinHeight(0).Gap(8).Padding(10).Background(p.surface).Border(1, p.line).Radius(14)
	list.Children(func() {
		ui.Row(c).Height(36).Gap(6).AlignItems(ui.Center).Children(func() {
			button := ui.ButtonBase(c.Key("requirements-sprint-picker")).Row().Grow(1).MinWidth(0).Height(34).Padding(0, 9).Gap(6).Radius(9).Border(1, p.line).Background(p.surface).Label("选择 Sprint")
			button.Children(func() {
				icon(c, "calendar", 15, p.blue)
				label(c, s.requirementSprintLabel(s.selectedRequirementSprint()), 11, p.text).Grow(1).SingleLine().Tooltip(s.requirementSprintLabel(s.selectedRequirementSprint()))
				icon(c, "chevron-down", 13, p.muted)
			})
			button.Disabled(len(s.requirementUnit.state.requirementsSprints) == 0)
			button.Menu(func(menu *ui.Menu) {
				for _, option := range s.requirementUnit.state.requirementsSprints {
					text := firstNonEmpty(option.Title, option.ID) + " · " + firstNonEmpty(option.ProjectName, option.ProjectKey) + " · " + option.Status
					if menu.Item(text).Checked(option.key() == s.requirementUnit.state.requirementsSelectedSprintKey).Chosen() {
						s.requestRequirements(option.key(), false)
					}
				}
			})
			refreshLabel := "刷新当前 Sprint"
			progress := s.requirementUnit.state.requirementListProgress
			viewProgress := s.requirementListProgressCurrent(progress) && progress.running
			if viewProgress {
				refreshLabel = "查看刷新进度"
			}
			if iconButtonDisabled(c, "refresh-requirements", "refresh", refreshLabel, p, (s.requirementUnit.state.requirementsLoading && !viewProgress) || s.requirementUnit.state.requirementsSelectedSprintKey == "").Clicked() {
				s.refreshRequirementListWithProgress(s.requirementUnit.state.requirementsSelectedSprintKey)
			}
		})
		if s.requirementUnit.state.requirementsLoadError != "" {
			s.requirementFailurePanel(c, p, []string{s.requirementUnit.state.requirementsLoadError}, true)
		} else if len(s.requirementUnit.state.requirementsFailures) > 0 {
			s.requirementFailurePanel(c, p, s.requirementUnit.state.requirementsFailures, false)
		}
		if s.requirementUnit.state.requirementsSelectedSprintKey == "" && len(s.requirementUnit.state.requirementsSprints) > 0 {
			ui.Column(c).Grow(1).Center().Gap(8).Children(func() {
				label(c, "请选择要查看的 Sprint", 12, p.muted)
			})
			return
		}
		if len(s.requirementUnit.state.requirements) == 0 {
			if s.requirementUnit.state.requirementsLoading {
				s.renderRequirementListSkeleton(c, p, 5)
			} else {
				ui.Column(c).Grow(1).Center().Gap(8).Children(func() {
					if len(s.requirementUnit.state.requirementsSprints) == 0 && s.requirementUnit.state.requirementsLoadError == "" {
						label(c, "没有进行中或未开始的可选 Sprint", 12, p.muted)
					} else {
						label(c, "该 Sprint 中没有我参与的工作项", 12, p.muted)
					}
				})
			}
			return
		}
		ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Padding(1).Gap(4).Children(func() {
			for _, item := range s.requirementUnit.state.requirements {
				s.requirementListItem(c, p, item, focused)
			}
		})
	})
}

func (s *app) requirementListItem(c *ui.Context, p palette, item application.Requirement, focused bool) {
	index := -1
	for i, candidate := range s.requirementUnit.state.requirements {
		if requirementKey(candidate) == requirementKey(item) {
			index = i
			break
		}
	}
	selected := index >= 0 && index == s.requirementUnit.state.selectedRequirement
	button := ui.ButtonBase(c.Key("requirement-"+requirementKey(item))).Column().FillWidth().Padding(8, 9).Gap(3).Radius(9).Border(1, p.line).Label(item.Title)
	scope := s.currentRequirementListIdentity()
	button.ContextMenu(func(menu *ui.Menu) {
		name := "刷新"
		running := s.requirementRefreshing(item.URL)
		if running {
			name = "刷新中…"
		}
		if menu.Item(name).Icon(icons["refresh"]).Disabled(running || s.backend == nil || scope != s.currentRequirementListIdentity()).Chosen() {
			s.startRequirementRefresh(item.URL)
		}
	})
	if selected {
		button.Background(p.selected).Border(1, p.blue)
	} else {
		button.Background(p.surface)
		if button.Hovered() {
			button.Background(p.soft)
		}
	}
	button.Children(func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			label(c, item.Title, 12, p.text).FontWeight(500).Grow(1).SingleLine().Tooltip(item.Title)
			compactState(c, item.Status, p)
		})
		estimate := ""
		if days := item.WorkItem.MySprintEstimateDays; days != nil && strings.TrimSpace(*days) != "" {
			estimate = "估时 " + strings.TrimSpace(*days) + " 天"
		}
		secondary := requirementTypeGroupLabel(item)
		if estimate != "" {
			secondary += " · " + estimate
		}
		label(c, secondary, 10, p.muted).FillWidth().SingleLine().Tooltip(secondary)
	})
	if button.Clicked() && index >= 0 {
		nextKey := requirementKey(item)
		if nextKey != s.requirementUnit.state.selectedRequirementKey {
			s.dismissMarkdownBitmapViewer()
		}
		s.requirementUnit.state.selectedRequirement = index
		s.requirementUnit.state.selectedRequirementKey = nextKey
		if state := s.ensureRequirementListJobs(); state.current != nil && state.current.sprintKey == s.requirementUnit.state.requirementsSelectedSprintKey {
			state.current.selectedKey = s.requirementUnit.state.selectedRequirementKey
		}
		s.prioritizeRequirementNamingPreparation(item)
		s.prioritizeRequirementDocumentPreparation(item)
		s.lastResult = ""
		if focused {
			s.requirementUnit.state.requirementsFocusedDetail = true
		}
	}
}

func (s *app) requirementDetail(c *ui.Context, p palette, item application.Requirement) {
	reader := s.requirementReader("", item.URL)
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Gap(10).Padding(16).Background(p.surface).Border(1, p.line).Radius(14).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
			label(c, item.Title, 14, p.text).FontWeight(400).Grow(1).Wrap()
			if primaryIconButton(c, "create-task-from-requirement", "plus", "基于需求创建任务", p).Clicked() {
				s.openTaskCreateForm("基于需求创建任务", item.Title, item.URL)
			}
			s.renderRequirementReaderActions(c, p, reader, func(menu *overflowMenuBuilder) {
				if menu.Item("refresh", s.requirementRefreshLabel(item.URL)).Icon("refresh").Disabled(s.requirementRefreshing(item.URL)).Chosen() {
					s.startRequirementRefresh(item.URL)
				}
			})
		})
		s.renderRequirementMetadataSummary(c, p, item)
		if item.URL != "" {
			ui.Row(c).Gap(7).Children(func() {
				s.renderRequirementURLLink(c, p, "", item.URL, 12)
				if iconButton(c, "copy-requirement-url", "copy", "复制需求链接", p).Clicked() {
					s.copyTextToClipboard(c.Services(), item.URL, "已复制需求链接")
				}
			})
		}
		s.renderRequirementContent(c, p, "", item.URL)
	})
}
