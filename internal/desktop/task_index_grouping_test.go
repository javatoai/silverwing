package desktop

import (
	"fmt"
	"image/color"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

type taskIndexGroupFixtureRow struct {
	title, status, updated string
}

func taskIndexGroupFixture(rows []taskIndexGroupFixtureRow) (*app, []task) {
	s := newApp(nil, Options{CapturePath: "isolated-task-index-grouping"})
	s.loading, s.page = false, "tasks"
	items := make([]task, len(rows))
	for i, row := range rows {
		item := task{id: fmt.Sprint(i), title: row.title, updated: row.updated, createdAt: "2026-11-20 12:00:00"}
		if row.status != "" {
			item.requirementURL = "https://project.feishu.cn/obt/userstory/detail/" + item.id
			status := row.status
			s.setTaskRequirementMetadata(item.id, taskRequirementMetadataState{
				URL: item.requirementURL, Status: requirementMetadataLoaded, Metadata: requirementMetadata{Status: &status},
			})
		}
		items[i] = item
	}
	s.taskUnit.replaceTasks(items, 0)
	return s, items
}

func assertTaskIndexGroupRows(t *testing.T, s *app, archived bool, want []int, starts map[int]bool) {
	t.Helper()
	if got := s.taskIndexRows(archived); !slices.Equal(got, want) {
		t.Fatalf("task projection order %v, want %v", got, want)
	}
	if got := s.taskIndexRowsFor(s.taskUnit.state.tasks, archived); !slices.Equal(got, want) {
		t.Fatalf("next-snapshot order %v, want %v", got, want)
	}
	if got := s.taskIndexBrowsingFor(archived).projection.groupStarts; !maps.Equal(got, starts) {
		t.Fatalf("group starts %v, want %v", got, starts)
	}
}

func TestTaskIndexGroupsSortAllRequirementStatuses(t *testing.T) {
	groups := [][]string{
		{"需求提出", "产品设计中", "待评审", "待技术调研", "技术设计中", "设计中", "待技术评审", "待测试用例评审"},
		{"待排期", "待开发", "待办", "TO DO", "开发中", "进行中", "IN PROGRESS"},
		{"待提测", "待测试", "测试中", "TESTING", "待验收"},
		{"已验收", "DONE", "CLOSED", "已终止"},
	}
	var rows []taskIndexGroupFixtureRow
	var want []int
	starts := make(map[int]bool)
	// Put later stages first in the source, with newer timestamps. Within
	// each stage, the most advanced status is also the most recently updated.
	for group := len(groups) - 1; group >= 0; group-- {
		first := len(rows)
		for minute, status := range groups[group] {
			updated := fmt.Sprintf("2026-11-26T%02d:%02d:00Z", group, minute)
			rows = append(rows, taskIndexGroupFixtureRow{status, status, updated})
		}
		starts[len(rows)-1] = true
		for i := first; i < len(rows); i++ {
			want = append(want, i)
		}
	}
	slices.Reverse(want)
	s, _ := taskIndexGroupFixture(rows)
	assertTaskIndexGroupRows(t, s, false, want, starts)
}

func TestTaskIndexGroupsKeepPinsArchiveAndTimestampFallback(t *testing.T) {
	s, items := taskIndexGroupFixture([]taskIndexGroupFixtureRow{
		{"design", "待评审", "2026-10-01T09:00:00Z"},
		{"same instant, first", "开发中", "2026-11-26 08:31:00"},
		{"same instant, second", "TO DO", "2026-11-26T00:31:00Z"},
		{"older in Shanghai", "待排期", "2026-11-26 08:30:00"},
		{"creation fallback", "进行中", ""},
		{"invalid update fallback", "待办", "invalid"},
		{"unknown state", "custom state", "2026-11-26T10:00:00Z"},
		{"unlinked", "", "2026-11-26T11:00:00Z"},
		{"unread", "", "2026-11-26T12:00:00Z"},
		{"pinned complete", "DONE", "2026-10-01T09:00:00Z"},
		{"pinned development", "开发中", "2026-11-26T08:00:00Z"},
		{"archived complete", "已验收", "2026-10-01T09:00:00Z"},
		{"archived design", "需求提出", "2026-11-26T08:00:00Z"},
		{"archived pin", "待排期", "2026-10-01T09:00:00Z"},
	})
	items[4].createdAt = "2026-11-26T00:31:00.1Z"
	items[5].createdAt = "2026-11-26T08:30:30+08:00"
	items[7].status = "进行中"
	items[8].requirementURL = "https://project.feishu.cn/obt/task/detail/8"
	for i := 11; i < len(items); i++ {
		items[i].archived = true
	}
	s.taskUnit.replaceTasks(items, 0)
	for _, i := range []int{10, 9, 13} {
		s.setTaskPinned(items[i], true)
	}
	assertTaskIndexGroupRows(t, s, false, []int{9, 10, 0, 4, 1, 2, 5, 3, 8, 7, 6}, map[int]bool{0: true, 4: true})
	assertTaskIndexGroupRows(t, s, true, []int{13, 11, 12}, nil)
}

func TestTaskIndexGroupsFollowFilteredRowsAndRefreshedMetadata(t *testing.T) {
	s, items := taskIndexGroupFixture([]taskIndexGroupFixtureRow{
		{"支付任务甲", "待验收", "2026-11-26T09:00:00Z"},
		{"退款任务", "待评审", "2026-11-26T10:00:00Z"},
		{"支付任务乙", "需求提出", "2026-11-26T08:00:00Z"},
		{"支付任务丙", "开发中", "2026-11-26T07:00:00Z"},
	})
	state := s.taskIndexBrowsingFor(false)
	assertTaskIndexGroupRows(t, s, false, []int{1, 2, 3, 0}, map[int]bool{1: true, 3: true, 0: true})
	state.search = "支付"
	assertTaskIndexGroupRows(t, s, false, []int{2, 3, 0}, map[int]bool{2: true, 3: true, 0: true})

	// The refresh publishes a loading state carrying the previous status.
	metadata := s.taskRequirementMetadataFor(items[0])
	metadata.Status = requirementMetadataLoading
	s.setTaskRequirementMetadata(items[0].id, metadata)
	assertTaskIndexGroupRows(t, s, false, []int{2, 3, 0}, map[int]bool{2: true, 3: true, 0: true})
	status := "技术设计中"
	metadata.Metadata.Status = &status
	metadata.Status = requirementMetadataLoaded
	s.setTaskRequirementMetadata(items[0].id, metadata)
	assertTaskIndexGroupRows(t, s, false, []int{0, 2, 3}, map[int]bool{0: true, 3: true})
	if selected := s.taskUnit.currentTask(); selected.id != items[0].id {
		t.Fatalf("refresh selected %q instead of %q", selected.title, items[0].title)
	}
	state.search = ""

	view := taskNavigationTester(s, 240, 360)
	navigationClick(t, view, "搜索任务")
	view.Type("支付")
	for _, i := range []int{0, 2, 3} {
		view.Key(0, ui.KeyDown)
		if !view.Focused(items[i].title) {
			t.Fatalf("keyboard navigation did not focus %q", items[i].title)
		}
	}
	if s.taskUnit.currentTask().id != items[0].id {
		t.Fatal("keyboard navigation changed selected task before activation")
	}
}

func TestTaskIndexGroupDividersAndTaskNameHover(t *testing.T) {
	for _, width := range []int{130, 280} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("width-%d-dark-%t", width, dark), func(t *testing.T) {
				s, items := taskIndexGroupFixture([]taskIndexGroupFixtureRow{
					{"已置顶同组任务", "产品设计中", "2026-11-26T09:00:00Z"},
					{"设计跨多端业务联动与异常场景兼容的长任务名称", "待评审", "2026-11-26T08:00:00Z"},
					{"短任务", "需求提出", "2026-11-26T07:00:00Z"},
					{"开发任务", "开发中", "2026-11-26T06:00:00Z"},
					{"未关联任务", "", "2026-11-26T05:00:00Z"},
				})
				s.dark, s.listWidth = dark, float32(width)
				s.setTaskPinned(items[0], true)
				view := taskNavigationTester(s, width, 360)
				assertTaskIndexDivider(t, view, items[0].title, dark, false)
				assertTaskIndexDivider(t, view, items[1].title, dark, true)
				assertTaskIndexDivider(t, view, items[2].title, dark, false)
				assertTaskIndexDivider(t, view, items[3].title, dark, true)
				assertTaskIndexDivider(t, view, items[4].title, dark, false)
				captureDesktopTestFrame(t, view, fmt.Sprintf("task-index-dividers-%d-dark-%t", width, dark))

				for _, i := range []int{1, 2} {
					title := items[i].title
					row, _ := view.Find(title)
					before := taskIndexTextCount(view, title)
					x, y := int(row.X+row.W-4), int(row.Y+row.H-8)
					idlePixel := view.Image().RGBAAt(x, y)
					view.Move(row.X+24, row.Y+row.H/2)
					view.Advance(750 * time.Millisecond)
					if after := taskIndexTextCount(view, title); after != before {
						t.Fatalf("hover added a duplicate task name %q: count %d -> %d", title, before, after)
					}
					if view.Image().RGBAAt(x, y) == idlePixel {
						t.Fatalf("task %q no longer highlights on hover", title)
					}
				}
				status, ok := view.Find("开发中")
				if !ok {
					t.Fatal("status label or dot is missing")
				}
				before := taskIndexTextCount(view, "开发中")
				view.Move(status.X+status.W/2, status.Y+status.H/2)
				view.Advance(750 * time.Millisecond)
				if taskIndexTextCount(view, "开发中") <= before {
					t.Fatal("hovering the status no longer shows its tooltip")
				}

				navigationClick(t, view, "搜索任务")
				view.Type("短任务")
				assertTaskIndexDivider(t, view, items[2].title, dark, true)
			})
		}
	}
}

func assertTaskIndexDivider(t *testing.T, view *ui.Tester, title string, dark, want bool) {
	t.Helper()
	row, found := view.Find(title)
	if !found || row.H < 20 {
		t.Fatalf("task row %q is missing or clipped: %+v", title, row)
	}
	line := colors(dark).line
	linePixel := color.RGBA{line.R, line.G, line.B, line.A}
	pixels := view.Image()
	x, top := int(row.X+row.W/2), int(row.Y)
	count := 0
	for y := top - 6; y < top-3; y++ {
		if pixels.RGBAAt(x, y) != linePixel {
			continue
		}
		if pixels.RGBAAt(int(row.X)+3, y) == linePixel {
			t.Fatalf("divider before %q extends into the row margin", title)
		}
		count++
	}
	if want && count != 1 || !want && count != 0 {
		t.Fatalf("task %q has %d divider pixels in the gap at row %+v, want divider %t", title, count, row, want)
	}
}

func taskIndexTextCount(view *ui.Tester, value string) int {
	count := 0
	for _, text := range view.Texts() {
		if text == value {
			count++
		}
	}
	return count
}
