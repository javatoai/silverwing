package desktop

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestTaskListDensityKeepsRowsCompactAcrossLayouts(test *testing.T) {
	for _, width := range []int{160, 280, 360} {
		for _, dark := range []bool{false, true} {
			for _, archived := range []bool{false, true} {
				test.Run(fmt.Sprintf("width-%d-dark-%t-archived-%t", width, dark, archived), func(test *testing.T) {
					state := taskNavigationFixture()
					state.dark = dark
					state.page = "tasks"
					if archived {
						state.page = "archive"
					}
					state.tasks = []task{
						{id: "density-active-short", title: "支付对账任务", createdAt: "2026-10-01", status: "待测试"},
						{id: "density-active-long", title: "跨区域支付链路异常定位与清算数据一致性验证", createdAt: "2026-10-02", status: "技术设计中"},
						{id: "density-active-finished", title: "月末结算核对", createdAt: "2026-10-03", status: "已验收"},
						{id: "density-archive-short", title: "旧客户端回归", createdAt: "2026-09-01", status: "已验收", archived: true},
						{id: "density-archive-long", title: "历史订单状态兼容性复核", createdAt: "2026-09-02", status: "技术设计中", archived: true},
						{id: "density-active-certificate", title: "证书加载", createdAt: "2026-10-04", status: "待开发"},
						{id: "density-active-rules", title: "黑名单规则", createdAt: "2026-10-05", status: "进行中"},
						{id: "density-archive-ledger", title: "历史流水核对", createdAt: "2026-09-03", status: "待测试", archived: true},
						{id: "density-archive-tools", title: "旧工具迁移", createdAt: "2026-09-04", status: "待开发", archived: true},
						{id: "density-archive-cleanup", title: "历史环境清理", createdAt: "2026-09-05", status: "进行中", archived: true},
					}
					selectedIndex := 1
					if archived {
						selectedIndex = 3
						state.setTaskPinned(state.tasks[selectedIndex], true)
					}
					state.selected = selectedIndex
					state.setTaskPinned(state.tasks[0], true)

					tester := taskNavigationTester(state, width, 520)
					rowIndexes := state.taskIndexRows(archived)
					if len(rowIndexes) != 5 {
						test.Fatalf("fixture should show five rows in archived=%t list, got %v", archived, rowIndexes)
					}
					var rowBounds []ui.Rect
					for _, taskIndex := range rowIndexes {
						item := state.tasks[taskIndex]
						row, found := tester.Find(item.title)
						if !found {
							test.Fatalf("task row %q missing at width %d", item.title, width)
						}
						rowBounds = append(rowBounds, row)
						if row.H != 40 {
							test.Errorf("task row %q height=%v, want 40 DIP", item.title, row.H)
						}
						if row.X < 0 || row.X+row.W > float32(width) {
							test.Errorf("task row %q overflows the list width: row=%+v viewport=%d", item.title, row, width)
						}
						status, hasStatus := tester.Find(item.status)
						if !hasStatus {
							test.Errorf("task status %q missing", item.status)
							continue
						}
						if status.X < row.X || status.X+status.W > row.X+row.W || status.X+status.W > float32(width) {
							test.Errorf("task status %q overflows its row: status=%+v row=%+v viewport=%d", item.status, status, row, width)
						}
						rowCenter := row.Y + row.H/2
						statusCenter := status.Y + status.H/2
						if statusCenter < rowCenter-3 || statusCenter > rowCenter+3 {
							test.Errorf("task status %q is not aligned on the title row: status=%+v row=%+v", item.status, status, row)
						}
					}
					for rowIndex := 1; rowIndex < len(rowBounds); rowIndex++ {
						pitch := rowBounds[rowIndex].Y - rowBounds[rowIndex-1].Y
						if pitch < 43 || pitch > 45 {
							test.Errorf("task row pitch=%v, want 44 DIP including the 4 DIP gap", pitch)
						}
					}
					if archived {
						if rowBounds[0].H != rowBounds[1].H {
							test.Errorf("selected and unselected archived rows should have equal height: first=%+v second=%+v", rowBounds[0], rowBounds[1])
						}
					} else {
						selectedRow, selectedFound := tester.Find(state.tasks[state.selected].title)
						unselectedRow, unselectedFound := tester.Find(state.tasks[2].title)
						if !selectedFound || !unselectedFound || selectedRow.H != unselectedRow.H {
							test.Errorf("selected and unselected rows should have equal height: selected=%+v unselected=%+v", selectedRow, unselectedRow)
						}
						if !tester.HasText(state.tasks[1].title) {
							test.Errorf("long task title %q is missing from the compact row", state.tasks[1].title)
						}
						saveFrameworkReadingFixture(test, tester, fmt.Sprintf("task-density-%d-dark-%t", width, dark))
					}
				})
			}
		}
	}
}
