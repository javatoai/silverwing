package desktop

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func TestRequirementCommentCardsPageLayoutsAndScroll(test *testing.T) {
	for _, page := range []string{"tasks", "requirements"} {
		for _, size := range [][2]int{{960, 760}, {1440, 940}} {
			for _, dark := range []bool{false, true} {
				name := fmt.Sprintf("%s-%d-dark-%t", page, size[0], dark)
				test.Run(name, func(test *testing.T) {
					state := newApp(nil, Options{CapturePath: "isolated-comment-cards-page"})
					state.loading, state.page, state.dark, state.tab = false, page, dark, 2
					state.snapshot.Home = test.TempDir()
					state.configJSON = `{"meegleProjects":[{"project_key":"fixture","simple_name":"本地示例"}]}`
					item := application.Requirement{
						ID: "1001", Title: "需求讨论与验收 · 本地示例", URL: "https://example.invalid/requirements/1001", ProjectKey: "fixture",
						WorkItem: domain.ParticipatedWorkItem{ID: "1001", ProjectKey: "fixture", Type: "userstory"},
					}
					state.requirements, state.requirementsLoaded, state.requirementsRequested = []application.Requirement{item}, true, true
					state.selectedRequirementKey = requirementKey(item)
					state.tasks = []task{{id: "comment-card-task", title: "讨论展示优化", requirementTitle: item.Title, requirementURL: item.URL, status: "进行中"}}
					taskID := ""
					if page == "tasks" {
						taskID = state.tasks[0].id
					}
					reader := state.requirementReader(taskID, item.URL)
					test.Cleanup(state.closeRequirementReaders)
					state.updateRequirementReaderBody(reader, application.RequirementContent{Body: "## 需求正文\n\n保留现有阅读体验，讨论内容按原顺序全部展开。", Complete: true})
					parentID := "opening"
					attachmentURL := "https://example.invalid/files/acceptance.pdf"
					state.updateRequirementReaderComments(reader, application.RequirementComments{Comments: []domain.RequirementComment{
						{ID: parentID, AuthorName: "王震", CreatedAt: "2026-10-19 09:30:00", Content: "需求已提测，请检查 **边界与间距**。"},
						{ID: "reply", AuthorName: "测试同事", CreatedAt: "2026-10-19 09:35:00", ParentID: &parentID, Content: "收到，会覆盖窄屏与长内容。"},
						{ID: "attachment", AuthorName: "产品同事", CreatedAt: "2026-10-19 09:40:00", AttachmentURL: &attachmentURL},
						{ID: "long", AuthorName: "开发同事", CreatedAt: "2026-10-19 10:00:00", Content: strings.Repeat("长评论仍然全部展开，与正文共用纵向滚动。\n\n", 24) + "评论末尾定位标记"},
					}})
					tester := ui.NewTester(func(context *ui.Context) {
						theme := setTheme(context, dark)
						ui.Row(context).Fill().AlignItems(ui.Stretch).Background(theme.bg).Children(func() {
							state.sidebar(context, theme, size[0] < 1180)
							ui.Column(context).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
								if page == "tasks" {
									state.taskWorkspace(context, theme)
								} else {
									state.replicaRequirements(context, theme)
								}
							})
						})
						state.bindVisibleRequirementReaderShortcuts(context)
					}, size[0], size[1])
					card, found := tester.Find("评论卡片 id:opening")
					if !found || card.X < 0 || card.Y < 0 || card.X+card.W > float32(size[0]) || card.Y+card.H > float32(size[1]) {
						test.Fatalf("first comment card is not reachable within the page: %+v", card)
					}
					if !tester.HasText("回复 王震") || !tester.HasText("查看评论附件") || !tester.HasText("（无文字内容）") {
						test.Fatal("page lost reply, attachment or empty-content presentation")
					}
					if err := tester.Click("复制需求链接"); err != nil || tester.Clipboard() != item.URL {
						test.Fatalf("page URL copy changed: %v, %q", err, tester.Clipboard())
					}
					saveFrameworkReadingFixture(test, tester, "comment-cards-page-"+name)
					controller := state.prepareRequirementMarkdownReader(reader)
					controller.findOpen = true
					controller.find.set(requirementReaderFindIdentity(reader, false), "评论末尾定位标记", requirementFindBlocksForReader(reader, false))
					controller.find.navigation++
					tester.Frame()
					last, found := tester.Find("评论末尾定位标记")
					viewport := controller.find.viewport
					if !found || readonlyMarkdownReaderScroll(controller, false).Y <= 0 || last.Y < viewport.Y || last.Y+last.H > viewport.Y+viewport.H {
						test.Fatalf("last expanded comment cannot be revealed in shared scroll: last=%+v viewport=%+v", last, viewport)
					}
					for _, document := range reader.commentMarkdown {
						if document.readonlyReader.externalScroll != readonlyMarkdownReaderScroll(controller, false) {
							test.Fatal("comment acquired an independent vertical scroll")
						}
					}
					if len(tester.OpenedURLs()) != 0 {
						test.Fatal("local page validation opened an external URL")
					}
				})
			}
		}
	}
}

func TestRequirementCommentCardsKeyboardFindAcrossDocuments(test *testing.T) {
	for _, width := range []int{360, 960} {
		test.Run(fmt.Sprint(width), func(test *testing.T) {
			state := newApp(nil, Options{CapturePath: "isolated-comment-cards-keyboard"})
			state.loading, state.page = false, "requirements"
			state.snapshot.Home = test.TempDir()
			const link = "https://example.invalid/requirements/keyboard"
			reader := state.requirementReader("", link)
			test.Cleanup(state.closeRequirementReaders)
			state.updateRequirementReaderBody(reader, application.RequirementContent{Body: "正文 needle", Complete: true})
			state.updateRequirementReaderComments(reader, application.RequirementComments{Comments: []domain.RequirementComment{
				{ID: "first", AuthorName: "第一作者", Content: strings.Repeat("第一条评论保持展开。\n\n", 30) + "第一评论 needle"},
				{ID: "second", AuthorName: "第二作者", Content: strings.Repeat("第二条评论保持展开。\n\n", 24) + "第二评论 needle"},
			}})
			controller := state.prepareRequirementMarkdownReader(reader)
			controller.findOpen, controller.findDraft = true, "needle"
			controller.find.set(requirementReaderFindIdentity(reader, false), "needle", requirementFindBlocksForReader(reader, false))
			tester := ui.NewTester(func(context *ui.Context) {
				ui.Column(context).Fill().Children(func() {
					state.renderRequirementContent(context, setTheme(context, false), "", link)
				})
				state.bindVisibleRequirementReaderShortcuts(context)
			}, width, 480)
			assertVisibleHit := func(section, text string) {
				test.Helper()
				hit, found := controller.find.current()
				if !found || !strings.HasPrefix(hit.block, section+"\x00") {
					test.Fatalf("keyboard find should enter %q, got %+v", section, hit)
				}
				bounds, found := tester.Find(text)
				viewport := controller.find.viewport
				if !found || bounds.Y < viewport.Y || bounds.Y+bounds.H > viewport.Y+viewport.H {
					test.Fatalf("keyboard find did not reveal %q: bounds=%+v viewport=%+v", text, bounds, viewport)
				}
			}
			assertVisibleHit("body", "正文 needle")
			tester.Key(0, ui.KeyF3)
			assertVisibleHit("comment:id:first", "第一评论 needle")
			tester.Key(0, ui.KeyF3)
			assertVisibleHit("comment:id:second", "第二评论 needle")
			tester.Key(ui.Shift, ui.KeyF3)
			assertVisibleHit("comment:id:first", "第一评论 needle")
			tester.Key(ui.Shift, ui.KeyF3)
			assertVisibleHit("body", "正文 needle")
		})
	}
}
