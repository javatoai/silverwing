package desktop

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func taskCommentOptimizationVisualFixture(t *testing.T) (*app, *requirementReaderState) {
	t.Helper()
	state := taskNavigationFixture()
	state.loading, state.page, state.selected = false, "tasks", 0
	state.snapshot.Home = t.TempDir()
	state.configJSON = `{"meegleProjects":[{"project_key":"visual-fixture","simple_name":"本地示例"}]}`
	requirementURL := "https://project.feishu.cn/visual-fixture/technical/detail/9900001"
	requirementTitle := "示例资源加载（虚构测试）"
	state.tasks[0].requirementURL = requirementURL
	state.tasks[0].requirementTitle = requirementTitle
	state.taskRequirementMetadata[state.tasks[0].id] = taskRequirementMetadataState{
		URL: requirementURL, Status: requirementMetadataLoaded,
		Metadata: requirementMetadata{Title: &requirementTitle},
	}

	workspace := application.Workspace{
		ID: "visual-workspace", ServiceID: "visual-job-manager", ModuleID: "visual-worker",
		Path: `C:\synthetic\silverwing\job-manager`, Branch: "feature/visual-fixture", State: "READY",
	}
	state.tasks[0].workspaces = []application.Workspace{workspace}
	state.snapshot.Services = []application.Service{{
		ID: workspace.ServiceID, Name: "job-manager", GroupID: state.tasks[0].groupID, MasterBranch: "main",
		Modules: []application.Module{{ID: workspace.ModuleID, Name: "worker", MasterBranch: "main"}},
	}}
	actualBranch := workspace.Branch
	state.workspaceGit = &workspaceGitState{health: map[string]domain.WorkspaceGitHealth{
		workspaceHealthPathKey(workspace.Path): {
			State: domain.WorkspaceGitReady, DirtyFileCount: 2, PushState: domain.LocalPushPushed, ActualBranch: &actualBranch,
		},
	}}
	state.setTaskPinned(state.tasks[2], true)
	state.setTaskPinned(state.tasks[1], true)

	reader := state.requirementReader(state.tasks[0].id, requirementURL)
	state.updateRequirementReaderBody(reader, application.RequirementContent{
		Body: "## 需求正文\n\n使用本地虚构数据检查评论阅读和任务页面布局。", Complete: true,
	})
	parentID := "comment-html-list"
	attachmentURL := "https://example.invalid/files/fixture-attachment.pdf"
	state.updateRequirementReaderComments(reader, application.RequirementComments{Comments: []domain.RequirementComment{
		{
			ID: parentID, AuthorName: "演示用户甲", CreatedAt: "2025-04-01 09:30:00",
			Content: "虚构文件说明：\n\n1. <span style=\"background-color: rgb(255, 255, 255)\"><span style=\"font-size: 14px\"><span style=\"color: #000000\">demo-public-key.crt</span></span></span>\n\n- 检查 **示例配置** 与权限。",
		},
		{
			ID: "comment-reply", AuthorName: "演示用户乙", CreatedAt: "2025-04-01 09:35:00",
			ParentID: &parentID, Content: "- 回复列表第一项\n- 回复列表第二项",
		},
		{ID: "comment-attachment", AuthorName: "演示用户丙", CreatedAt: "2025-04-01 09:40:00", AttachmentURL: &attachmentURL},
	}})
	t.Cleanup(state.closeRequirementReaders)
	return state, reader
}

func assertTaskCommentOptimizationFixture(t *testing.T, tester *ui.Tester) {
	t.Helper()
	for _, visible := range []string{"评论卡片 id:comment-html-list", "demo-public-key.crt", "回复 演示用户甲", "查看评论附件"} {
		if !tester.HasText(visible) {
			t.Errorf("fixture is missing %q", visible)
		}
	}
	visibleText := strings.Join(tester.Texts(), "\n")
	if strings.Contains(visibleText, "<span") || strings.Contains(visibleText, "style=") {
		t.Errorf("comment HTML is shown as source text: %q", visibleText)
	}
}

func TestTaskCommentOptimizationVisualFixtures(t *testing.T) {
	for _, width := range []int{360, 640, 960} {
		for _, dark := range []bool{false, true} {
			name := fmt.Sprintf("task-comment-reader-%d-dark-%t", width, dark)
			t.Run(name, func(t *testing.T) {
				state, reader := taskCommentOptimizationVisualFixture(t)
				tester := ui.NewTester(func(context *ui.Context) {
					theme := setTheme(context, dark)
					state.renderRequirementReadonlyMarkdownReader(context, theme, reader)
				}, width, 1280)
				tester.SetLocale("zh-Hans")
				if width == 640 && dark {
					tester.SetScale(1.5)
					tester.Frame()
					if got, want := tester.Image().Bounds().Dx(), int(float32(width)*1.5); got != want {
						t.Fatalf("scaled fixture width=%d, want %d", got, want)
					}
				}
				assertTaskCommentOptimizationFixture(t, tester)
				saveFrameworkReadingFixture(t, tester, name)
			})
		}
	}

	for _, size := range [][2]int{{960, 760}, {1440, 940}} {
		for _, tab := range []int{0, 2} {
			name := fmt.Sprintf("task-comment-page-%dx%d-tab-%d", size[0], size[1], tab)
			t.Run(name, func(t *testing.T) {
				state, _ := taskCommentOptimizationVisualFixture(t)
				state.tab = tab
				dark := size[0] == 1440
				tester := ui.NewTester(func(context *ui.Context) {
					theme := setTheme(context, dark)
					ui.Row(context).Fill().AlignItems(ui.Stretch).Background(theme.bg).Children(func() {
						state.sidebar(context, theme, size[0] < 1180)
						ui.Column(context).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
							state.taskWorkspace(context, theme)
						})
					})
					state.bindVisibleRequirementReaderShortcuts(context)
				}, size[0], size[1])
				tester.SetLocale("zh-Hans")
				if !tester.HasText("搜索任务") || tester.HasText("最近访问") {
					t.Fatal("task page should show search without the removed recent-visit mode")
				}
				if tab == 0 {
					if !tester.HasText("本地未提交 2") || !tester.HasText("提交历史") || !tester.HasText("已推送") {
						t.Fatal("synthetic repository health actions are missing from the workspace card")
					}
					if tester.HasText("整体差异") {
						t.Fatal("workspace card still exposes the removed overall-diff entry")
					}
					list := taskNavigationTester(state, 360, 520)
					if list.HasText("全部") || list.HasText("最近访问") {
						t.Fatal("task list still exposes a removed view mode")
					}
					if got, want := state.taskIndexRows(false), []int{1, 2, 0, 4}; !slices.Equal(got, want) {
						t.Fatalf("fixture pin order=%v, want %v", got, want)
					}
				} else {
					assertTaskCommentOptimizationFixture(t, tester)
				}
				if len(tester.OpenedURLs()) != 0 {
					t.Fatal("visual fixture opened an external URL")
				}
				saveFrameworkReadingFixture(t, tester, name)
			})
		}
	}
}
