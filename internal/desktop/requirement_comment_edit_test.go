package desktop

import (
	"fmt"
	"image"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func commentEditorFixture(t *testing.T) (*app, *taskOpenToolsProbeBackend, chan func(), *requirementCommentComposer, *requirementCommentComposer) {
	t.Helper()
	s, b, updates, creation := commentComposerFixture(t)
	s.openRequirementCommentEditor(&requirementReaderState{url: creation.url}, "comment-42")
	state := s.requirementUnit.state.commentComposers.active
	call := nextCommentComposerCall(t, b)
	if call.action.Kind != "requirements.comment-draft" || call.action.Payload["editCommentID"] != "comment-42" || call.action.Payload["operation"] != "load" {
		t.Fatal(call.action)
	}
	d := creation.draft
	d.ID = strings.Repeat("e", 32)
	d.Content = "原始评论"
	remote := domain.RequirementComment{ID: "comment-42", Content: d.Content, AuthorKey: "other-user"}
	d.Edit = &domain.CommentEditTarget{CommentID: "comment-42", Original: &remote}
	call.reply <- taskOpenToolsProbeReply{result: application.ActionResult{Data: d}}
	nextTaskOpenToolsUpdate(t, updates)()
	return s, b, updates, state, creation
}

func TestCommentEditorDraftIsolationUpdateAndLateResults(t *testing.T) {
	for _, change := range []string{"none", "minimize", "requirement", "account", "home", "config", "wrong-comment"} {
		t.Run(change, func(t *testing.T) {
			s, b, updates, state, creation := commentEditorFixture(t)
			if commentComposerCanPublish(state) {
				t.Fatal("unchanged comment is saveable")
			}
			state.body = ""
			state.draft.Content = ""
			if commentComposerCanPublish(state) {
				t.Fatal("empty comment is saveable")
			}
			state.body = "修改后"
			state.draft.Content = state.body
			if !commentComposerCanPublish(state) {
				t.Fatal("changed comment cannot save")
			}
			s.startCommentComposerJob(state, "publish", nil)
			call := nextCommentComposerCall(t, b)
			if call.action.Kind != "requirements.comments/update" || call.action.Payload["editCommentID"] != "comment-42" {
				t.Fatal("edit fell back to create", call.action)
			}
			s.startCommentComposerJob(state, "publish", nil)
			if b.count.Load() != 3 {
				t.Fatal("double-click duplicated update")
			}
			switch change {
			case "minimize":
				state.open = false
			case "requirement":
				s.requirementUnit.state.requirementContentJobs = &requirementContentJobState{visible: "different"}
			case "account":
				s.requirementUnit.state.requirementIdentityJobs = &requirementIdentityJobState{confirmed: true, identity: "another", state: application.RequirementIdentityAuthenticated}
			case "home":
				s.snapshot.Home += "-new"
			case "config":
				s.settingsUnit.state.configJSON = `{"meegleExecutablePath":"new"}`
			}
			s.observeRequirementCommentComposers()
			d := state.draft
			d.Content = ""
			d.Attempt = &domain.CommentPublishAttempt{State: "published", CommentID: "comment-42"}
			if change == "wrong-comment" {
				e := *d.Edit
				e.CommentID = "comment-other"
				d.Edit = &e
			}
			call.reply <- taskOpenToolsProbeReply{result: application.ActionResult{Data: domain.CommentPublishResult{Status: "updated", Draft: d, CommentID: "comment-42"}}}
			nextTaskOpenToolsUpdate(t, updates)()
			if creation.body != "" || creation.draft.Edit != nil || creation.draft.ID == state.draft.ID {
				t.Fatal("edit overwrote creation draft")
			}
			if change == "account" || change == "home" || change == "config" || change == "wrong-comment" {
				if s.lastMessage != "" {
					t.Fatal("stale/invalid result changed new target")
				}
			}
			if change == "wrong-comment" && (!state.open || state.body != "修改后") {
				t.Fatal("mismatched result discarded current input")
			}
		})
	}
}

func TestCommentEditorNativeLayoutConflictPendingAndEscape(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			for _, mode := range []string{"edit", "preview", "conflict", "pending", "upload"} {
				t.Run(fmt.Sprintf("%d-dark-%t-%s", width, dark, mode), func(t *testing.T) {
					s, _, _, state, _ := commentEditorFixture(t)
					s.backend = nil
					s.dark = dark
					state.body += "\n\n修改的 **Markdown** 内容"
					state.draft.Content = state.body
					state.draft.Item.Title = strings.Repeat("评论目标长名称", 12)
					switch mode {
					case "preview":
						state.preview = true
					case "conflict":
						state.draft.Edit.Conflict = true
					case "pending":
						state.draft.Attempt = &domain.CommentPublishAttempt{State: "unknown", CommentID: state.editCommentID}
					case "upload":
						state.running = true
						state.operation = "publish"
						state.progress = domain.CommentPublishProgress{Stage: "上传图片", Completed: 1, Total: 3}
					}
					tester := ui.NewTester(func(c *ui.Context) {
						p := setTheme(c, dark)
						ui.Column(c).Fill().Background(p.bg)
						s.renderRequirementCommentComposer(c, p)
						s.renderConfirmationDialog(c, p)
					}, width, 900)
					for _, text := range []string{"编辑评论", "编辑", "预览", "添加图片", "粘贴截图", "添加附件"} {
						if !tester.HasText(text) {
							t.Fatal("missing", text)
						}
					}
					for _, text := range []string{"Meegle 账号", "account-1", "测试账号", "发布评论", "重新发布…"} {
						if tester.HasText(text) {
							t.Fatal("unexpected", text)
						}
					}
					if mode == "pending" {
						if !tester.HasText("核对修改结果") || tester.HasText("保存修改") || tester.HasText("放弃修改") {
							t.Fatal("pending edit allows a new write")
						}
					} else if !tester.HasText("保存修改") {
						t.Fatal("missing save")
					}
					if mode == "conflict" && (!tester.HasText("重新载入原评论") || commentComposerCanPublish(state)) {
						t.Fatal("conflict is not protected")
					}
					if r, ok := tester.Find("编辑需求评论弹窗"); !ok || r.X < 0 || r.X+r.W > float32(width)+1 || r.Y+r.H > 901 {
						t.Fatal("edit dialog outside viewport", r)
					}
					captureDesktopTestFrame(t, tester, fmt.Sprintf("comment-editor-%d-dark-%t-%s", width, dark, mode))
					tester.Key(0, ui.KeyEscape)
					if state.open || state.body == "" {
						t.Fatal("Esc discarded edit")
					}
					if mode == "upload" && !state.running {
						t.Fatal("Esc cancelled update")
					}
				})
			}
		}
	}
}

func TestCommentEditorConfirmationCancelAndReload(t *testing.T) {
	s, b, updates, state, _ := commentEditorFixture(t)
	s.win = &mygo.Window{}
	state.body = "本地修改"
	state.draft.Content = state.body
	state.draft.Edit.Conflict = true
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		ui.Column(c).Fill()
		s.renderRequirementCommentComposer(c, p)
		s.renderConfirmationDialog(c, p)
	}, 960, 900)
	if err := tester.Click("放弃修改"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.Focused("取消") {
		t.Fatal("discard confirmation did not focus cancel")
	}
	tester.Key(0, ui.KeyEscape)
	if s.confirmationDialog != nil || !state.open || state.body != "本地修改" || b.count.Load() != 2 {
		t.Fatal("cancel/Esc discarded edit or issued action")
	}
	if err := tester.Click("重新载入原评论"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if err := tester.Click("重新载入"); err != nil {
		t.Fatal(err)
	}
	call := nextCommentComposerCall(t, b)
	if call.action.Payload["operation"] != "reload" || call.action.Payload["confirmed"] != true || call.action.Payload["editCommentID"] != "comment-42" {
		t.Fatal(call.action)
	}
	d := state.draft
	e := *d.Edit
	e.Conflict = false
	remote := *e.Original
	remote.Content = "远端修改"
	e.Original = &remote
	d.Edit = &e
	d.Content = remote.Content
	d.Revision++
	call.reply <- taskOpenToolsProbeReply{result: application.ActionResult{Data: d}}
	nextTaskOpenToolsUpdate(t, updates)()
	if state.body != "远端修改" || state.draft.Edit.Conflict {
		t.Fatal("reload did not replace only this draft")
	}
}

func TestCommentCardsOfferEditForOtherAuthorsAndReplies(t *testing.T) {
	for _, taskID := range []string{"", "task-entry"} {
		for _, id := range []string{"other-comment", "reply-comment"} {
			t.Run(taskID+"/"+id, func(t *testing.T) {
				s, b, _, creation := commentComposerFixture(t)
				creation.open = false
				reader := s.requirementReader(taskID, creation.url)
				parent := "other-comment"
				s.updateRequirementReaderComments(reader, application.RequirementComments{Comments: []domain.RequirementComment{{ID: "other-comment", AuthorKey: "other", Content: "他人评论"}, {ID: "reply-comment", AuthorKey: "another", Content: "回复正文", ParentID: &parent}, {Content: "没有编号"}}})
				tester := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, false)
					s.beginOverflowMenuFrame()
					ui.Column(c).Fill().Children(func() { s.renderRequirementCommentList(c, p, reader) })
					s.endOverflowMenuFrame()
				}, 960, 900)
				if err := tester.Click("评论操作 " + id); err != nil {
					t.Fatal(err)
				}
				tester.Frame()
				if !tester.HasText("编辑") || tester.HasText("删除") {
					t.Fatal("wrong comment actions")
				}
				if err := tester.Click("编辑"); err != nil {
					t.Fatal(err)
				}
				call := nextCommentComposerCall(t, b)
				if call.action.Kind != "requirements.comment-draft" || call.action.Payload["editCommentID"] != id || call.action.Payload["operation"] != "load" {
					t.Fatal("menu wrote remotely or targeted wrong comment", call.action)
				}
				call.reply <- taskOpenToolsProbeReply{}
			})
		}
	}
}

func TestCommentEditorExistingImageTopLayerZoomAndEscape(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, b, _, state, _ := commentEditorFixture(t)
				path := filepath.Join(t.TempDir(), "original.png")
				writeMarkdownImageFixture(t, path, 320, 180)
				originalURL := "https://project.feishu.cn/goapi/v5/platform/file/stream/download/original"
				state.body = "图片前文\n\n![原图](" + originalURL + ")\n\n图片后文"
				state.preview = true
				state.draft.Content = state.body
				state.draft.Images = []domain.CommentDraftAsset{{ID: "original", Name: "原图.png", OriginalURL: originalURL, LocalPath: path}}
				bitmap := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 320, 180)))
				tester := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, dark)
					ui.Column(c).Fill().Background(p.bg)
					s.renderMarkdownBitmapViewer(c, p)
					s.observeRequirementCommentComposers()
					s.renderRequirementCommentComposer(c, p)
					s.renderCommentDraftBitmapViewer(c, p)
					s.renderConfirmationDialog(c, p)
				}, width, 900)
				for _, entry := range state.markdown.inlineImages {
					entry.loading, entry.error, entry.bitmap = false, "", bitmap
				}
				tester.Frame()
				if !tester.HasText("图片前文") || !tester.HasText("图片后文") {
					t.Fatal("image positions lost in preview")
				}
				if err := tester.Click("Markdown 图片：原图"); err != nil {
					t.Fatal(err)
				}
				viewer := s.materialsUnit.state.markdownBitmapViewer
				if viewer == nil || viewer.composer != state {
					t.Fatal("image rendered behind edit dialog")
				}
				viewer.media.imageExpandedBitmap = bitmap
				viewer.media.imageExpandedLoading = false
				viewer.media.imageExpandedError = ""
				viewer.media.imageInfo.Width, viewer.media.imageInfo.Height = 320, 180
				tester.Frame()
				canvas, ok := tester.Find("图片画布")
				if !ok {
					t.Fatal("no image canvas")
				}
				zoom := viewer.media.imageZoom
				tester.Scroll(canvas.X+canvas.W/2, canvas.Y+canvas.H/2, 0, -120)
				if viewer.media.imageZoom <= zoom {
					t.Fatal("edit image wheel zoom did not work")
				}
				captureDesktopTestFrame(t, tester, fmt.Sprintf("comment-editor-image-%d-dark-%t", width, dark))
				_ = tester.Click("保存修改")
				if b.count.Load() != 2 || state.running {
					t.Fatal("image click passed through to update")
				}
				if s.materialsUnit.state.markdownBitmapViewer != nil {
					tester.Key(0, ui.KeyEscape)
				}
				tester.Frame()
				if !state.open || state.body != state.draft.Content {
					t.Fatal("image close discarded edit")
				}
				tester.Key(0, ui.KeyEscape)
				if state.open {
					t.Fatal("second Esc did not hide editor")
				}
			})
		}
	}
}
