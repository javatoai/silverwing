package desktop

import (
	"strings"
	"unicode"
)

// Keep business stages separate from read failures, and match unfinished
// stages before completion: "开发完成，待测试" is still waiting for testing.
func statusColorCategory(status string) string {
	text := strings.ToLower(strings.TrimSpace(status))
	has := func(values ...string) bool {
		for _, value := range values {
			if strings.Contains(text, value) {
				return true
			}
		}
		return false
	}
	switch {
	case has("取消", "暂停", "挂起", "拒绝", "不做", "终止", "cancelled", "canceled", "paused", "archived", "已归档"):
		return "neutral"
	case has("失败", "错误") || text == "failed" || text == "error":
		return "failed"
	case has("评审", "待确认", "提测", "待测试", "测试中", "验收中", "待验收", "review", "testing"):
		return "testing"
	case has("技术设计", "设计中", "开发中", "研发中", "进行中", "实现中", "编码中", "in progress", "developing") || text == "开发" || text == "研发":
		return "development"
	case has("待排期", "排期中", "规划中", "待开始", "未开始", "待开发") || text == "todo" || text == "to do" || text == "pending":
		return "planning"
	case has("未完成", "未验收", "未发布", "未关闭", "未成功", "不成功", "not done", "not completed", "not resolved", "unresolved", "incomplete", "unfinished", "undone"):
		return "neutral"
	case has("已完成", "已验收", "已发布", "已关闭") || strings.HasSuffix(text, "完成") || strings.HasSuffix(text, "成功") || text == "干净" || text == "已就绪" || text == "就绪" || text == "success" || text == "ready":
		return "done"
	}
	for _, word := range strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) }) {
		switch word {
		case "done", "closed", "resolved", "completed":
			return "done"
		}
	}
	return "neutral"
}
