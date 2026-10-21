package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func tagLayoutFixture(t *testing.T) *app {
	t.Helper()
	root := t.TempDir()
	config, err := json.Marshal(map[string]string{"taskRoot": filepath.Join(root, "tasks"), "materialsRoot": filepath.Join(root, "materials")})
	if err != nil {
		t.Fatal(err)
	}
	checked, completed := "2026-10-15 13:48:21", "2026-10-14 17:42:38"
	s := &app{inputs: map[string]*string{}, configJSON: string(config), tasks: []task{{id: "task-a", title: "示例扩容适配", requirementTitle: "示例扩容适配：完整的需求标题与发版通知", path: filepath.Join(root, "tasks", "task-a"), createdAt: "task-created", requirementURL: "https://example.invalid/requirement/1"}}}
	s.snapshot.Home = filepath.Join(root, "home")
	s.snapshot.TagHistory = []application.TagRecord{
		{ID: "running", TaskID: "task-a", FolderName: "示例扩容适配", ServiceName: "transit-notification-service", Tag: "1.6.28.beta-4", State: "SUCCESS", CreatedAt: "2026-10-15 13:48:03", Updated: "2026-10-15 13:48:21", SourceBranch: "feature/example.user_1000000008_tns_scaleout", TargetBranch: "release/test", Detail: "transit-notification-service: 1.6.28.beta-4", GenbuStatus: domain.GenbuTagProbeStatus{Build: domain.GenbuBuilding, UAT: domain.GenbuInitial, CheckedAt: &checked}},
		{ID: "success", TaskID: "task-a", BatchID: "batch", FolderName: "示例扩容适配", ServiceName: "ios-transit-service", Tag: "3.0.35.beta-8", State: "SUCCESS", CreatedAt: "2026-10-14 16:46:19", Updated: "2026-10-15 13:48:23", SourceBranch: "feature/" + strings.Repeat("long-branch-name/", 12), TargetBranch: "release/" + strings.Repeat("target-branch/", 8), GenbuStatus: domain.GenbuTagProbeStatus{Build: domain.GenbuSuccess, UAT: domain.GenbuSuccess, CheckedAt: &checked, BuiltCompletedAt: &completed, ReleasedCompletedAt: &completed}},
		{ID: "failed", TaskID: "task-a", BatchID: "batch", FolderName: "示例扩容适配", ServiceName: "android-transit-service", Tag: "3.11.82.beta-1", State: "SUCCESS", CreatedAt: "2026-10-14 16:45:48", Updated: "2026-10-15 13:48:23", SourceBranch: "feature/example.user_1000000008_tns_scaleout", TargetBranch: "release/test", GenbuStatus: domain.GenbuTagProbeStatus{Build: domain.GenbuFailed, UAT: domain.GenbuUnknown, CheckedAt: &checked}},
		{ID: "other-task", TaskID: "task-b", ServiceName: "other-task-service", Tag: "other-tag", State: "SUCCESS"},
	}
	return s
}

func tagLayoutTester(s *app, width int, dark bool) *ui.Tester {
	s.dark = dark
	return ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, dark)
		ui.Column(c).Fill().Background(p.bg).Children(func() {
			ui.Row(c).FillWidth().Height(51).AlignItems(ui.Center).Children(func() {
				label(c, "Tag", 14, p.blue)
				ui.Spacer(c)
				s.renderTagHistoryPageMenu(c, p, s.currentTask())
			})
			s.renderTaskTagHistory(c, p, s.currentTask())
		})
	}, width, 780)
}

func TestTagHistoryCompactLayout(t *testing.T) {
	for _, width := range []int{1100, 480, 360} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s := tagLayoutFixture(t)
				before := append([]application.TagRecord(nil), s.snapshot.TagHistory...)
				tester := tagLayoutTester(s, width, dark)
				if tester.HasText("复制构建记录") {
					t.Fatal("individual copy controls remain in single/batch rows")
				}
				for _, row := range []struct{ id, build, uat string }{{"running", "构建 进行中", "UAT 未开始"}, {"success", "构建 成功", "UAT 成功"}, {"failed", "构建 失败", "UAT 未知"}} {
					build, buildOK := tester.Find(row.build)
					uat, uatOK := tester.Find(row.uat)
					branches, branchesOK := tester.Find("合并分支 · " + row.id)
					if !buildOK || !uatOK || !branchesOK || math.Abs(float64(build.Y-uat.Y)) > 1 || uat.X < build.X+build.W || uat.X+uat.W > float32(width-28) {
						t.Fatalf("status pair split or clipped: build=%+v uat=%+v branches=%+v", build, uat, branches)
					}
					cardID := "batch"
					if row.id == "running" {
						cardID = "running"
					}
					card, _ := tester.Find("Tag 批次 · " + cardID)
					if math.Abs(float64(card.X+card.W-13-uat.X-uat.W)) > 1 {
						t.Fatalf("status pair retained removed copy space: card=%+v uat=%+v", card, uat)
					}
					if width == 1100 && (build.X <= branches.X || build.Y > branches.Y) {
						t.Fatalf("wide status pair did not stay to the right: build=%+v branches=%+v", build, branches)
					}
					if width <= 360 && build.Y <= branches.Y+branches.H {
						t.Fatalf("narrow status group squeezed the main text instead of wrapping: build=%+v branches=%+v", build, branches)
					}
					if branches.X < 0 || branches.X+branches.W > float32(width-28) || branches.H > 20 {
						t.Fatalf("branch row escaped/tried to wrap: %+v", branches)
					}
					source, sourceOK := tester.Find("来源分支 · " + row.id)
					target, targetOK := tester.Find("目标分支 · " + row.id)
					if !sourceOK || !targetOK || source.W <= 0 || target.W <= 0 || target.X <= source.X+source.W || target.X+target.W > branches.X+branches.W+1 {
						t.Fatalf("source/target branch direction lost: source=%+v target=%+v", source, target)
					}
				}
				for _, removed := range []string{"创建于", "显示 1 /", "最近检查", "完成 ·", "构建成功", "other-task-service", "Tag 记录更多操作", "批次更多操作", "transit-notification-service:", "示例扩容适配 · 测试 Tag", "示例扩容适配 · 批量测试 Tag"} {
					if tester.HasText(removed) {
						t.Fatalf("redundant or misleading content remains: %s", removed)
					}
				}
				if !reflect.DeepEqual(before, s.snapshot.TagHistory) {
					t.Fatal("rendering changed persisted status/time data")
				}
				if directory := os.Getenv("SILVERWING_TAG_EVIDENCE_DIR"); directory != "" {
					if err := os.MkdirAll(directory, 0700); err != nil {
						t.Fatal(err)
					}
					file, err := os.Create(filepath.Join(directory, fmt.Sprintf("tags-%d-dark-%t.png", width, dark)))
					if err != nil {
						t.Fatal(err)
					}
					encodeErr := png.Encode(file, tester.Image())
					if closeErr := file.Close(); encodeErr != nil || closeErr != nil {
						t.Fatalf("capture failed: %v / %v", encodeErr, closeErr)
					}
				}
			})
		}
	}
}

func TestTagHistoryDirectCopyAndManagement(t *testing.T) {
	s := tagLayoutFixture(t)
	tester := tagLayoutTester(s, 900, false)
	if tester.HasText("复制构建记录") {
		t.Fatal("record copy controls must be removed from all rows")
	}
	if err := tester.Click("复制发版通知 · running"); err != nil {
		t.Fatal(err)
	}
	groups := filterTagHistory(s.taskTagHistoryRecords("task-a"), "", false)
	if tester.Clipboard() != s.tagHistoryAnnouncement(groups[0]) {
		t.Fatal("single operation announcement changed")
	}
	if err := tester.Click("复制发版通知 · batch"); err != nil {
		t.Fatal(err)
	}
	if tester.Clipboard() != s.tagHistoryAnnouncement(groups[1]) || !strings.Contains(tester.Clipboard(), "3.11.82.beta-1") {
		t.Fatal("batch copy lost a member or changed its existing order/value")
	}
	if err := tester.Click("Tag 历史更多操作"); err != nil {
		t.Fatal(err)
	}
	if !tester.HasText("刷新") || !tester.HasText("清空该任务历史") || !tester.HasText("选择批次") {
		t.Fatal("page management actions were lost")
	}
	if err := tester.Click("选择批次"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("选择批次 batch"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !s.tagHistory.Selected["batch"] || tester.HasText("选择记录 ") || tester.HasText("选择批次 other-task") || !tester.HasText("已选 1 批") {
		t.Fatal("selection identity/task scope changed")
	}
	if err := tester.Click("删除所选"); err != nil {
		t.Fatal(err)
	}
	if !s.tagHistory.DeleteOpen || !reflect.DeepEqual(s.tagHistory.DeleteIDs, []string{"failed", "success"}) || !tester.HasText("删除 1 批 Tag 历史") || !tester.HasText("确认删除") {
		t.Fatal("selection no longer requires the existing confirmation")
	}
	if err := tester.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if !s.tagHistory.Selected["batch"] || len(s.snapshot.TagHistory) != 4 {
		t.Fatal("cancel changed selection or records")
	}
}

func TestTagHistoryAnnouncementRequirementTitle(t *testing.T) {
	s := tagLayoutFixture(t)
	group := filterTagHistory(s.taskTagHistoryRecords("task-a"), "", false)[1]
	want := "需求：" + s.tasks[0].requirementTitle + "\n测试Tag已构建完毕，辛苦发版：\n\nandroid-transit-service · 3.11.82.beta-1\nios-transit-service · 3.0.35.beta-8"
	if got := s.tagHistoryAnnouncement(group); got != want || strings.Contains(got, "https://") || strings.Contains(got, `\n`) {
		t.Fatalf("notification title/newlines changed: %q", got)
	}
	loadedTitle := "已加载的完整需求标题，比短任务名更完整"
	s.taskRequirementMetadata = map[string]taskRequirementMetadataState{"task-a": {URL: s.tasks[0].requirementURL, Status: requirementMetadataLoaded, Metadata: requirementMetadata{Title: &loadedTitle}}}
	if got := s.tagHistoryAnnouncement(group); !strings.HasPrefix(got, "需求："+loadedTitle+"\n") {
		t.Fatalf("loaded full requirement title ignored: %q", got)
	}
	s.tasks[0].requirementURL = "https://example.invalid/changed-identity"
	s.tasks[0].requirementTitle = ""
	if got := s.tagHistoryAnnouncement(group); !strings.HasPrefix(got, "需求：示例扩容适配\n") {
		t.Fatalf("missing/wrong-identity metadata did not fall back to the task name: %q", got)
	}
}

func TestTagHistoryMenuUsesTaskTabRow(t *testing.T) {
	for _, width := range []int{640, 1100} {
		s := tagLayoutFixture(t)
		s.tab = taskTagTab
		tester := ui.NewTester(func(c *ui.Context) { s.taskWorkspaceDetail(c, setTheme(c, false)) }, width, 780)
		tabs, _ := tester.Find("任务内容页签")
		more, moreOK := tester.Find("Tag 历史更多操作")
		group, groupOK := tester.Find("Tag 批次 · running")
		header, headerOK := tester.Find("Tag 批次标题 · running")
		copy, copyOK := tester.Find("复制发版通知 · running")
		service, serviceOK := tester.Find("transit-notification-service")
		if !moreOK || !groupOK || !headerOK || !copyOK || !serviceOK || more.Y < tabs.Y || more.Y+more.H > tabs.Y+tabs.H || more.X+more.W > float32(width) || more.X < float32(width-65) || group.Y > tabs.Y+tabs.H+10 || service.Y < header.Y+header.H || copy.Y < header.Y || copy.Y+copy.H > header.Y+header.H+.5 {
			t.Fatalf("%d DIP: page menu or batch header ownership changed: tabs=%+v more=%+v group=%+v header=%+v copy=%+v service=%+v", width, tabs, more, group, header, copy, service)
		}
	}
}

func TestTagHistoryGroupHeaderUsesCreationTime(t *testing.T) {
	for _, width := range []int{360, 480, 1100} {
		for _, dark := range []bool{false, true} {
			s := tagLayoutFixture(t)
			for index := range s.snapshot.TagHistory {
				s.snapshot.TagHistory[index].FolderName = strings.Repeat("长组名与完整需求标题", 10)
				s.snapshot.TagHistory[index].Updated = "2030-01-01 01:02:03"
			}
			tester := tagLayoutTester(s, width, dark)
			for _, id := range []string{"running", "batch"} {
				header, headerOK := tester.Find("Tag 批次标题 · " + id)
				name, nameOK := tester.Find("Tag 批次名称 · " + id)
				date, dateOK := tester.Find("Tag 构建时间 · " + id)
				copy, copyOK := tester.Find("复制发版通知 · " + id)
				if !headerOK || !nameOK || !dateOK || !copyOK || name.H > 18 || name.W <= 0 || name.X+name.W > copy.X || copy.X+copy.W > float32(width-28) || copy.Y < header.Y || copy.Y+copy.H > header.Y+header.H+.5 || date.Y <= name.Y {
					t.Fatalf("%d dark=%t: group hierarchy/copy was clipped: header=%+v name=%+v date=%+v copy=%+v", width, dark, header, name, date, copy)
				}
			}
			groups := filterTagHistory(s.taskTagHistoryRecords("task-a"), "", false)
			if groups[1].Item.CreatedAt != "2026-10-14 16:45:48" || groups[1].Item.UpdatedAt != "2030-01-01 01:02:03" {
				t.Fatal("group creation time was replaced with last update/refresh time")
			}
		}
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	want := "构建于 " + time.Date(2026, 10, 15, 13, 48, 3, 0, location).Local().Format("2006-01-02 15:04")
	for _, input := range []string{"2026-10-15 13:48:03", "2026-10-15T05:48:03Z"} {
		if got := tagHistoryBuildTimeLabel(input); got != want {
			t.Fatalf("wrong local numeric creation label: %q", got)
		}
	}
	if tagHistoryBuildTimeLabel("unknown") != "构建时间未知" {
		t.Fatal("unknown creation time was replaced with a different timestamp")
	}
}

type tagLayoutCaptureBackend struct{ actions chan application.Action }

func (b *tagLayoutCaptureBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, nil
}
func (b *tagLayoutCaptureBackend) Dispatch(_ context.Context, action application.Action) (application.ActionResult, error) {
	b.actions <- action
	return application.ActionResult{}, nil
}

func TestTagHistoryGroupSelectionRequestAndStaleConfirmation(t *testing.T) {
	s := tagLayoutFixture(t)
	backend := &tagLayoutCaptureBackend{actions: make(chan application.Action, 1)}
	s.backend, s.backendGate = backend, make(chan struct{}, 1)
	tester := tagLayoutTester(s, 900, false)
	for _, click := range []string{"Tag 历史更多操作", "选择批次", "全选"} {
		if err := tester.Click(click); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.tagHistory.Selected) != 2 || !tester.HasText("已选 2 批") {
		t.Fatal("select-all counted records instead of complete current-task groups")
	}
	for _, click := range []string{"全选", "选择批次 batch", "删除所选", "确认删除"} {
		if err := tester.Click(click); err != nil {
			t.Fatal(err)
		}
	}
	var request application.Action
	select {
	case request = <-backend.actions:
	case <-time.After(time.Second):
		t.Fatal("confirmed whole-batch request did not reach memory backend")
	}
	if request.Kind != "tag.history.delete" || request.TaskID != "task-a" || !reflect.DeepEqual(request.Payload["operationIds"], []string{"failed", "success"}) || payloadString(request.Payload, "tagHistoryHome") != s.snapshot.Home || payloadString(request.Payload, "tagHistoryTaskPath") != s.tasks[0].path || payloadString(request.Payload, "tagHistoryTaskCreatedAt") != s.tasks[0].createdAt {
		t.Fatalf("request lost complete-group or task confirmation identity: %+v", request)
	}
	s.cancelOperation()
	s.busy = false
	s.beginTagHistoryDelete(false)
	s.snapshot.TagHistory = append(s.snapshot.TagHistory, application.TagRecord{ID: "new-member", TaskID: "task-a", BatchID: "batch", State: "SUCCESS"})
	s.ensureTagHistoryView()
	if s.tagHistory.DeleteOpen || s.tagHistory.Error == "" {
		t.Fatal("changed batch membership kept a stale partial deletion confirmation")
	}
	s.tasks = append(s.tasks, task{id: "task-b", path: filepath.Join(t.TempDir(), "task-b"), createdAt: "b-created"})
	s.selected = 1
	stateB := s.ensureTagHistoryView()
	stateB.Selected["other-task"] = true
	before := append([]application.TagRecord(nil), s.snapshot.TagHistory...)
	s.applyDeliveryActionResult(request, application.ActionResult{Data: application.TagHistoryMutationResult{Records: []application.TagRecord{}}})
	if !stateB.Selected["other-task"] || !reflect.DeepEqual(before, s.snapshot.TagHistory) {
		t.Fatal("late other-task deletion response changed current selection/history")
	}
}

func TestTagHistoryUnknownFailureAndTooltip(t *testing.T) {
	s := tagLayoutFixture(t)
	s.snapshot.TagHistory = s.snapshot.TagHistory[:1]
	record := &s.snapshot.TagHistory[0]
	publishedTag := record.Tag
	record.GenbuStatus = domain.GenbuTagProbeStatus{}
	tester := tagLayoutTester(s, 480, false)
	if !tester.HasText("构建 未知") || !tester.HasText("UAT 未知") || tester.HasText("构建成功") {
		t.Fatal("empty remote state inferred local Tag success")
	}
	record.State, record.Tag, record.Detail = "FAILED", "", "fixture: push rejected"
	failure := "fixture: Genbu unavailable"
	record.GenbuStatus.FailureReason = &failure
	tester.Frame()
	if !tester.HasText("Tag 创建失败") || !tester.HasText(record.Detail) || tester.HasText(failure) || !tester.HasText("尚未生成 Tag") || tester.HasText("构建 未知") || tester.HasText("UAT 未知") {
		t.Fatal("local failure was lost or a missing Tag displayed remote state")
	}
	if err := tester.Click("复制发版通知 · running"); err != nil {
		t.Fatal(err)
	}
	group := filterTagHistory(s.taskTagHistoryRecords("task-a"), "", false)[0]
	if tester.Clipboard() != s.tagHistoryAnnouncement(group) || tester.HasText("复制构建记录") {
		t.Fatal("remaining release notice copy lost failure information or retained record copy controls")
	}
	branches := record.SourceBranch + " → " + record.TargetBranch
	branchRect, _ := tester.Find("合并分支 · running")
	tester.Move(branchRect.X+branchRect.W/2, branchRect.Y+branchRect.H/2)
	time.Sleep(650 * time.Millisecond)
	tester.Frame()
	count := 0
	for _, text := range tester.Texts() {
		if text == branches {
			count++
		}
	}
	if count < 1 {
		t.Fatal("complete source and target branch tooltip missing")
	}
	record.State, record.Tag = "SUCCESS", publishedTag
	record.GenbuStatus = domain.GenbuTagProbeStatus{NotFound: true, Build: domain.GenbuSuccess, UAT: domain.GenbuSuccess}
	tester.Move(0, 0)
	tester.Frame()
	if !tester.HasText("未找到该 Tag") || !tester.HasText("构建 未知") || tester.HasText("构建 成功") {
		t.Fatal("not-found state showed stale success")
	}
}
