package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func TestTaskRequirementURLCopiesFromBothDetails(t *testing.T) {
	for _, suffix := range []string{"", "?detail=" + strings.Repeat("long", 160)} {
		t.Run(map[bool]string{true: "long", false: "short"}[suffix != ""], func(t *testing.T) {
			url := "https://project.feishu.cn/obt/bug/detail/1000000007" + suffix
			s := &app{page: "tasks", configJSON: `{}`, tasks: []task{{id: "task", path: `C:\synthetic\task`, groupID: "g", requirementID: "1000000007", requirementURL: url}},
				snapshot: application.Snapshot{Groups: []application.Group{{ID: "g", Name: "无需重复组名"}, {ID: "other", Name: "其它组"}}}}
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
			reader := s.requirementReader(s.currentTask().id, url)
			reader.bodyLoading, reader.commentsLoading = true, true
			t.Cleanup(s.closeRequirementReaders)
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
		s.closeRequirementReaders()
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
		s.closeRequirementReaders()
	}
}

func TestRepositoryRowHasQuietDirectRemovalEntry(t *testing.T) {
	s := &app{page: "repositories", inputs: make(map[string]*string), configJSON: `{"groups":[{"id":"g","name":"国内组","services":[{"id":"s","displayName":"api-service","enabled":true}]}]}`,
		snapshot: application.Snapshot{Groups: []application.Group{{ID: "g", Name: "国内组"}}, Services: []application.Service{{ID: "s", GroupID: "g", Name: "api-service", RepositoryID: "repo", MasterBranch: "origin/main", Modules: []application.Module{{ID: "m"}}}}, Repositories: []application.Repository{{ID: "repo", RootPath: `C:\synthetic\repo`}}}}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(12).Children(func() { s.serviceList(c, setTheme(c, false)) })
	}, 1200, 800)
	if tester.HasText("主分支 · main") {
		t.Fatal("repository row still repeats mainline config")
	}
	if _, found := tester.Find("仓库操作"); found {
		t.Fatal("one-item row overflow remains")
	}
	if err := tester.Click("从当前组移除仓库 api-service"); err != nil {
		t.Fatal(err)
	}
	// A Tester has no OS window: the existing confirmation guard cannot open
	// its dialog, so the click must not dispatch the destructive action.
	if s.busy {
		t.Fatal("removal proceeded without a confirmation window")
	}
}
