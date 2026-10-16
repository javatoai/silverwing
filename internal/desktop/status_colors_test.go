package desktop

import "testing"

func TestStatusColorCategories(t *testing.T) {
	for _, tc := range []struct{ text, category string }{
		{"技术设计中", "development"}, {"研发中", "development"}, {"进行中", "development"},
		{"TO DO", "planning"}, {"待排期", "planning"}, {"待测试", "testing"},
		{"开发完成，待测试", "testing"}, {"待评审", "testing"}, {"已验收", "done"},
		{"DONE", "done"}, {"CLOSED", "done"}, {"已完成", "done"}, {"未完成", "neutral"},
		{"not completed", "neutral"}, {"取消", "neutral"}, {"未读取", "neutral"},
		{"读取失败", "failed"}, {"FAILED", "failed"}, {"未标记", "neutral"},
		{"构建成功", "done"}, {"未成功", "neutral"},
	} {
		t.Run(tc.text, func(t *testing.T) {
			if got := statusColorCategory(tc.text); got != tc.category {
				t.Fatalf("%q: got %s, want %s", tc.text, got, tc.category)
			}
		})
	}
}
