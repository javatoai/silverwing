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
		sprintKey = s.requirementsSelectedSprintKey
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
	previousKey := s.selectedRequirementKey
	data := mapData(result.Data)
	items, err := decodeData[application.Requirement](data["items"])
	if err != nil {
		s.requirementsLoading = false
		s.requirementsLoadError = "读取需求列表格式失败：" + err.Error()
		return
	}
	s.requirements = items
	s.requirementsFetchedAt = dataInt64(data, "fetchedAt")
	s.requirementsLoaded = true
	s.requirementsLoading = false
	s.requirementsLoadError = ""
	s.requirementsFailures = mapsString(data["failures"])
	s.requirementsCompletedQueries = int(dataInt64(data, "completedQueries"))
	s.requirementsSprints, err = decodeData[requirementSprint](data["sprints"])
	if err != nil {
		s.requirementsSprints = nil
		s.requirementsLoadError = "读取 Sprint 列表格式失败：" + err.Error()
	}
	s.requirementsSelectedSprintKey = dataString(data, "selectedSprintKey")
	s.overlayRefreshedRequirements(s.requirements, s.requirementsSelectedSprintKey, s.requirementsFetchedAt)
	if selected, ok := s.selectedRequirementByKey(s.selectedRequirementKey); ok {
		s.selectedRequirement = selected
	} else if len(s.requirements) > 0 {
		s.selectedRequirement = 0
		s.selectedRequirementKey = requirementKey(s.requirements[0])
	} else {
		s.selectedRequirement = 0
		s.selectedRequirementKey = ""
	}
	if previousKey != s.selectedRequirementKey {
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
	for index, item := range s.requirements {
		if requirementKey(item) == key {
			return index, true
		}
	}
	return 0, false
}

func (s *app) ensureTaskRequirementMetadata() {
	if s.loading || len(s.tasks) == 0 {
		return
	}
	known := make(map[string]string, len(s.tasks))
	for _, item := range s.tasks {
		if domain.ParseFeishuWorkItemLink(item.requirementURL) == nil {
			if cancel := s.taskRequirementMetadataCancels[item.id]; cancel != nil {
				cancel()
				delete(s.taskRequirementMetadataCancels, item.id)
				if s.taskRequirementMetadataGeneration != nil {
					s.taskRequirementMetadataGeneration[item.id]++
				}
			}
			delete(s.taskRequirementMetadata, item.id)
			continue
		}
		known[item.id] = item.requirementURL
		state, exists := s.taskRequirementMetadata[item.id]
		if !exists || state.URL != item.requirementURL {
			s.requestTaskRequirementMetadata(item, false)
		}
	}
	for taskID, state := range s.taskRequirementMetadata {
		if _, exists := known[taskID]; !exists {
			if cancel := s.taskRequirementMetadataCancels[taskID]; cancel != nil {
				cancel()
				delete(s.taskRequirementMetadataCancels, taskID)
			}
			delete(s.taskRequirementMetadata, taskID)
			continue
		}
		if state.URL != known[taskID] {
			if cancel := s.taskRequirementMetadataCancels[taskID]; cancel != nil {
				cancel()
				delete(s.taskRequirementMetadataCancels, taskID)
			}
			delete(s.taskRequirementMetadata, taskID)
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
		cancel := s.taskRequirementMetadataCancels[item.id]
		if cancel != nil {
			cancel()
			delete(s.taskRequirementMetadataCancels, item.id)
		}
		previous, exists := s.taskRequirementMetadata[item.id]
		changed := !exists || previous.URL != url || previous.Status != requirementMetadataNotLoaded
		if s.taskRequirementMetadataGeneration != nil && (cancel != nil || changed) {
			s.taskRequirementMetadataGeneration[item.id]++
		}
		if s.taskRequirementMetadata == nil {
			s.taskRequirementMetadata = make(map[string]taskRequirementMetadataState)
		}
		s.taskRequirementMetadata[item.id] = taskRequirementMetadataState{URL: url, Status: requirementMetadataNotLoaded}
		if (cancel != nil || changed) && s.win != nil {
			s.win.Invalidate()
		}
		return
	}
	if s.backend == nil || s.win == nil {
		return
	}
	if s.taskRequirementMetadata == nil {
		s.taskRequirementMetadata = make(map[string]taskRequirementMetadataState)
	}
	if s.taskRequirementMetadataCancels == nil {
		s.taskRequirementMetadataCancels = make(map[string]context.CancelFunc)
	}
	if s.taskRequirementMetadataGeneration == nil {
		s.taskRequirementMetadataGeneration = make(map[string]uint64)
	}
	previous, exists := s.taskRequirementMetadata[item.id]
	if !force && exists && previous.URL == url && (previous.Status == requirementMetadataLoading || previous.Status == requirementMetadataLoaded) {
		return
	}
	if cancel := s.taskRequirementMetadataCancels[item.id]; cancel != nil {
		cancel()
	}
	generation := s.taskRequirementMetadataGeneration[item.id] + 1
	s.taskRequirementMetadataGeneration[item.id] = generation
	ctx, cancel := context.WithCancel(context.Background())
	s.taskRequirementMetadataCancels[item.id] = cancel
	retained := requirementMetadataForRefresh(previous, exists && previous.URL == url)
	retained.URL, retained.Status, retained.Error = url, requirementMetadataLoading, ""
	s.taskRequirementMetadata[item.id] = retained
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
			if s.taskRequirementMetadataGeneration[item.id] != generation {
				return
			}
			delete(s.taskRequirementMetadataCancels, item.id)
			if !s.hasTaskRequirementURL(item.id, url) {
				delete(s.taskRequirementMetadata, item.id)
				return
			}
			if ctx.Err() != nil {
				if requirementMetadataHasValues(retained.Metadata) {
					retained.URL, retained.Status = url, requirementMetadataLoaded
					s.taskRequirementMetadata[item.id] = retained
				} else {
					s.taskRequirementMetadata[item.id] = taskRequirementMetadataState{URL: url, Status: requirementMetadataNotLoaded}
				}
				return
			}
			if err != nil {
				if requirementMetadataHasValues(retained.Metadata) {
					retained.URL, retained.Status, retained.Error = url, requirementMetadataLoaded, err.Error()
					s.taskRequirementMetadata[item.id] = retained
				} else {
					s.taskRequirementMetadata[item.id] = taskRequirementMetadataState{URL: url, Status: requirementMetadataFailed, Error: err.Error()}
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
					s.taskRequirementMetadata[item.id] = retained
				} else {
					s.taskRequirementMetadata[item.id] = taskRequirementMetadataState{URL: url, Status: requirementMetadataFailed, Error: failure}
				}
				return
			}
			s.taskRequirementMetadata[item.id] = taskRequirementMetadataState{URL: url, Status: requirementMetadataLoaded, Metadata: metadata, Error: metadata.RefreshError}
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
	for _, item := range s.tasks {
		if item.id == taskID {
			return strings.TrimSpace(item.requirementURL) == strings.TrimSpace(url)
		}
	}
	return false
}

func (s *app) cancelTaskRequirementMetadata(taskID string) {
	if cancel := s.taskRequirementMetadataCancels[taskID]; cancel != nil {
		cancel()
		delete(s.taskRequirementMetadataCancels, taskID)
	}
	s.taskRequirementMetadataGeneration[taskID]++
	if state, ok := s.taskRequirementMetadata[taskID]; ok {
		s.taskRequirementMetadata[taskID] = taskRequirementMetadataState{URL: state.URL, Status: requirementMetadataNotLoaded}
	}
	if s.win != nil {
		s.win.Invalidate()
	}
}

func (s *app) cancelTaskRequirementReads() {
	for taskID, cancel := range s.taskRequirementMetadataCancels {
		cancel()
		delete(s.taskRequirementMetadataCancels, taskID)
	}
}

func (s *app) taskRequirementMetadataFor(item task) taskRequirementMetadataState {
	state, ok := s.taskRequirementMetadata[item.id]
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
	if !s.loading && !s.requirementsRequested && len(s.readConfigArray("meegleProjects")) > 0 {
		s.requestRequirements("", false)
	}
	width, _ := c.Size()
	focused := width < 800
	if s.loading {
		s.renderRequirementLoadingShell(c, p)
		return
	}
	if focused && s.requirementsFocusedDetail && len(s.requirements) > 0 {
		selected := s.selectedRequirement
		if selected < 0 || selected >= len(s.requirements) {
			selected = 0
		}
		ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(8).Gap(8).Background(p.bg).Children(func() {
			if iconButton(c, "requirements-back-to-list", "chevron-left", "返回需求列表", p).Clicked() {
				s.requirementsFocusedDetail = false
			}
			s.requirementDetail(c, p, s.requirements[selected])
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
		ui.Split(c.Key("requirements-list-detail-split"), &s.requirementWidth, func() {
			s.requirementList(c, p, false)
		}, func() {
			ui.Box(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
				if len(s.requirements) == 0 {
					if s.requirementsLoading {
						s.renderRequirementDetailSkeleton(c, p)
					} else if !s.requirementsLoaded {
						ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Center().Background(p.surface).Border(1, p.line).Radius(14).Children(func() {
							label(c, "正在读取所选 Sprint…", 13, p.muted)
						})
					} else {
						ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Center().Background(p.surface).Border(1, p.line).Radius(14).Children(func() {
							if len(s.requirementsSprints) == 0 && len(s.requirementsFailures) == 0 {
								label(c, "没有进行中或未开始的可选 Sprint", 13, p.muted)
							} else {
								label(c, "该 Sprint 中没有我参与的工作项", 13, p.muted)
							}
						})
					}
				} else {
					selected := s.selectedRequirement
					if selected < 0 || selected >= len(s.requirements) {
						selected = 0
					}
					s.requirementDetail(c, p, s.requirements[selected])
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
			if s.requirementsFocusedDetail {
				ui.Row(c).Height(34).Children(func() {
					if iconButton(c, "requirements-back-to-list", "chevron-left", "返回需求列表", p).Clicked() {
						s.requirementsFocusedDetail = false
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
		ui.Split(c.Key("requirements-loading-split"), &s.requirementWidth, func() {
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
	s.page, s.settingsTab, s.settingsPage, s.settingsLoaded = "settings", "CLI", "cli-meegle", ""
}

func (s *app) selectedRequirementSprint() requirementSprint {
	for _, sprint := range s.requirementsSprints {
		if sprint.key() == s.requirementsSelectedSprintKey {
			return sprint
		}
	}
	if len(s.requirementsSprints) > 0 {
		return s.requirementsSprints[0]
	}
	return requirementSprint{}
}

func (s *app) requirementSprintLabel(sprint requirementSprint) string {
	if sprint.ID == "" {
		if s.requirementsLoading {
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
			if s.requirementsCompletedQueries > 0 {
				label(c, fmt.Sprintf("已完成 %d 项查询", s.requirementsCompletedQueries), 11, p.muted)
			}
			if !s.requirementsLoading && iconButton(c, "requirements-retry-failures", "refresh", "重试需求查询", p).Clicked() {
				s.requestRequirements(s.requirementsSelectedSprintKey, true)
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
		if requestError && s.requirementsLoadError == "" {
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
			button.Disabled(len(s.requirementsSprints) == 0)
			button.Menu(func(menu *ui.Menu) {
				for _, option := range s.requirementsSprints {
					text := firstNonEmpty(option.Title, option.ID) + " · " + firstNonEmpty(option.ProjectName, option.ProjectKey) + " · " + option.Status
					if menu.Item(text).Checked(option.key() == s.requirementsSelectedSprintKey).Chosen() {
						s.requestRequirements(option.key(), false)
					}
				}
			})
			if iconButtonDisabled(c, "refresh-requirements", "refresh", "刷新当前 Sprint", p, s.requirementsLoading || s.requirementsSelectedSprintKey == "").Clicked() {
				s.requestRequirements(s.requirementsSelectedSprintKey, true)
			}
		})
		if s.requirementsLoadError != "" {
			s.requirementFailurePanel(c, p, []string{s.requirementsLoadError}, true)
		} else if len(s.requirementsFailures) > 0 {
			s.requirementFailurePanel(c, p, s.requirementsFailures, false)
		}
		if s.requirementsSelectedSprintKey == "" && len(s.requirementsSprints) > 0 {
			ui.Column(c).Grow(1).Center().Gap(8).Children(func() {
				label(c, "请选择要查看的 Sprint", 12, p.muted)
			})
			return
		}
		if len(s.requirements) == 0 {
			if s.requirementsLoading {
				s.renderRequirementListSkeleton(c, p, 5)
			} else {
				ui.Column(c).Grow(1).Center().Gap(8).Children(func() {
					if len(s.requirementsSprints) == 0 && s.requirementsLoadError == "" {
						label(c, "没有进行中或未开始的可选 Sprint", 12, p.muted)
					} else {
						label(c, "该 Sprint 中没有我参与的工作项", 12, p.muted)
					}
				})
			}
			return
		}
		ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Padding(1).Gap(4).Children(func() {
			for _, item := range s.requirements {
				s.requirementListItem(c, p, item, focused)
			}
		})
	})
}

func (s *app) requirementListItem(c *ui.Context, p palette, item application.Requirement, focused bool) {
	index := -1
	for i, candidate := range s.requirements {
		if requirementKey(candidate) == requirementKey(item) {
			index = i
			break
		}
	}
	selected := index >= 0 && index == s.selectedRequirement
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
		s.selectedRequirement = index
		s.selectedRequirementKey = requirementKey(item)
		if state := s.ensureRequirementListJobs(); state.current != nil && state.current.sprintKey == s.requirementsSelectedSprintKey {
			state.current.selectedKey = s.selectedRequirementKey
		}
		s.prioritizeRequirementNamingPreparation(item)
		s.prioritizeRequirementDocumentPreparation(item)
		s.lastResult = ""
		if focused {
			s.requirementsFocusedDetail = true
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
