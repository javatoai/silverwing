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
	checked, completed := "2026-10-15 13:48:21", "2026-10-14 17:42:38"
	s := &app{
		taskUnit: taskController{state: taskState{tasks: []task{{id: "task-a", title: "示例扩容适配", requirementTitle: "示例扩容适配：完整的需求标题与发版通知", path: filepath.Join(root, "tasks", "task-a"), createdAt: "task-created", requirementURL: "https://example.invalid/requirement/1"}}}},
		inputs:   map[string]*string{},
	}
	s.snapshot.Home = filepath.Join(root, "home")
	seedPlatformConnectionsForDesktopTest(s, true, false)
	s.snapshot.TagHistory = []application.TagRecord{
		{ID: "running", TaskID: "task-a", FolderName: "示例扩容适配", ServiceName: "transit-notification-service", Tag: "1.6.28.beta-4", State: "SUCCESS", CreatedAt: "2026-10-15 13:48:03", Updated: "2026-10-15 13:48:21", SourceBranch: "feature/example.user_1000000008_tns_scaleout", TargetBranch: "release/test", Detail: "transit-notification-service: 1.6.28.beta-4", GenbuStatus: domain.GenbuTagProbeStatus{Build: domain.GenbuBuilding, UAT: domain.GenbuInitial, CheckedAt: &checked}},
		{ID: "success", TaskID: "task-a", BatchID: "batch", FolderName: "示例扩容适配", ServiceName: "ios-transit-service", Tag: "3.0.35.beta-8", State: "SUCCESS", CreatedAt: "2026-10-14 16:46:19", Updated: "2026-10-15 13:48:23", SourceBranch: "feature/" + strings.Repeat("long-branch-name/", 12), TargetBranch: "release/" + strings.Repeat("target-branch/", 8), GenbuStatus: domain.GenbuTagProbeStatus{Build: domain.GenbuSuccess, UAT: domain.GenbuSuccess, CheckedAt: &checked, BuiltCompletedAt: &completed, ReleasedCompletedAt: &completed, StoppedByNewerRelease: true}},
		{ID: "failed", TaskID: "task-a", BatchID: "batch", FolderName: "示例扩容适配", ServiceName: "android-transit-service", Tag: "3.11.82.beta-1", State: "SUCCESS", CreatedAt: "2026-10-14 16:45:48", Updated: "2026-10-15 13:48:23", SourceBranch: "feature/example.user_1000000008_tns_scaleout", TargetBranch: "release/test", GenbuStatus: domain.GenbuTagProbeStatus{Build: domain.GenbuFailed, UAT: domain.GenbuUnknown, CheckedAt: &checked}},
		{ID: "other-task", TaskID: "task-b", ServiceName: "other-task-service", Tag: "other-tag", State: "SUCCESS"},
	}
	services := make([]map[string]any, 0, len(s.snapshot.TagHistory))
	for i := range s.snapshot.TagHistory {
		record := &s.snapshot.TagHistory[i]
		record.ServiceID = record.ServiceName
		services = append(services, map[string]any{
			"id": record.ServiceID, "displayName": record.ServiceName, "enabled": true,
			"genbuProbeEnabled": true, "genbuServiceName": record.ServiceName,
			"repositoryId": "repo-" + record.ServiceID, "masterBranch": "origin/main",
			"testTagBaselineRef": "origin/release/test",
		})
	}
	config, err := json.Marshal(map[string]any{
		"taskRoot":     filepath.Join(root, "tasks"),
		"optionalCLIs": map[string]bool{"genbu": true, "lark": true},
		"services":     services,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.snapshot.ConfigJSON = string(config)
	s.settingsUnit.state.configJSON = string(config)
	if _, err := s.readSnapshotAppConfig(); err != nil {
		t.Fatalf("invalid Tag layout fixture configuration: %v", err)
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
			})
			s.renderTaskTagHistory(c, p, s.taskUnit.currentTask())
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
				for _, row := range []struct{ id, build, uat string }{
					{"running", "构建 进行中", "UAT 未开始"},
					{"success", "构建 成功", "UAT 成功"},
					{"failed", "构建 失败", "UAT 未知"},
				} {
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
					if !tagHistoryBoundsNear(uat.X+uat.W+2, card.X+card.W-13) {
						t.Fatalf("status group without a publish action must be right-aligned: card=%+v uat=%+v", card, uat)
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
				for _, removed := range []string{"创建于", "显示 1 /", "最近检查", "完成 ·", "构建成功", "other-task-service", "Tag 记录更多操作", "批次更多操作", "transit-notification-service:", "示例扩容适配 · 测试 Tag", "示例扩容适配 · 批量测试 Tag", "后续 Tag 已发布，停止自动探测"} {
					if tester.HasText(removed) {
						t.Fatalf("redundant or misleading content remains: %s", removed)
					}
				}
				if !reflect.DeepEqual(before, s.snapshot.TagHistory) {
					t.Fatal("rendering changed persisted status/time data")
				}
				captureTagHistoryEvidence(t, tester, fmt.Sprintf("tags-%d-dark-%t.png", width, dark))
			})
		}
	}
}

func captureTagHistoryEvidence(t *testing.T, tester *ui.Tester, name string) {
	t.Helper()
	directory := os.Getenv("SILVERWING_TAG_EVIDENCE_DIR")
	if directory == "" {
		return
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(directory, name))
	if err != nil {
		t.Fatal(err)
	}
	encodeErr := png.Encode(file, tester.Image())
	if closeErr := file.Close(); encodeErr != nil || closeErr != nil {
		t.Fatalf("capture failed: %v / %v", encodeErr, closeErr)
	}
}

func TestTagHistoryWideUATActionAlignment(t *testing.T) {
	for _, width := range []int{1060, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, _ := uatBatchDesktopFixture(t)
				first, second := s.snapshot.TagHistory[0], s.snapshot.TagHistory[1]
				first.FolderName, second.FolderName = "双服务测试发布", "双服务测试发布"
				first.GenbuStatus.UAT, second.GenbuStatus.UAT = domain.GenbuInitial, domain.GenbuSuccess
				single := first
				single.ID, single.BatchID, single.Tag, single.FolderName = "operation-19", "single-member-batch", "3.4.0.beta-9", "单服务测试发布"
				single.GenbuStatus.UAT = domain.GenbuBuilding
				ineligible := second
				ineligible.ID, ineligible.BatchID, ineligible.Tag, ineligible.FolderName = "operation-20", "", "3.4.1.beta-5", "构建失败记录"
				ineligible.GenbuStatus.Build, ineligible.GenbuStatus.UAT = domain.GenbuFailed, domain.GenbuUnknown
				s.snapshot.TagHistory = []application.TagRecord{first, second, single, ineligible}
				for i := range s.snapshot.TagHistory {
					s.snapshot.TagHistory[i].SourceBranch = "feature/uat-tag-activity"
					s.snapshot.TagHistory[i].TargetBranch = "release/test"
				}
				tester := tagLayoutTester(s, width, dark)
				tester.Frame()
				root := ui.Rect{W: float32(width), H: 780}
				batch := taskServicesBoundsWithin(t, tester, root, uatBatchReleaseLabel+" · "+uatTestBatchID)
				singleBatch := taskServicesBoundsWithin(t, tester, root, uatBatchReleaseLabel+" · "+single.BatchID)
				right := batch.X + batch.W
				if !tagHistoryBoundsNear(right, singleBatch.X+singleBatch.W) || batch.H != tagHistoryActionHeight || singleBatch.H != batch.H {
					t.Fatalf("batch header actions do not align: shared=%+v single=%+v", batch, singleBatch)
				}
				for _, pair := range []struct {
					id      string
					publish ui.Rect
				}{{uatTestBatchID, batch}, {single.BatchID, singleBatch}} {
					copy := taskServicesBoundsWithin(t, tester, root, "复制发版通知 · "+pair.id)
					if copy.X+copy.W >= pair.publish.X || !tagHistoryBoundsNear(copy.Y, pair.publish.Y) || copy.H != pair.publish.H {
						t.Fatalf("batch %s copy should sit immediately left of publish: copy=%+v publish=%+v", pair.id, copy, pair.publish)
					}
				}
				statusGroup := taskServicesBoundsWithin(t, tester, root, "构建与 UAT 状态")
				firstBuild := taskServicesBoundsWithin(t, tester, statusGroup, "构建 成功")
				firstStage := taskServicesBoundsWithin(t, tester, statusGroup, "UAT 未开始")
				firstRelease := taskServicesBoundsWithin(t, tester, root, tagHistoryPublishLabel+" · "+first.Tag)
				if firstBuild.X+firstBuild.W >= firstStage.X || statusGroup.X+statusGroup.W >= firstRelease.X ||
					statusGroup.H != firstRelease.H || !tagHistoryBoundsNear(statusGroup.Y, firstRelease.Y) {
					t.Fatalf("status pair and publish action must be separate and aligned: group=%+v build=%+v UAT=%+v publish=%+v", statusGroup, firstBuild, firstStage, firstRelease)
				}
				var stagePosition float32
				stageLabels := [...]string{"UAT 未开始", "UAT 成功", "UAT 发布中", "UAT 未知"}
				for i, record := range []application.TagRecord{first, second, single, ineligible} {
					stage := taskServicesBoundsWithin(t, tester, root, stageLabels[i])
					branches := taskServicesBoundsWithin(t, tester, root, "合并分支 · "+record.ID)
					if i == 0 {
						stagePosition = stage.X
					}
					if !tagHistoryBoundsNear(stage.X, stagePosition) || stage.H != firstStage.H || branches.Y < stage.Y+stage.H {
						t.Fatalf("wide status column shifted: row=%s stage=%+v branches=%+v", record.ID, stage, branches)
					}
					publication := tagHistoryPublishLabel + " · " + record.Tag
					if i == 3 {
						if _, found := tester.Find(publication); found {
							t.Fatal("a reserved release slot appeared as an action on a failed build")
						}
						continue
					}
					release := taskServicesBoundsWithin(t, tester, root, publication)
					if !tagHistoryBoundsNear(release.X+release.W, right) || !tagHistoryBoundsNear(release.Y+release.H/2, stage.Y+stage.H/2) ||
						release.H != statusGroup.H || stage.X+stage.W >= release.X {
						t.Fatalf("publish action is misaligned: release=%+v stage=%+v header=%+v", release, stage, batch)
					}
				}
				if tester.HasText("查看发布结果") || tester.HasText("构建日志") || tester.HasText("发布到 UAT") {
					t.Fatal("wide Tag history retained an old action or label")
				}
				captureTagHistoryEvidence(t, tester, fmt.Sprintf("tags-wide-uat-%d-dark-%t.png", width, dark))
			})
		}
	}
}

func tagHistoryBoundsNear(a, b float32) bool {
	return math.Abs(float64(a-b)) <= 1
}

func TestTagHistoryManagementWithReleaseNoticeCopy(t *testing.T) {
	s := tagLayoutFixture(t)
	tester := tagLayoutTester(s, 900, false)
	if !tester.HasText("复制发版通知") || tester.HasText("发送发版通知") || !tester.HasText("3.11.82.beta-1") {
		t.Fatal("Tag history should retain copy and individual Tags without a send action")
	}
	if err := tester.Click("选择批次"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("选择批次 batch"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !s.releaseUnit.state.tagHistory.Selected["batch"] || tester.HasText("选择记录 ") || tester.HasText("选择批次 other-task") || !tester.HasText("已选 1 批") {
		t.Fatal("selection identity/task scope changed")
	}
	if err := tester.Click("删除所选"); err != nil {
		t.Fatal(err)
	}
	if !s.releaseUnit.state.tagHistory.DeleteOpen || !reflect.DeepEqual(s.releaseUnit.state.tagHistory.DeleteIDs, []string{"failed", "success"}) || !tester.HasText("删除 1 批 Tag 历史") || !tester.HasText("确认删除") {
		t.Fatal("selection no longer requires the existing confirmation")
	}
	if err := tester.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if !s.releaseUnit.state.tagHistory.Selected["batch"] || len(s.snapshot.TagHistory) != 4 {
		t.Fatal("cancel changed selection or records")
	}
}

func TestTagHistoryCopiesCompleteFilteredBatchNotice(t *testing.T) {
	s := tagLayoutFixture(t)
	s.page, s.tab = "tasks", taskTagTab
	s.snapshot.TagHistory[2].GenbuStatus.Build = domain.GenbuSuccess
	s.snapshot.TagHistory[3].BatchID = "batch"
	title := "合成需求的完整标题"
	email := "qa@example.invalid"
	s.requirementUnit.state.taskRequirementMetadata = map[string]taskRequirementMetadataState{
		"task-a": {URL: s.taskUnit.currentTask().requirementURL, Status: requirementMetadataLoaded, Metadata: requirementMetadata{
			Title: &title,
			Participants: domain.RequirementParticipants{QCOwners: []domain.RequirementPerson{
				{Name: "测试甲"}, {Name: " 测试甲 "}, {Email: &email},
			}},
		}},
	}
	backend := &tagLayoutCaptureBackend{actions: make(chan application.Action, 1)}
	s.backend, s.backendGate = backend, make(chan struct{}, 1)
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		state := s.ensureTagHistoryView()
		for _, group := range filterTagHistory(s.taskTagHistoryRecords(state.TaskID), "ios-transit-service", false) {
			s.renderTagHistoryGroup(c.Key(group.Item.GroupID), p, state, group, false)
		}
		s.renderOperationNotifications(c)
	}, 900, 600)
	if !tester.HasText("ios-transit-service") || tester.HasText("android-transit-service") || tester.HasText("other-task-service") {
		t.Fatal("fixture must show only the matching service in this task")
	}
	if err := tester.Click("复制发版通知 · batch"); err != nil {
		t.Fatal(err)
	}
	want := "需求：" + title + "\n测试Tag已构建完毕，辛苦发版：\n\nandroid-transit-service · 3.11.82.beta-1\nios-transit-service · 3.0.35.beta-8"
	if got := tester.Clipboard(); got != want {
		t.Fatalf("clipboard should contain the complete selected batch, got %q", got)
	}
	feedback := "已复制：\n\n" + want + "\n\n请发送给：测试甲、" + email
	if !tester.HasText(feedback) || s.lastMessage != feedback || s.lastError != "" {
		t.Fatalf("copy feedback should include the cached test owners, got %q / %q", s.lastMessage, s.lastError)
	}
	select {
	case request := <-backend.actions:
		t.Fatalf("clipboard copy dispatched a business operation: %+v", request)
	default:
	}
}

func TestTagHistoryActionsFollowLastCard(t *testing.T) {
	for _, width := range []int{360, 640, 1100} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			s := tagLayoutFixture(t)
			s.page, s.tab = "tasks", taskTagTab
			tester := ui.NewTester(func(c *ui.Context) { s.taskWorkspaceDetail(c, setTheme(c, false)) }, width, 780)
			root := ui.Rect{W: float32(width), H: 780}
			tabs := taskServicesBoundsWithin(t, tester, root, "任务内容页签")
			first := taskServicesBoundsWithin(t, tester, root, "Tag 批次 · running")
			last := taskServicesBoundsWithin(t, tester, root, "Tag 批次 · batch")
			footer := taskServicesBoundsWithin(t, tester, root, "Tag 历史操作")
			if _, found := tester.Find("Tag 历史更多操作"); found {
				t.Fatal("task tab row still exposes the Tag history overflow")
			}
			if first.Y > tabs.Y+tabs.H+10 || first.Y+first.H > last.Y || footer.Y < last.Y+last.H {
				t.Fatalf("Tag history actions should follow the cards: tabs=%+v first=%+v last=%+v actions=%+v", tabs, first, last, footer)
			}
			refresh := taskServicesBoundsWithin(t, tester, footer, "刷新")
			selectBatch := taskServicesBoundsWithin(t, tester, footer, "选择批次")
			clear := taskServicesBoundsWithin(t, tester, footer, "清空该任务历史")
			if !tagHistoryBoundsNear(refresh.Y, selectBatch.Y) || !tagHistoryBoundsNear(selectBatch.Y, clear.Y) ||
				refresh.X+refresh.W > selectBatch.X || selectBatch.X+selectBatch.W > clear.X ||
				!tagHistoryBoundsNear(clear.X+clear.W, last.X+last.W) {
				t.Fatalf("Tag history actions should form a right-aligned row: refresh=%+v select=%+v clear=%+v card=%+v", refresh, selectBatch, clear, last)
			}
		})
	}
}

func TestTagHistoryFooterRefreshAndClearConfirmation(t *testing.T) {
	s := tagLayoutFixture(t)
	s.page, s.tab = "tasks", taskTagTab
	backend := &tagLayoutCaptureBackend{actions: make(chan application.Action, 1)}
	s.backend, s.backendGate = backend, make(chan struct{}, 1)
	tester := tagLayoutTester(s, 900, false)
	if err := tester.Click("刷新"); err != nil {
		t.Fatal(err)
	}
	probe := s.releaseUnit.state.tagGenbuAutoProbe
	if probe == nil || !probe.refreshPending || !probe.forcePending || probe.forceIdentity == "" || probe.forceIdentity != s.taskTagGenbuIdentity() {
		t.Fatal("footer refresh did not queue a refresh for the current task")
	}
	before := append([]application.TagRecord(nil), s.snapshot.TagHistory...)
	if err := tester.Click("清空该任务历史"); err != nil {
		t.Fatal(err)
	}
	state := s.releaseUnit.state.tagHistory
	if !state.DeleteOpen || !state.DeleteAll || !reflect.DeepEqual(state.DeleteIDs, []string{"failed", "running", "success"}) ||
		!tester.HasText("清空该任务的本地 Tag 历史") || !tester.HasText("确认删除") {
		t.Fatal("footer clear did not confirm the current task's complete history")
	}
	if err := tester.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if state.DeleteOpen || !reflect.DeepEqual(before, s.snapshot.TagHistory) {
		t.Fatal("cancel changed Tag history")
	}
	select {
	case request := <-backend.actions:
		t.Fatalf("clearing history reached the backend before confirmation: %+v", request)
	default:
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
				if !headerOK || !nameOK || !dateOK || name.H > 18 || name.W <= 0 || name.X+name.W > header.X+header.W+.5 || date.Y <= name.Y {
					t.Fatalf("%d dark=%t: group hierarchy was clipped: header=%+v name=%+v date=%+v", width, dark, header, name, date)
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
	for _, click := range []string{"选择批次", "全选"} {
		if err := tester.Click(click); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.releaseUnit.state.tagHistory.Selected) != 2 || !tester.HasText("已选 2 批") {
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
	if request.Kind != "tag.history.delete" || request.TaskID != "task-a" || !reflect.DeepEqual(request.Payload["operationIds"], []string{"failed", "success"}) || payloadString(request.Payload, "tagHistoryHome") != s.snapshot.Home || payloadString(request.Payload, "tagHistoryTaskPath") != s.taskUnit.state.tasks[0].path || payloadString(request.Payload, "tagHistoryTaskCreatedAt") != s.taskUnit.state.tasks[0].createdAt {
		t.Fatalf("request lost complete-group or task confirmation identity: %+v", request)
	}
	s.cancelOperation()
	s.busy = false
	s.beginTagHistoryDelete(false)
	s.snapshot.TagHistory = append(s.snapshot.TagHistory, application.TagRecord{ID: "new-member", TaskID: "task-a", BatchID: "batch", State: "SUCCESS"})
	s.ensureTagHistoryView()
	if s.releaseUnit.state.tagHistory.DeleteOpen || s.releaseUnit.state.tagHistory.Error == "" {
		t.Fatal("changed batch membership kept a stale partial deletion confirmation")
	}
	s.taskUnit.state.tasks = append(s.taskUnit.state.tasks, task{id: "task-b", path: filepath.Join(t.TempDir(), "task-b"), createdAt: "b-created"})
	s.taskUnit.state.selected = 1
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
	branches := record.SourceBranch + " → " + record.TargetBranch
	branchRect, _ := tester.Find("合并分支 · running")
	tester.Move(branchRect.X+branchRect.W/2, branchRect.Y+branchRect.H/2)
	tester.Advance(650 * time.Millisecond)
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
