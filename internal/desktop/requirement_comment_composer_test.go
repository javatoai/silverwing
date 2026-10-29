package desktop

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func commentComposerFixture(t *testing.T) (*app, *taskOpenToolsProbeBackend, chan func(), *requirementCommentComposer) {
	t.Helper()
	updates := make(chan func(), 64)
	b := &taskOpenToolsProbeBackend{calls: make(chan *taskOpenToolsProbeCall, 32)}
	s := &app{
		settingsUnit: settingsController{state: settingsState{configJSON: `{}`}},
		backend:      b,
		snapshot:     application.Snapshot{Home: t.TempDir()},
		page:         "requirements",
	}
	s.requirementUnit.state.commentComposers = &requirementCommentComposers{update: func(f func()) { updates <- f }}
	s.openRequirementCommentComposer(&requirementReaderState{url: "https://project.feishu.cn/fixture/userstory/detail/101"})
	state := s.requirementUnit.state.commentComposers.active
	call := nextCommentComposerCall(t, b)
	if call.action.Kind != "requirements.comment-draft" || call.action.Payload["operation"] != "load" {
		t.Fatal(call.action)
	}
	draft := domain.RequirementCommentDraft{ID: strings.Repeat("a", 32), Identity: "account-1", Account: "测试账号", Scope: "scope-1", Revision: 1, Item: domain.ParticipatedWorkItem{ProjectKey: "p", ProjectName: "fixture", Type: "userstory", ID: "101", Title: "目标需求", URL: state.url}, Images: []domain.CommentDraftAsset{}}
	call.reply <- taskOpenToolsProbeReply{result: application.ActionResult{Data: draft}}
	nextTaskOpenToolsUpdate(t, updates)()
	t.Cleanup(func() {
		for _, c := range s.requirementUnit.state.commentComposers.all {
			if c.cancel != nil {
				c.cancel()
			}
		}
	})
	return s, b, updates, state
}

func nextCommentComposerCall(t *testing.T, b *taskOpenToolsProbeBackend) *taskOpenToolsProbeCall {
	t.Helper()
	select {
	case c := <-b.calls:
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("comment job did not start")
		return nil
	}
}

func TestCommentComposerSharedDraftAutosaveAndSaveFailure(t *testing.T) {
	s, b, updates, state := commentComposerFixture(t)
	s.openRequirementCommentComposer(&requirementReaderState{taskID: "different-entry", url: state.url})
	if s.requirementUnit.state.commentComposers.active != state || b.count.Load() != 1 {
		t.Fatal("second entry did not share the draft")
	}
	state.body = "first"
	state.dirty = true
	s.startCommentComposerJob(state, "save", nil)
	first := nextCommentComposerCall(t, b)
	state.body = "newer input"
	state.dirty = true
	draft := state.draft
	draft.Content = "first"
	draft.Revision++
	first.reply <- taskOpenToolsProbeReply{result: application.ActionResult{Data: draft}}
	nextTaskOpenToolsUpdate(t, updates)()
	second := nextCommentComposerCall(t, b)
	if state.body != "newer input" || second.action.Payload["content"] != "newer input" || second.action.Payload["revision"] != uint64(2) {
		t.Fatal("late autosave replaced newer text")
	}
	second.reply <- taskOpenToolsProbeReply{err: errors.New("disk full")}
	nextTaskOpenToolsUpdate(t, updates)()
	s.observeRequirementCommentComposers()
	if state.body != "newer input" || !state.dirty || state.errorText != "disk full" || state.running || s.commentComposerCloseError() == nil {
		t.Fatal("failed autosave did not protect the draft")
	}
}

func TestCommentComposerPublishMinimizeSwitchAndLateResults(t *testing.T) {
	for _, change := range []string{"minimize", "requirement", "account", "home", "config", "success"} {
		t.Run(change, func(t *testing.T) {
			s, b, updates, state := commentComposerFixture(t)
			state.body = "snapshot text"
			state.draft.Content = state.body
			s.startCommentComposerJob(state, "publish", nil)
			call := nextCommentComposerCall(t, b)
			if call.action.Kind != "requirements.comments/create" || call.action.TaskID != "" || call.action.Payload["expectedIdentity"] != "account-1" {
				t.Fatal(call.action)
			}
			s.startCommentComposerJob(state, "publish", nil)
			if b.count.Load() != 2 {
				t.Fatal("duplicate click started a second create")
			}
			switch change {
			case "minimize":
				state.open = false
			case "requirement":
				s.requirementUnit.state.requirementContentJobs = &requirementContentJobState{visible: "another requirement"}
			case "account":
				s.requirementUnit.state.requirementIdentityJobs = &requirementIdentityJobState{confirmed: true, identity: "account-2", state: application.RequirementIdentityAuthenticated}
			case "home":
				s.snapshot.Home += "-changed"
			case "config":
				s.settingsUnit.state.configJSON = `{"meegleExecutablePath":"other-cli"}`
			}
			s.observeRequirementCommentComposers()
			cancelled := change == "account" || change == "home" || change == "config"
			if (call.ctx.Err() != nil) != cancelled {
				t.Fatalf("wrong cancellation %s %v", change, call.ctx.Err())
			}
			if !cancelled {
				d := state.draft
				d.Content = ""
				d.Revision++
				d.Attempt = &domain.CommentPublishAttempt{ID: "attempt", State: "published", CommentID: "c1"}
				call.reply <- taskOpenToolsProbeReply{result: application.ActionResult{Data: domain.CommentPublishResult{Status: "published", Message: "评论已发布", Draft: d}}}
			}
			nextTaskOpenToolsUpdate(t, updates)()
			if state.running {
				t.Fatal("finished job stayed busy")
			}
			if cancelled && (s.lastMessage != "" || state.body != "snapshot text") {
				t.Fatal("late result touched new target")
			}
			if !cancelled && (state.body != "" || state.open || s.lastMessage != "评论已发布") {
				t.Fatal("success did not clear matching draft")
			}
		})
	}
}

func TestCommentComposerPendingNeverResubmits(t *testing.T) {
	s, b, updates, state := commentComposerFixture(t)
	state.body = "pending"
	state.draft.Content = state.body
	state.draft.Attempt = &domain.CommentPublishAttempt{ID: "attempt", State: "unknown"}
	if commentComposerCanPublish(state) {
		t.Fatal("unknown write enabled publish")
	}
	s.observeRequirementCommentComposers()
	state.open = false
	s.openRequirementCommentComposer(&requirementReaderState{url: state.url})
	if b.count.Load() != 1 {
		t.Fatal("restore restarted publish")
	}
	s.startCommentComposerJob(state, "check", nil)
	call := nextCommentComposerCall(t, b)
	if call.action.Kind != "requirements.comments/check" {
		t.Fatal("check dispatched create")
	}
	call.reply <- taskOpenToolsProbeReply{result: application.ActionResult{Data: domain.CommentPublishResult{Status: "unknown", Message: "仍待核对", Draft: state.draft}}}
	nextTaskOpenToolsUpdate(t, updates)()
	if !state.draft.Pending() || state.body != "pending" {
		t.Fatal("check lost pending content")
	}
}

func TestCommentComposerNativeDialogLayoutAndKeyboard(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			for _, mode := range []string{"edit", "preview", "save", "load", "check", "publishing", "unknown", "error"} {
				t.Run(fmt.Sprintf("%d-dark-%t-%s", width, dark, mode), func(t *testing.T) {
					s, _, _, state := commentComposerFixture(t)
					s.backend = nil
					s.dark = dark
					state.body = "# 标题\n\n- **Markdown** 评论\n- 保留链接 [需求](https://example.com)\n\n```go\nfmt.Println(\"中文\")\n```"
					state.draft.Content = state.body
					state.draft.Item.Title = strings.Repeat("长名称需求说明", 9)
					bitmapImage := image.NewRGBA(image.Rect(0, 0, 160, 90))
					for y := 0; y < 90; y++ {
						for x := 0; x < 160; x++ {
							bitmapImage.Set(x, y, color.RGBA{R: uint8(x), G: uint8(100 + y), B: 190, A: 255})
						}
					}
					imagePath := filepath.Join(t.TempDir(), "staged.png")
					imageFile, err := os.Create(imagePath)
					if err != nil {
						t.Fatal(err)
					}
					if err = png.Encode(imageFile, bitmapImage); err != nil {
						t.Fatal(err)
					}
					_ = imageFile.Close()
					state.draft.Images = []domain.CommentDraftAsset{{ID: "image-1", Name: strings.Repeat("多图预览", 9) + ".png", Size: 16384, LocalPath: imagePath}}
					state.draft.Attachment = &domain.CommentDraftAsset{ID: "attachment-1", Name: "发布说明及处理结果.md", Size: 8000}
					state.thumbnails = map[string]*ui.Bitmap{"image-1": ui.NewBitmap(bitmapImage)}
					if mode == "preview" {
						state.preview = true
					}
					if mode == "save" || mode == "load" || mode == "check" {
						state.running = true
						state.operation = mode
					}
					if mode == "publishing" {
						state.running = true
						state.operation = "publish"
						state.progress = domain.CommentPublishProgress{Stage: "上传图片", Completed: 1, Total: 3}
					}
					if mode == "unknown" {
						state.draft.Attempt = &domain.CommentPublishAttempt{ID: "attempt", State: "unknown"}
					}
					if mode == "error" {
						state.errorText = "上传失败：网络不可用；草稿已保留，请检查连接后重试。"
					}
					tt := ui.NewTester(func(c *ui.Context) {
						p := setTheme(c, dark)
						ui.Column(c).Fill().Background(p.bg)
						s.renderRequirementCommentComposer(c, p)
					}, width, 900)
					if state.markdown != nil {
						bitmap, e := decodeNativeImagePreview(context.Background(), imagePath, 1200)
						if e != nil {
							t.Fatal(e)
						}
						for _, entry := range state.markdown.inlineImages {
							entry.loading = false
							entry.bitmap = bitmap
							entry.error = ""
						}
						tt.Frame()
						if tt.HasText("图片加载失败") {
							t.Fatal("native comment image preview failed")
						}
					}
					for _, name := range []string{"添加评论", "编辑", "预览", "添加图片", "添加附件"} {
						if !tt.HasText(name) {
							t.Fatalf("missing %s %v", name, tt.Texts())
						}
					}
					visible := strings.Join(tt.Texts(), "\n")
					for _, redundant := range []string{"Meegle 账号", "测试账号", "account-1", "草稿已自动保存", "保存草稿", "输入评论，支持 Markdown；Enter 换行"} {
						if strings.Contains(visible, redundant) {
							t.Fatalf("redundant information remains visible: %s", redundant)
						}
					}
					if mode == "save" && (!tt.HasText("关闭") || tt.HasText("收起")) {
						t.Fatal("autosave changed the close button")
					}
					if mode == "load" && !tt.HasText("读取草稿") || mode == "check" && !tt.HasText("核对发布结果") || mode == "publishing" && (!tt.HasText("上传图片 · 已上传 1／3") || !tt.HasText("收起")) || mode == "error" && !tt.HasText(state.errorText) {
						t.Fatal("necessary progress or error is missing")
					}
					if mode == "unknown" {
						for _, name := range []string{"核对发布结果", "打开原需求", "重新发布…"} {
							if !tt.HasText(name) {
								t.Fatal(name)
							}
						}
						if tt.HasText("发布评论") {
							t.Fatal("pending has publish button")
						}
					}
					if r, ok := tt.Find("添加需求评论弹窗"); !ok || r.X < 0 || r.Y < 0 || r.X+r.W > float32(width)+1 || r.Y+r.H > 901 {
						t.Fatalf("dialog outside window: %+v %t", r, ok)
					}
					captureDesktopTestFrame(t, tt, fmt.Sprintf("comment-composer-%d-dark-%t-%s", width, dark, mode))
					tt.Key(0, ui.KeyEscape)
					if state.open {
						t.Fatal("Escape did not minimize dialog")
					}
					if mode == "publishing" && !state.running {
						t.Fatal("Escape cancelled publication")
					}
				})
			}
		}
	}
}

func TestCommentReaderAlwaysOffersAdd(t *testing.T) {
	for _, failure := range []bool{false, true} {
		s := &app{
			settingsUnit: settingsController{state: settingsState{configJSON: `{}`}},
		}
		r := &requirementReaderState{url: "https://project.feishu.cn/fixture/userstory/detail/101"}
		if failure {
			r.commentsError = "读取失败"
		} else {
			r.comments = &application.RequirementComments{}
		}
		tt := ui.NewTester(func(c *ui.Context) {
			p := setTheme(c, false)
			ui.Column(c).Fill().Children(func() { s.renderRequirementReadonlyMarkdownReader(c, p, r) })
		}, 900, 800)
		if !tt.HasText("添加评论") {
			t.Fatalf("missing add for failure=%t: %v", failure, tt.Texts())
		}
	}
}

func TestCommentComposerEnterAndConfirmationEscape(t *testing.T) {
	s, b, _, state := commentComposerFixture(t)
	tt := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		ui.Column(c).Fill()
		s.renderRequirementCommentComposer(c, p)
		s.renderConfirmationDialog(c, p)
	}, 960, 900)
	if err := tt.Click("评论 Markdown 正文"); err != nil {
		t.Fatal(err)
	}
	tt.Type("只换行")
	tt.Key(0, ui.KeyEnter)
	call := nextCommentComposerCall(t, b)
	if call.action.Kind != "requirements.comment-draft" || call.action.Payload["operation"] != "save" || !strings.Contains(state.body, "\n") {
		t.Fatal("Enter published instead of editing", call.action, state.body)
	}
	// An independently displayed top confirmation owns Esc and default focus.
	state.running = false
	state.dirty = false
	s.confirmationDialog = &confirmationDialogState{Title: "清空草稿", Message: "只清空本地草稿", OwnerKey: s.confirmationOwnerKey(), Open: true, Buttons: []confirmationDialogButton{{label: "清空草稿", primary: true}, {label: "取消"}}, DefaultIndex: 1, CancelIndex: 1}
	tt.Frame()
	if !tt.Focused("取消") {
		t.Fatal("confirmation did not default to Cancel")
	}
	tt.Key(0, ui.KeyEscape)
	if s.confirmationDialog != nil || !state.open {
		t.Fatal("Esc closed the underlying composer")
	}
}

func TestCommentRefreshTouchesOnlyMatchingComments(t *testing.T) {
	s, _, _, state := commentComposerFixture(t)
	matching := &requirementReaderState{url: state.url, body: &application.RequirementContent{Body: "正文不变"}, comments: &application.RequirementComments{}}
	other := &requirementReaderState{url: "https://project.feishu.cn/fixture/userstory/detail/102", comments: &application.RequirementComments{}}
	s.requirementUnit.state.requirementReaders = map[string]*requirementReaderState{"same": matching, "other": other}
	s.refreshPublishedRequirementComments(state.url)
	if !matching.commentsDirty || other.commentsDirty || matching.body.Body != "正文不变" {
		t.Fatal("comment refresh changed other content")
	}
}

func TestCommentComposerHelpAndIdleSaveClose(t *testing.T) {
	s, b, _, state := commentComposerFixture(t)
	const help = "支持 Markdown；Enter 换行"
	tt := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		ui.Column(c).Fill()
		s.renderRequirementCommentComposer(c, p)
	}, 960, 900)
	if tt.HasText(help) || tt.HasText("草稿已自动保存") {
		t.Fatalf("editor is not quiet by default: %#v", tt.Texts())
	}
	captureDesktopTestFrame(t, tt, "comment-minimal-empty-960-light")
	edit, ok := tt.Find("编辑")
	if !ok {
		t.Fatal("edit control missing")
	}
	tt.Move(edit.X+edit.W/2, edit.Y+edit.H/2)
	time.Sleep(650 * time.Millisecond)
	tt.Frame()
	if !tt.HasText(help) {
		t.Fatal("edit help did not appear on hover")
	}
	tt.Key(0, ui.KeyEscape)
	if tt.HasText(help) || !state.open {
		t.Fatal("Escape closed the composer instead of its tooltip")
	}
	tt.Move(950, 890)
	tt.Frame()
	for i := 0; i < 16; i++ {
		tt.Key(0, ui.KeyTab)
		if tt.Focused("编辑") {
			break
		}
	}
	if !tt.Focused("编辑") || !tt.HasText(help) {
		t.Fatal("keyboard focus did not show native help")
	}
	tt.Key(0, ui.KeyEscape)
	if !state.open || tt.HasText(help) {
		t.Fatal("keyboard help dismissal changed the composer")
	}
	state.running = true
	state.operation = "save"
	state.body = "保留输入"
	state.dirty = true
	tt.Frame()
	if !tt.HasText("关闭") || tt.HasText("收起") || tt.HasText("保存草稿") {
		t.Fatal("autosave disturbed the footer")
	}
	if err := tt.Click("关闭"); err != nil {
		t.Fatal(err)
	}
	if state.open || !state.running || state.body != "保留输入" || !state.dirty || b.count.Load() != 1 {
		t.Fatal("closing the saving draft cancelled or changed it")
	}
}

var _ application.Backend = (*taskOpenToolsProbeBackend)(nil)
