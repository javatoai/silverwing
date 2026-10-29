package desktop

import (
	"fmt"
	"image"
	"image/color"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func TestCommentDraftImageTopLayerAndEscape(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, backend, _, state := commentComposerFixture(t)
				state.body, state.preview = "# 你好", true
				state.draft.Content = state.body
				path := filepath.Join(t.TempDir(), "screenshot.png")
				writeMarkdownImageFixture(t, path, 320, 180)
				state.draft.Images = []domain.CommentDraftAsset{{ID: "image", Name: "截图.png", LocalPath: path}}
				pixels := image.NewRGBA(image.Rect(0, 0, 320, 180))
				for y := 0; y < 180; y++ {
					for x := 0; x < 320; x++ {
						pixels.SetRGBA(x, y, color.RGBA{R: uint8(40 + x/3), G: uint8(70 + y/2), B: 190, A: 255})
					}
				}
				bitmap := ui.NewBitmap(pixels)
				tester := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, dark)
					ui.Column(c).Fill().Background(p.bg)
					s.renderMarkdownBitmapViewer(c, p)
					s.observeRequirementCommentComposers()
					s.renderRequirementCommentComposer(c, p)
					s.renderCommentDraftBitmapViewer(c, p)
					s.renderConfirmationDialog(c, p)
				}, width, 900)
				seedMarkdownReadyImages(t, s, state.markdown, bitmap)
				tester.Frame()
				if err := tester.Click("Markdown 图片：截图.png"); err != nil {
					t.Fatal(err)
				}
				viewer := s.materialsUnit.state.markdownBitmapViewer
				if viewer == nil || viewer.composer != state {
					t.Fatal("image was not bound to its composer")
				}
				viewer.media.imageExpandedError, viewer.media.imageExpandedLoading = "", false
				viewer.media.imageExpandedBitmap = bitmap
				viewer.media.imageInfo.Width, viewer.media.imageInfo.Height = 320, 180
				tester.Frame()
				if _, ok := tester.Find("关闭图片"); !ok {
					t.Fatal("top image viewer did not render")
				}
				captureDesktopTestFrame(t, tester, fmt.Sprintf("comment-image-top-%d-dark-%t", width, dark))
				s.confirmationDialog = &confirmationDialogState{Title: "清空草稿", Message: "只清空本地草稿", OwnerKey: s.confirmationOwnerKey(), Open: true, Buttons: []confirmationDialogButton{{label: "清空草稿", primary: true}, {label: "取消"}}, DefaultIndex: 1, CancelIndex: 1}
				tester.Frame()
				if !tester.Focused("取消") {
					t.Fatal("confirmation above image did not focus cancel")
				}
				tester.Key(0, ui.KeyEscape)
				if s.confirmationDialog != nil || s.materialsUnit.state.markdownBitmapViewer != viewer || !state.open {
					t.Fatal("Esc passed through the top confirmation")
				}
				tester.Key(0, ui.KeyEscape)
				tester.Frame()
				if s.materialsUnit.state.markdownBitmapViewer != nil || !state.open || state.body != "# 你好" || !state.preview {
					t.Fatal("Esc closed or changed the underlying draft")
				}
				if !tester.Focused("Markdown 图片：截图.png") {
					t.Fatal("focus did not return to the original image")
				}
				tester.Key(0, ui.KeyEnter)
				if s.materialsUnit.state.markdownBitmapViewer == nil {
					t.Fatal("keyboard did not reopen the image")
				}
				tester.Key(0, ui.KeyEscape)
				for _, action := range []string{"发布评论", "移除"} {
					s.openMarkdownBitmapViewer(state.markdown, bitmap)
					tester.Frame()
					_ = tester.Click(action)
					if state.running || backend.count.Load() != 1 || len(state.draft.Images) != 1 {
						t.Fatal("pointer passed through to composer actions")
					}
					if s.materialsUnit.state.markdownBitmapViewer != nil {
						tester.Key(0, ui.KeyEscape)
					}
				}
				s.openMarkdownBitmapViewer(state.markdown, bitmap)
				tester.Frame()
				if err := tester.Click("关闭图片"); err != nil {
					t.Fatal(err)
				}
				if s.materialsUnit.state.markdownBitmapViewer != nil || !state.open {
					t.Fatal("close button affected the composer")
				}
				tester.Key(0, ui.KeyEscape)
				if state.open {
					t.Fatal("second Esc did not close the composer")
				}
			})
		}
	}
}

func TestCommentDraftImageOwnerInvalidation(t *testing.T) {
	for _, change := range []string{"closed", "edit", "active", "navigation", "home", "config", "account", "published"} {
		t.Run(change, func(t *testing.T) {
			s, _, _, state := commentComposerFixture(t)
			s.backend = nil
			state.preview = true
			state.markdown = newReadonlyMarkdownState("comment-draft:"+state.draft.ID, "preview", nil)
			state.markdown.commentReading = true
			t.Cleanup(func() { closeMarkdownPreviewState(state.markdown) })
			s.openMarkdownBitmapViewer(state.markdown, ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 20, 20))))
			viewer := s.materialsUnit.state.markdownBitmapViewer
			if viewer == nil {
				t.Fatal("viewer did not open")
			}
			viewer.sourcePath = "late-original.png"
			ctx, generation := beginMarkdownLocalImageViewerRequest(viewer)
			switch change {
			case "closed":
				state.open = false
			case "edit":
				state.preview = false
			case "active":
				s.requirementUnit.state.commentComposers.active = &requirementCommentComposer{}
			case "navigation":
				s.page = "tasks"
			case "home":
				s.snapshot.Home += "-other"
			case "config":
				s.settingsUnit.state.configJSON = `{"meegleExecutablePath":"other"}`
			case "account":
				state.account = "old"
				s.requirementUnit.state.requirementIdentityJobs = &requirementIdentityJobState{confirmed: true, identity: "other", state: application.RequirementIdentityAuthenticated}
			case "published":
				state.open, state.body = false, ""
			}
			s.observeRequirementCommentComposers()
			if s.materialsUnit.state.markdownBitmapViewer != nil || ctx.Err() == nil || markdownLocalImageViewerResultCurrent(s, viewer, generation) || viewer.media.imageExpandedBitmap != nil {
				t.Fatal("invalidated draft retained its image or accepted a late bitmap")
			}
		})
	}
}
