package desktop

import (
	"encoding/json"
	"fmt"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"

	"github.com/egoist/mygo/ui"
)

func tagGenbuErrorKey(operationID string) string {
	if strings.TrimSpace(operationID) == "" {
		return "tag-genbu-error:global"
	}
	return "tag-genbu-error:" + operationID
}

func tagGenbuTaskErrorKey(taskID string) string { return "tag-genbu-error:task:" + taskID }

func tagGenbuActionErrorKey(action application.Action) string {
	if action.Kind == "tag.genbu.refresh" && action.TaskID != "" {
		return tagGenbuTaskErrorKey(action.TaskID)
	}
	return tagGenbuErrorKey(payloadString(action.Payload, "operationId"))
}

func (s *app) tagGenbuError(operationID string) string {
	return strings.TrimSpace(*s.input(tagGenbuErrorKey(operationID), ""))
}

// applyTagStatusResult merges only Genbu-owned state into existing history rows.
// The full-refresh response contains []TagRecord; the single-operation response
// contains one TagRecord. Both merge only into the same Tag version.
func (s *app) applyTagStatusResult(action application.Action, result application.ActionResult) {
	if _, scopedRequest := action.Payload["tagHistoryTaskPath"]; scopedRequest && !s.tagHistoryResultRelevant(action) {
		return
	}
	if action.Kind == "tag.genbu.refresh" {
		if action.TaskID != "" && !s.tagHistoryResultRelevant(action) {
			return
		}
		if result.Data == nil {
			s.applyTagStatusFailure(action, fmt.Errorf("Genbu 刷新没有返回状态记录"))
			return
		}
		items, err := decodeData[application.TagRecord](result.Data)
		if err != nil {
			s.applyTagStatusFailure(action, err)
			return
		}
		if !tagHistoryRecordsOwned(action.TaskID, items) {
			s.applyTagStatusFailure(action, fmt.Errorf("Genbu 刷新响应包含其它任务的记录"))
			return
		}
		s.applyTagStatusRecords(items)
		if action.TaskID != "" {
			*s.input(tagGenbuTaskErrorKey(action.TaskID), "") = ""
			for _, item := range items {
				*s.input(tagGenbuErrorKey(item.ID), "") = ""
			}
			return
		}
		for key := range s.inputs {
			if strings.HasPrefix(key, "tag-genbu-error:") {
				*s.inputs[key] = ""
			}
		}
		return
	}
	if action.Kind != "tag.status" {
		return
	}
	raw, err := json.Marshal(result.Data)
	if err != nil {
		s.applyTagStatusFailure(action, err)
		return
	}
	var item application.TagRecord
	if err := json.Unmarshal(raw, &item); err != nil {
		s.applyTagStatusFailure(action, err)
		return
	}
	wanted := payloadString(action.Payload, "operationId")
	if item.ID == "" || (wanted != "" && item.ID != wanted) {
		s.applyTagStatusFailure(action, fmt.Errorf("Genbu 状态响应与请求记录不匹配"))
		return
	}
	if (action.TaskID != "" && item.TaskID != action.TaskID) ||
		(action.ServiceID != "" && item.ServiceID != action.ServiceID) ||
		(action.ModuleID != "" && item.ModuleID != action.ModuleID) {
		s.applyTagStatusFailure(action, fmt.Errorf("Genbu 状态响应的任务或模块与请求不匹配"))
		return
	}
	s.applyTagStatusRecords([]application.TagRecord{item})
	*s.input(tagGenbuErrorKey(item.ID), "") = ""
}

// applyTagStatusFailure keeps CLI/probe errors beside Genbu status instead of
// changing the local Tag operation state or replacing its build message.
func (s *app) applyTagStatusFailure(action application.Action, err error) {
	if action.Kind != "tag.status" && action.Kind != "tag.genbu.refresh" {
		return
	}
	if _, scopedRequest := action.Payload["tagHistoryTaskPath"]; scopedRequest && !s.tagHistoryResultRelevant(action) {
		return
	}
	if action.Kind == "tag.genbu.refresh" && action.TaskID != "" && !s.tagHistoryResultRelevant(action) {
		return
	}
	message := "Genbu 刷新失败"
	if err != nil {
		message = err.Error()
	}
	*s.input(tagGenbuActionErrorKey(action), "") = message
}

func (s *app) applyTagStatusRecords(items []application.TagRecord) {
	type identity struct{ taskID, serviceID, moduleID, operationID, tag, updated string }
	byIdentity := make(map[identity]application.TagRecord, len(items))
	for _, item := range items {
		if item.ID != "" {
			key := identity{item.TaskID, item.ServiceID, item.ModuleID, item.ID, item.Tag, item.Updated}
			byIdentity[key] = item
		}
	}
	for i := range s.snapshot.TagHistory {
		item := s.snapshot.TagHistory[i]
		key := identity{item.TaskID, item.ServiceID, item.ModuleID, item.ID, item.Tag, item.Updated}
		if incoming, ok := byIdentity[key]; ok {
			s.snapshot.TagHistory[i].GenbuStatus = incoming.GenbuStatus
			s.snapshot.TagHistory[i].RetryKind = incoming.RetryKind
		}
	}
}

func tagHistoryRetryable(record application.TagRecord) bool {
	switch record.RetryKind {
	case "RESOLVE_CONFLICT", "RETRY_INTERRUPTED", "RETRY_BUILD", "RESUME_PARTIAL", "RETAG":
		return true
	default:
		return false
	}
}

func renderTagGenbuStages(c *ui.Context, p palette, dark bool, record application.TagRecord) {
	status := record.GenbuStatus
	build, uat := status.Build, status.UAT
	if status.NotFound {
		build, uat = domain.GenbuUnknown, domain.GenbuUnknown
	}
	// This row stays intact when the owning record wraps. Genbu currently has
	// no log/detail action in the desktop contract, so these are status controls.
	ui.Row(c).Shrink(0).Gap(6).AlignItems(ui.Center).Children(func() {
		renderGenbuStage(c, p, dark, "构建", build)
		renderGenbuStage(c, p, dark, "UAT", uat)
	})
}

func renderTagGenbuNotes(c *ui.Context, p palette, dark bool, record application.TagRecord, requestError string) {
	if requestError != "" {
		label(c, "Genbu 刷新失败："+requestError, 11, tagStatusErrorColor(dark)).LineHeight(1.4)
	}
	status := record.GenbuStatus
	if status.NotFound {
		label(c, "Genbu：未找到该 Tag", 11, p.muted)
	} else if status.FailureReason != nil && strings.TrimSpace(*status.FailureReason) != "" {
		label(c, "Genbu CLI 查询失败："+strings.TrimSpace(*status.FailureReason), 11, tagStatusErrorColor(dark)).LineHeight(1.4)
	}
	if status.StoppedByNewerRelease {
		label(c, "后续 Tag 已发布，停止自动探测", 10, p.muted)
	}
}

func renderGenbuStage(c *ui.Context, p palette, dark bool, stage string, status domain.GenbuStageStatus) {
	name, tint := genbuStageLabel(status)
	text := stage + " " + name
	ui.Box(c).Shrink(0).Height(28).Padding(0, 9).Center().Radius(8).Border(1, p.line).
		Background(tint.background(p, dark)).Label(text).Tooltip(text).Children(func() {
		label(c, text, 11, tint.foreground(p, dark)).NoWrap()
	})
}

type genbuStageTint int

const (
	genbuTintNeutral genbuStageTint = iota
	genbuTintActive
	genbuTintSuccess
	genbuTintFailed
)

func genbuStageLabel(status domain.GenbuStageStatus) (string, genbuStageTint) {
	switch status {
	case domain.GenbuInitial:
		return "未开始", genbuTintNeutral
	case domain.GenbuBuilding:
		return "进行中", genbuTintActive
	case domain.GenbuSuccess:
		return "成功", genbuTintSuccess
	case domain.GenbuFailed:
		return "失败", genbuTintFailed
	default:
		return "未知", genbuTintNeutral
	}
}

func (t genbuStageTint) background(p palette, dark bool) ui.Color {
	switch t {
	case genbuTintActive:
		return p.selected
	case genbuTintSuccess:
		return p.greenBg
	case genbuTintFailed:
		if dark {
			return ui.Hex("#4A1C1B")
		}
		return ui.Hex("#FEE4E2")
	default:
		return p.soft
	}
}

func (t genbuStageTint) foreground(p palette, dark bool) ui.Color {
	switch t {
	case genbuTintActive:
		return p.blue
	case genbuTintSuccess:
		return p.green
	case genbuTintFailed:
		return tagStatusErrorColor(dark)
	default:
		return p.muted
	}
}

func tagStatusErrorColor(dark bool) ui.Color {
	if dark {
		return ui.Hex("#FDA29B")
	}
	return ui.Hex("#B42318")
}
