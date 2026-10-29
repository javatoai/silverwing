package desktop

import (
	"encoding/json"
	"fmt"
	"silverwing-go/internal/application"
)

// applyDeliveryActionResult applies only delivery feature results on the UI thread.
func (s *app) applyDeliveryActionResult(action application.Action, result application.ActionResult) bool {
	switch action.Kind {
	case "tag.target.update":
		s.applyTagTargetResult(action, result)
	case "tag.status", "tag.genbu.refresh":
		s.applyTagStatusResult(action, result)
	case "tag.history.refresh":
		if !s.tagHistoryResultRelevant(action) {
			return true
		}
		var refresh application.TagHistoryRefreshResult
		raw, err := json.Marshal(result.Data)
		if err == nil {
			err = json.Unmarshal(raw, &refresh)
		}
		if err == nil && refresh.Records == nil {
			err = fmt.Errorf("响应缺少 Tag 历史记录列表")
		}
		if err != nil {
			if s.releaseUnit.state.tagHistory != nil {
				s.releaseUnit.state.tagHistory.Error = "读取 Tag 刷新结果失败：" + err.Error()
			}
			return true
		}
		if !tagHistoryRecordsOwned(action.TaskID, refresh.Records) {
			if s.releaseUnit.state.tagHistory != nil {
				s.releaseUnit.state.tagHistory.Error = "Tag 刷新响应包含其它任务的记录，请刷新后检查"
			}
			return true
		}
		s.applyTagHistoryRecords(action.TaskID, refresh.Records)
		if s.releaseUnit.state.tagHistory != nil {
			s.releaseUnit.state.tagHistory.Error = ""
		}
		*s.input(tagGenbuTaskErrorKey(action.TaskID), "") = refresh.StatusError
	case "tag.workspace-check":
		s.applyTagWorkspaceCheckResult(action, result)
	case "tag.history":
		if !s.tagHistoryResultRelevant(action) {
			return true
		}
		items, err := decodeData[application.TagRecord](result.Data)
		if err != nil {
			s.lastError = "读取 Tag 历史格式失败：" + err.Error()
			return true
		}
		if result.Data != nil && tagHistoryRecordsOwned(action.TaskID, items) {
			s.applyTagHistoryRecords(action.TaskID, items)
		} else if result.Data != nil {
			s.lastError = "Tag历史响应包含其它任务的记录，请刷新后检查"
		}
	case "tag.history.delete", "tag.history.clear":
		if s.tagHistoryResultRelevant(action) {
			s.applyTagHistoryMutationResult(action, result)
		}
	default:
		return false
	}
	return true
}

func (s *app) applyTagHistoryRecords(taskID string, items []application.TagRecord) {
	if taskID == "" {
		s.snapshot.TagHistory = items
		return
	}
	kept := make([]application.TagRecord, 0, len(s.snapshot.TagHistory)+len(items))
	for _, record := range s.snapshot.TagHistory {
		if record.TaskID != taskID {
			kept = append(kept, record)
		}
	}
	for _, record := range items {
		if record.TaskID == taskID {
			kept = append(kept, record)
		}
	}
	s.snapshot.TagHistory = kept
}

func (s *app) applyTagHistoryMutationResult(action application.Action, result application.ActionResult) {
	if result.Data == nil {
		return
	}
	raw, err := json.Marshal(result.Data)
	var mutation application.TagHistoryMutationResult
	if err == nil {
		err = json.Unmarshal(raw, &mutation)
	}
	if err != nil {
		s.lastError = "Tag历史删除结果格式错误：" + err.Error()
		return
	}
	if mutation.Records != nil {
		if !tagHistoryRecordsOwned(action.TaskID, mutation.Records) {
			s.lastError = "Tag历史删除响应包含其它任务的记录，请刷新后检查"
			return
		}
		s.applyTagHistoryRecords(action.TaskID, mutation.Records)
	}
	if s.releaseUnit.state.tagHistory != nil {
		s.releaseUnit.state.tagHistory.DeleteOpen = false
		s.releaseUnit.state.tagHistory.DeleteIDs = nil
		s.releaseUnit.state.tagHistory.Selected = map[string]bool{}
		s.releaseUnit.state.tagHistory.Error = ""
	}
}

func (s *app) applyTagHistoryMutationFailure(action application.Action, result application.ActionResult, err error) bool {
	if !tagHistoryMutation(action.Kind) {
		return false
	}
	if !s.tagHistoryResultRelevant(action) {
		return true
	}
	s.applyTagHistoryMutationResult(action, result)
	s.lastError = fmt.Sprintf("Tag历史操作未完全完成：%v", err)
	if s.releaseUnit.state.tagHistory != nil {
		s.releaseUnit.state.tagHistory.Error = s.lastError
	}
	return true
}

func tagHistoryMutation(kind string) bool {
	return kind == "tag.history.delete" || kind == "tag.history.clear"
}

func tagHistoryDedicatedResult(kind string) bool {
	return kind == "tag.history" || kind == "tag.history.refresh" || tagHistoryMutation(kind)
}

func (s *app) applyTagHistoryRefreshFailure(action application.Action, err error) bool {
	if action.Kind != "tag.history.refresh" {
		return false
	}
	if !s.tagHistoryResultRelevant(action) {
		return true
	}
	if s.releaseUnit.state.tagHistory != nil {
		s.releaseUnit.state.tagHistory.Error = fmt.Sprintf("刷新 Tag 历史失败：%v", err)
	}
	return true
}

func (s *app) tagHistoryResultRelevant(action application.Action) bool {
	if expected, provided := action.Payload["tagHistoryHome"].(string); provided && expected != s.snapshot.Home {
		return false
	}
	if expected, provided := action.Payload["tagHistoryTaskRoot"].(string); provided && expected != s.configString("taskRoot") {
		return false
	}
	if action.TaskID != "" {
		t := s.taskUnit.currentTask()
		if t.id != action.TaskID {
			return false
		}
		if expected, provided := action.Payload["tagHistoryTaskPath"].(string); provided && expected != t.path {
			return false
		}
		if expected, provided := action.Payload["tagHistoryTaskCreatedAt"].(string); provided && expected != t.createdAt {
			return false
		}
	}
	return true
}

func tagHistoryRecordsOwned(taskID string, items []application.TagRecord) bool {
	if taskID == "" {
		return true
	}
	for _, item := range items {
		if item.TaskID != taskID {
			return false
		}
	}
	return true
}
