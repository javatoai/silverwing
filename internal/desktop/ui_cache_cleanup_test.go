package desktop

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func TestTaskRequirementURLCopiesFromBothDetails(t *testing.T) {
	for _, suffix := range []string{"", "?detail=" + strings.Repeat("long", 160)} {
		t.Run(map[bool]string{true: "long", false: "short"}[suffix != ""], func(t *testing.T) {
			url := "https://project.feishu.cn/obt/bug/detail/1000000007" + suffix
			s := &app{
				taskUnit:     taskController{state: taskState{tasks: []task{{id: "task", path: `C:\synthetic\task`, groupID: "g", requirementID: "1000000007", requirementURL: url}}}},
				settingsUnit: settingsController{state: settingsState{configJSON: `{}`}},
				page:         "tasks",
				snapshot:     application.Snapshot{Groups: []application.Group{{ID: "g", Name: "无需重复组名"}, {ID: "other", Name: "其它组"}}},
			}
			tester := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Padding(16).Children(func() { s.taskHeader(c, setTheme(c, false)) })
			}, 960, 760)
			headerCopy, foundHeaderCopy := tester.Find("复制需求链接")
			if !tester.HasText(url) || !foundHeaderCopy || headerCopy.X < 16 || headerCopy.X+headerCopy.W > 944 {
				t.Fatalf("task header URL or copy is missing or clipped: %#v", headerCopy)
			}
			if err := tester.Click("复制需求链接"); err != nil || tester.Clipboard() != url {
				t.Fatalf("task header URL copy changed: err=%v clipboard=%q", err, tester.Clipboard())
			}
			if tester.HasText("复制需求编号 1000000007") {
				t.Fatal("task header restored an unrequested requirement ID control")
			}
			if tester.HasText("无需重复组名") {
				t.Fatal("task header repeats the group")
			}
			reader := s.requirementReader(s.taskUnit.currentTask().id, url)
			reader.bodyLoading, reader.commentsLoading = true, true
			t.Cleanup(s.requirementUnit.closeRequirementReaders)
			detail := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Padding(16).Children(func() { s.requirementDetails(c, setTheme(c, false)) })
			}, 960, 760)
			copyURL, foundCopy := detail.Find("复制需求链接")
			if !foundCopy || copyURL.X < 16 || copyURL.X+copyURL.W > 944 {
				t.Fatalf("requirement detail URL copy is missing or clipped: %#v", copyURL)
			}
			if err := detail.Click("复制需求链接"); err != nil {
				t.Fatal(err)
			}
			if detail.Clipboard() != url {
				t.Fatal("requirement detail URL copy was truncated")
			}
		})
	}
}

func TestRequirementShortContentUsesAvailableReaderWidth(t *testing.T) {
	for _, width := range []int{960, 1440} {
		s := &app{}
		reader := s.requirementReader("", "https://project.feishu.cn/obt/bug/detail/123")
		s.updateRequirementReaderBody(reader, application.RequirementContent{Body: "简短正文。", Complete: true})
		s.updateRequirementReaderComments(reader, application.RequirementComments{Comments: []domain.RequirementComment{{ID: "comment", AuthorName: "测试成员", Content: "简短评论。"}}})
		tester := ui.NewTester(func(c *ui.Context) {
			ui.Column(c).Fill().Children(func() { s.renderRequirementReadonlyMarkdownReader(c, setTheme(c, false), reader) })
		}, width, 760)
		card, found := tester.Find("评论正文 测试成员")
		outer, outerFound := tester.Find("评论卡片 id:comment")
		if !found || !outerFound || outer.W < float32(width-64) || card.W < outer.W-36 || card.X+card.W > float32(width) {
			t.Fatalf("short content collapsed the reading width at %d: %#v", width, card)
		}
		s.requirementUnit.closeRequirementReaders()
	}
}

func TestRequirementFindToolbarStaysCompact(t *testing.T) {
	for _, width := range []int{400, 960} {
		s := &app{}
		reader := s.requirementReader("", "https://project.feishu.cn/obt/bug/detail/123")
		s.updateRequirementReaderBody(reader, application.RequirementContent{Body: "简短正文。", Complete: true})
		s.updateRequirementReaderComments(reader, application.RequirementComments{})
		ensureRequirementReaderController(reader).findOpen = true
		tester := ui.NewTester(func(c *ui.Context) {
			ui.Column(c).Fill().Children(func() { s.renderRequirementReadonlyMarkdownReader(c, setTheme(c, false), reader) })
		}, width, 760)
		input, found := tester.Find("查找正文和评论")
		close, closeFound := tester.Find("关闭查找")
		if !found || !closeFound || input.H > 32 || input.Y+input.H > 50 || close.X+close.W > float32(width) || close.Y < input.Y-2 || close.Y > input.Y+2 {
			t.Fatalf("find toolbar expanded or clipped at %d: input=%#v close=%#v", width, input, close)
		}
		s.requirementUnit.closeRequirementReaders()
	}
}

func TestRepositoryRowRemovalUsesMenuAndConfirmation(t *testing.T) {
	backend := &transientFormBackend{actions: make(chan application.Action, 1)}
	s := &app{
		settingsUnit: settingsController{state: settingsState{configJSON: `{"services":[{"id":"s","displayName":"api-service","enabled":false}]}`}},
		page:         "repositories",
		inputs:       make(map[string]*string),
		snapshot:     application.Snapshot{Services: []application.Service{{ID: "s", Name: "api-service", RepositoryID: "repo", MasterBranch: "origin/main", Modules: []application.Module{{ID: "m"}}}}, Repositories: []application.Repository{{ID: "repo", RootPath: `C:\synthetic\repo`}}},
		win:          &mygo.Window{},
		backend:      backend,
		backendGate:  make(chan struct{}, 1),
	}
	t.Cleanup(func() {
		if s.cancelOperation != nil {
			s.cancelOperation()
		}
	})
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		s.beginOverflowMenuFrame()
		defer s.endOverflowMenuFrame()
		ui.Column(c).Fill().Padding(12).Children(func() { s.serviceList(c, p) })
		s.renderConfirmationDialog(c, p)
	}, 1200, 800)
	if tester.HasText("主分支 · main") {
		t.Fatal("repository row still repeats mainline config")
	}
	if _, found := tester.Find("仓库操作"); found {
		t.Fatal("single-repository list has unnecessary global actions")
	}
	badge, found := tester.Find("已停用")
	if !found {
		t.Fatal("disabled repository status is missing")
	}
	const menuName = "查看更多 api-service"
	for _, name := range []string{"配置仓库 api-service", menuName} {
		button, found := tester.Find(name)
		offset := button.Y + button.H/2 - (badge.Y + badge.H/2)
		if !found || offset < -1 || offset > 1 || badge.X+badge.W > button.X {
			t.Fatalf("disabled status must align with %s: status=%+v action=%+v", name, badge, button)
		}
	}
	if tester.HasText("移除") {
		t.Fatal("remove action still appears directly on the row")
	}
	openConfirmation := func() {
		t.Helper()
		if err := tester.Click(menuName); err != nil {
			t.Fatal(err)
		}
		if err := tester.Click("移除"); err != nil {
			t.Fatal(err)
		}
		if s.confirmationDialog == nil || s.confirmationDialog.Title != "移除仓库？" ||
			s.confirmationDialog.Detail != "api-service" || s.busy {
			t.Fatal("removing from the menu did not prompt for the active repository")
		}
	}
	openConfirmation()
	if err := tester.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if s.confirmationDialog != nil || s.busy || len(backend.actions) != 0 {
		t.Fatal("canceling confirmation dispatched a remove action")
	}
	openConfirmation()
	if err := tester.Click("继续"); err != nil {
		t.Fatal(err)
	}
	select {
	case action := <-backend.actions:
		if action.Kind != "service.remove" || action.ServiceID != "s" {
			t.Fatalf("unexpected operation after confirming removal: %+v", action)
		}
	case <-time.After(time.Second):
		t.Fatal("confirmed removal did not dispatch")
	}
}
