package desktop

import (
	"slices"
	"sort"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/delivery"
	"silverwing-go/internal/domain"
)

type tagHistoryViewState struct {
	Home           string
	TaskRoot       string
	TaskID         string
	TaskPath       string
	TaskCreatedAt  string
	Managing       bool
	Selected       map[string]bool // Complete history group IDs, never child IDs.
	DeleteOpen     bool
	DeleteAll      bool
	DeleteIDs      []string
	DeleteGroupIDs []string
	Error          string
}

type tagHistoryGroup struct {
	Item    domain.TagHistoryItem
	Visible []application.TagRecord
}

func (s *app) ensureTagHistoryView() *tagHistoryViewState {
	return s.ensureTaskTagHistoryView(s.taskUnit.currentTask())
}

func (s *app) ensureTaskTagHistoryView(t task) *tagHistoryViewState {
	root := s.configString("taskRoot")
	if s.releaseUnit.state.tagHistory == nil || s.releaseUnit.state.tagHistory.Home != s.snapshot.Home || s.releaseUnit.state.tagHistory.TaskRoot != root ||
		s.releaseUnit.state.tagHistory.TaskID != t.id || s.releaseUnit.state.tagHistory.TaskPath != t.path || s.releaseUnit.state.tagHistory.TaskCreatedAt != t.createdAt {
		s.releaseUnit.state.tagHistory = &tagHistoryViewState{Home: s.snapshot.Home, TaskRoot: root,
			TaskID: t.id, TaskPath: t.path, TaskCreatedAt: t.createdAt, Selected: map[string]bool{}}
	}
	state := s.releaseUnit.state.tagHistory
	if state.Selected == nil {
		state.Selected = map[string]bool{}
	}
	groups := filterTagHistory(s.taskTagHistoryRecords(state.TaskID), "", false)
	existing := make(map[string]bool, len(groups))
	for _, group := range groups {
		existing[group.Item.GroupID] = true
	}
	for id := range state.Selected {
		if !existing[id] {
			delete(state.Selected, id)
		}
	}
	if state.DeleteOpen {
		selected := make(map[string]bool, len(state.DeleteGroupIDs))
		for _, id := range state.DeleteGroupIDs {
			selected[id] = true
		}
		ids, _ := tagHistoryDeleteSelection(groups, selected, state.DeleteAll)
		if !slices.Equal(ids, state.DeleteIDs) {
			state.DeleteOpen = false
			state.DeleteIDs, state.DeleteGroupIDs = nil, nil
			state.Error = "所选批次已变化，请重新选择并确认。"
		}
	}
	return state
}

func (s *app) taskTagHistoryRecords(taskID string) []application.TagRecord {
	s.restoreActiveTagJobRecords()
	items := []application.TagRecord{}
	if taskID == "" {
		return items
	}
	for _, record := range s.snapshot.TagHistory {
		if record.TaskID == taskID {
			items = append(items, record)
		}
	}
	return items
}

func (s *app) tagHistoryStateCurrent(state *tagHistoryViewState) bool {
	if state == nil || state.TaskID == "" || s.releaseUnit.state.tagHistory != state || state.Home != s.snapshot.Home || state.TaskRoot != s.configString("taskRoot") {
		return false
	}
	t := s.taskUnit.currentTask()
	return t.id == state.TaskID && t.path == state.TaskPath && t.createdAt == state.TaskCreatedAt
}

func tagHistoryScopePayload(state *tagHistoryViewState) map[string]any {
	return map[string]any{"tagHistoryHome": state.Home, "tagHistoryTaskRoot": state.TaskRoot,
		"tagHistoryTaskPath": state.TaskPath, "tagHistoryTaskCreatedAt": state.TaskCreatedAt}
}

func tagHistoryMenuKey(state *tagHistoryViewState, item string) string {
	if state == nil {
		return "tag-history\x00" + item
	}
	return strings.Join([]string{"tag-history", state.Home, state.TaskRoot, state.TaskPath, state.TaskCreatedAt, state.TaskID, item}, "\x00")
}

func (s *app) tagHistoryRecordPayload(record application.TagRecord) map[string]any {
	t := s.taskUnit.taskByID(record.TaskID)
	return map[string]any{"operationId": record.ID, "tagHistoryHome": s.snapshot.Home,
		"tagHistoryTaskRoot": s.configString("taskRoot"), "tagHistoryTaskPath": t.path, "tagHistoryTaskCreatedAt": t.createdAt}
}

func tagHistoryOperation(record application.TagRecord) domain.TagOperation {
	return domain.TagOperation{OperationID: record.ID, FolderName: record.FolderName,
		ServiceName: record.ServiceName, GroupServiceID: record.ServiceID, ModuleID: record.ModuleID,
		SourceBranch: record.SourceBranch, TargetBranch: &record.TargetBranch, Remote: record.Remote,
		Tag: &record.Tag, BatchID: &record.BatchID, CreatedAt: record.CreatedAt, UpdatedAt: record.Updated,
		State: domain.TagOperationState(record.State), Message: &record.Detail, GenbuStatus: record.GenbuStatus}
}

func filterTagHistory(records []application.TagRecord, query string, onlyProblems bool) []tagHistoryGroup {
	operations := make([]domain.TagOperation, 0, len(records))
	byID := make(map[string]application.TagRecord, len(records))
	for _, record := range records {
		operations = append(operations, tagHistoryOperation(record))
		byID[record.ID] = record
	}
	query = strings.ToLower(strings.TrimSpace(query))
	groups := []tagHistoryGroup{}
	for _, item := range delivery.GroupTagHistory(operations) {
		group := tagHistoryGroup{Item: item}
		for _, operation := range item.Operations {
			record := byID[operation.OperationID]
			if onlyProblems && !tagHistoryProblem(record) {
				continue
			}
			match := query == ""
			for _, text := range []string{record.ServiceName, record.FolderName, record.Tag, record.SourceBranch, record.TargetBranch, record.Detail} {
				if strings.Contains(strings.ToLower(text), query) {
					match = true
					break
				}
			}
			if match {
				group.Visible = append(group.Visible, record)
			}
		}
		// The delivery group preserves update-time order for retention and
		// announcements. Sort only this presentation copy by creation time.
		sort.SliceStable(group.Visible, func(i, j int) bool {
			if group.Visible[i].CreatedAt != group.Visible[j].CreatedAt {
				return group.Visible[i].CreatedAt > group.Visible[j].CreatedAt
			}
			return group.Visible[i].ID < group.Visible[j].ID
		})
		if len(group.Visible) > 0 {
			groups = append(groups, group)
		}
	}
	// Retention remains ordered by update time in the delivery layer. The Tag
	// page presents the same complete groups by their creation time instead.
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Item.CreatedAt != groups[j].Item.CreatedAt {
			return groups[i].Item.CreatedAt > groups[j].Item.CreatedAt
		}
		return groups[i].Item.GroupID < groups[j].Item.GroupID
	})
	return groups
}

func tagHistoryProblem(record application.TagRecord) bool {
	return record.State == "CONFLICT" || record.State == "FAILED" || record.State == "PARTIAL" ||
		record.RetryKind == "RETRY_INTERRUPTED" || record.RetryKind == "RETAG"
}

func tagHistoryGroupState(operations []domain.TagOperation) string {
	for _, state := range []domain.TagOperationState{domain.TagOperationConflict, domain.TagOperationFailed, domain.TagOperationPartial} {
		for _, operation := range operations {
			if operation.State == state {
				return string(state)
			}
		}
	}
	allSuccess := len(operations) > 0
	for _, operation := range operations {
		if operation.State != domain.TagOperationSuccess {
			allSuccess = false
		}
	}
	if allSuccess {
		return "SUCCESS"
	}
	if len(operations) > 0 {
		return string(operations[0].State)
	}
	return "CREATED"
}

func (s *app) tagHistoryAnnouncement(group tagHistoryGroup) string {
	operations := append([]domain.TagOperation(nil), group.Item.Operations...)
	sort.SliceStable(operations, func(i, j int) bool {
		if operations[i].CreatedAt != operations[j].CreatedAt {
			return operations[i].CreatedAt < operations[j].CreatedAt
		}
		return operations[i].OperationID < operations[j].OperationID
	})
	title := ""
	if len(group.Visible) > 0 {
		for _, item := range s.taskUnit.state.tasks {
			if item.id == group.Visible[0].TaskID {
				metadata := s.taskRequirementMetadataFor(item)
				if metadata.Metadata.Title != nil {
					title = strings.TrimSpace(*metadata.Metadata.Title)
				}
				title = firstNonEmpty(title, item.requirementTitle, item.title)
				break
			}
		}
	}
	return delivery.FormatTagAnnouncement(firstNonEmpty(title, group.Item.FolderName, "未命名需求"), operations, true)
}

func tagHistoryDeleteSelection(groups []tagHistoryGroup, selected map[string]bool, all bool) ([]string, []string) {
	ids, groupIDs := []string{}, []string{}
	for _, group := range groups {
		if !all && !selected[group.Item.GroupID] {
			continue
		}
		groupIDs = append(groupIDs, group.Item.GroupID)
		for _, operation := range group.Item.Operations {
			ids = append(ids, operation.OperationID)
		}
	}
	sort.Strings(ids)
	return ids, groupIDs
}

func (s *app) beginTagHistoryDelete(all bool) {
	state := s.ensureTagHistoryView()
	if s.busy || !s.tagHistoryStateCurrent(state) {
		return
	}
	groups := filterTagHistory(s.taskTagHistoryRecords(state.TaskID), "", false)
	state.DeleteIDs, state.DeleteGroupIDs = tagHistoryDeleteSelection(groups, state.Selected, all)
	state.Error = ""
	state.DeleteAll, state.DeleteOpen = all, len(state.DeleteIDs) > 0
}
