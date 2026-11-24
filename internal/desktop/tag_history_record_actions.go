package desktop

import (
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func tagHistoryRetryPresentation(record application.TagRecord) (string, string) {
	switch record.RetryKind {
	case "RETAG":
		return "重新打Tag", "Genbu 构建失败，重新打Tag 后版本号自动 +1，当前记录将被替换。"
	case "RESUME_PARTIAL":
		return "继续构建Tag", "测试Tag的创建或推送尚未完成。解决记录中的错误后，可继续使用已记录的Tag和提交构建。"
	case "RETRY_INTERRUPTED":
		return "重试构建Tag", "上次Tag构建在源分支推送后被中断，尚未合并目标分支或创建测试Tag。"
	case "RETRY_BUILD":
		return "重试构建Tag", ""
	default:
		return "重试", ""
	}
}

func tagFailureSummary(record application.TagRecord) string {
	if strings.EqualFold(strings.TrimSpace(record.State), "CONFLICT") {
		return "合并存在冲突，请处理工作区后重试。"
	}
	return "工作区有未提交改动，请先提交或清理。"
}

func tagHistoryRecordCopyText(record application.TagRecord) string {
	if record.Tag != "" {
		return record.ServiceName + " · " + record.Tag
	}
	text := "服务名：" + record.ServiceName + "\n状态：" + tagHistoryStateLabel(record.State)
	if strings.TrimSpace(record.Detail) != "" {
		text += "\n说明：" + record.Detail
	}
	return text
}

// Keep the copied record and its desktop status in the Source user-facing terms.
func tagHistoryStateLabel(state string) string {
	switch domain.TagOperationState(state) {
	case domain.TagOperationCreated:
		return "已创建"
	case domain.TagOperationPreflightPassed:
		return "预检通过"
	case domain.TagOperationSourceBranchPushed:
		return "源分支已推送"
	case domain.TagOperationTargetBranchPushed:
		return "目标分支已推送"
	case domain.TagOperationLocalTagCreated:
		return "本地Tag已创建"
	case domain.TagOperationTagPushed:
		return "Tag已推送"
	case domain.TagOperationSuccess:
		return "构建成功"
	case domain.TagOperationConflict:
		return "存在冲突"
	case domain.TagOperationFailed:
		return "构建失败"
	case domain.TagOperationPartial:
		return "部分完成"
	default:
		return state
	}
}
