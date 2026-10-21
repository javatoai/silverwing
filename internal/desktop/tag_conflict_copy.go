package desktop

import (
	"fmt"
	"strings"

	"silverwing-go/internal/application"
)

func tagFailureAssistanceText(record application.TagRecord, failureKind string) string {
	if failureKind == application.TagJobFailureUncommitted ||
		(tagWorkspaceCheckEligible(record) && !strings.EqualFold(strings.TrimSpace(record.State), "CONFLICT")) {
		return fmt.Sprintf("我在为服务 %s 的 %s 分支构建 Tag 时，工作区存在未提交改动，请你检查并处理。",
			firstNonEmpty(record.ServiceName, record.ServiceID), firstNonEmpty(record.SourceBranch, "当前"))
	}
	return record.ConflictSummary
}

func tagConflictCopyText(record application.TagRecord) string {
	files := "未返回具体文件"
	if len(record.ConflictFiles) > 0 {
		files = strings.Join(record.ConflictFiles, "、")
	}
	return record.ConflictSummary + "\n冲突文件：" + files
}
