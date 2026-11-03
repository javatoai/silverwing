package desktop

import (
	"image"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	markdownui "silverwing-go/internal/ui/markdown"
)

func commentMarkdownTester(s *app) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		ui.Column(c).Fill()
		s.renderRequirementCommentComposer(c, p)
	}, 960, 900)
}

func TestCommentComposerPublicMarkdownUsesOneHistoryAndLocalSave(t *testing.T) {
	s, backend, updates, state := commentComposerFixture(t)
	state.body, state.draft.Content = "alpha", "alpha"
	tester := commentMarkdownTester(s)
	owner := state.markdown
	if owner == nil || owner.componentEditor == nil || owner.componentEditor.Mode() != markdownui.Live || owner.live != owner.componentEditor.NativeState() {
		t.Fatal("comment composer did not adopt the public live Markdown editor")
	}
	if tester.HasText("编辑") || tester.HasText("预览") {
		t.Fatal("permanent edit/preview toolbar remains")
	}
	if _, err := markdownui.ResolveLink(owner.path, domain.CommentImageMarkdownRef("synthetic")); err != nil {
		t.Fatal("virtual path does not permit fragment-only staged references", err)
	}
	if _, err := markdownui.ResolveLink(owner.path, "sw-comment-image:synthetic"); err == nil {
		t.Fatal("unsafe custom URI scheme was accepted")
	}
	native, component := owner.live, owner.componentEditor
	native.SetSelection(0, 5)
	if err := component.Execute(markdownui.EditorCommand{Kind: markdownui.Bold}); err != nil {
		t.Fatal(err)
	}
	call := nextCommentComposerCall(t, backend)
	if call.action.Kind != "requirements.comment-draft" || call.action.Payload["operation"] != "save" || call.action.Payload["content"] != "**alpha**" {
		t.Fatal("formatting dispatched a remote comment write", call.action)
	}
	d := state.draft
	d.Content, d.Revision = state.body, d.Revision+1
	call.reply <- taskOpenToolsProbeReply{result: application.ActionResult{Data: d}}
	nextTaskOpenToolsUpdate(t, updates)()
	tester.Frame()
	if owner.live != native || owner.componentEditor != component || !native.Buffer().Undo() || native.Buffer().OriginalText() != "alpha" || !native.Buffer().Redo() || native.Buffer().OriginalText() != "**alpha**" {
		t.Fatal("save receipt replaced the native buffer or its undo history")
	}
	native.Focus()
	tester.Frame()
	tester.Key(ui.Ctrl, ui.KeyS)
	call = nextCommentComposerCall(t, backend)
	if call.action.Kind != "requirements.comment-draft" || call.action.Payload["operation"] != "save" {
		t.Fatal("Ctrl+S published or updated a remote comment", call.action)
	}
	d = state.draft
	d.Content, d.Revision = state.body, d.Revision+1
	call.reply <- taskOpenToolsProbeReply{result: application.ActionResult{Data: d}}
	nextTaskOpenToolsUpdate(t, updates)()
}

func TestCommentMarkdownImportInsertsAtCapturedCaretAndRetainsDeletedCache(t *testing.T) {
	s, backend, updates, state := commentComposerFixture(t)
	state.body, state.draft.Content = "前文 后文", "前文 后文"
	tester := commentMarkdownTester(s)
	owner := state.markdown
	owner.live.SetSelection(len("前文"), len("前文"))
	owner.componentEditor.ImportResources(markdownui.ImportRequest{Files: []string{"picked.png"}})
	call := nextCommentComposerCall(t, backend)
	if call.action.Kind != "requirements.comment-draft" || call.action.Payload["operation"] != "add-image" || call.action.Payload["inlineImage"] != true {
		t.Fatal("image import did not stage a draft-only image", call.action)
	}
	path := filepath.Join(t.TempDir(), "picked.png")
	writeMarkdownImageFixture(t, path, 16, 16)
	a := domain.CommentDraftAsset{ID: strings.Repeat("b", 32), Name: "picked.png", MarkdownRef: domain.CommentImageMarkdownRef(strings.Repeat("b", 32)), LocalPath: path}
	d := state.draft
	d.Revision++
	d.Images = append(d.Images, a)
	call.reply <- taskOpenToolsProbeReply{result: application.ActionResult{Data: d}}
	nextTaskOpenToolsUpdate(t, updates)()
	if !strings.HasPrefix(state.body, "前文\n\n![picked.png]") || !strings.HasSuffix(state.body, "\n\n 后文") || !strings.Contains(state.body, a.MarkdownRef) || owner.trustedImages[a.MarkdownRef] != path {
		t.Fatalf("image was appended or failed to acquire trusted metadata: %q", state.body)
	}
	call = nextCommentComposerCall(t, backend)
	if call.action.Payload["operation"] != "save" {
		t.Fatal("inline insertion did not save only the local draft")
	}
	d = state.draft
	d.Content, d.Revision = state.body, d.Revision+1
	call.reply <- taskOpenToolsProbeReply{result: application.ActionResult{Data: d}}
	nextTaskOpenToolsUpdate(t, updates)()
	if !owner.live.Buffer().Undo() {
		t.Fatal("inline image insertion was absent from undo history")
	}
	tester.Frame()
	if state.body != "前文 后文" || len(state.draft.Images) != 1 || owner.trustedImages[a.MarkdownRef] != path {
		t.Fatal("deleting the inline image lost its undo cache")
	}
	call = nextCommentComposerCall(t, backend)
	d = state.draft
	d.Content, d.Revision = state.body, d.Revision+1
	call.reply <- taskOpenToolsProbeReply{result: application.ActionResult{Data: d}}
	nextTaskOpenToolsUpdate(t, updates)()
	if len(state.draft.ActiveImages()) != 0 || !owner.live.Buffer().Redo() {
		t.Fatal("deleted image remained active or could not be restored")
	}
	tester.Frame()
	if !strings.Contains(state.body, a.MarkdownRef) {
		t.Fatal("Redo did not restore its registered image reference")
	}
}

func TestReviewCommentImageReplacementWhileNewerBodyNeedsSave(t *testing.T) {
	s, backend, updates, state := commentComposerFixture(t)
	path := filepath.Join(t.TempDir(), "old.png")
	writeMarkdownImageFixture(t, path, 16, 16)
	oldID := strings.Repeat("d", 32)
	oldRef := domain.CommentImageMarkdownRef(oldID)
	state.body = "![a](" + oldRef + ")"
	state.draft.Content = state.body
	state.draft.Images = []domain.CommentDraftAsset{{ID: oldID, Name: "old.png", MarkdownRef: oldRef, LocalPath: path}}
	v := commentMarkdownTester(s)
	owner := state.markdown
	seedMarkdownReadyImages(t, s, owner, ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 16, 16))))
	v.Frame()
	if err := owner.live.ReplaceRange(owner.live.Buffer().Len(), owner.live.Buffer().Len(), " first", owner.componentEditor.Snapshot().Version); err != nil {
		t.Fatal(err)
	}
	first := nextCommentComposerCall(t, backend)
	if err := owner.live.ReplaceRange(owner.live.Buffer().Len(), owner.live.Buffer().Len(), " second", owner.componentEditor.Snapshot().Version); err != nil {
		t.Fatal(err)
	}
	v.Frame()
	if err := v.Click("Markdown 图片：a"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("编辑"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("图片地址"); err != nil {
		t.Fatal(err)
	}
	v.Key(ui.Cmd, ui.KeyA)
	v.Type("D:/synthetic/replacement.png")
	if err := v.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if state.pendingImport == nil {
		t.Fatalf("replacement was not queued behind save: op=%q running=%v error=%q body=%q view=%v", state.operation, state.running, state.errorText, state.body, v.Texts())
	}
	d := state.draft
	d.Content = first.action.Payload["content"].(string)
	d.Revision++
	first.reply <- taskOpenToolsProbeReply{result: application.ActionResult{Data: d}}
	nextTaskOpenToolsUpdate(t, updates)()
	if !state.running || state.operation != "save" {
		t.Fatalf("queued import blocked prerequisite body save: operation=%q error=%q", state.operation, state.errorText)
	}
	second := nextCommentComposerCall(t, backend)
	d = state.draft
	d.Content = state.body
	d.Revision++
	second.reply <- taskOpenToolsProbeReply{result: application.ActionResult{Data: d}}
	nextTaskOpenToolsUpdate(t, updates)()
	imported := nextCommentComposerCall(t, backend)
	if imported.action.Payload["operation"] != "add-image" {
		t.Fatal("replacement import never started")
	}
	newID := strings.Repeat("e", 32)
	newRef := domain.CommentImageMarkdownRef(newID)
	d = state.draft
	d.Revision++
	d.Images = append(d.Images, domain.CommentDraftAsset{ID: newID, Name: "new.png", MarkdownRef: newRef, LocalPath: path})
	imported.reply <- taskOpenToolsProbeReply{result: application.ActionResult{Data: d}}
	nextTaskOpenToolsUpdate(t, updates)()
	if state.pendingImport != nil || state.importing || !strings.Contains(state.body, newRef) || !strings.HasSuffix(state.body, " first second") {
		t.Fatalf("replacement lost body or stayed pending: %q error=%q", state.body, state.errorText)
	}
	saved := nextCommentComposerCall(t, backend)
	d = state.draft
	d.Content = state.body
	d.Revision++
	saved.reply <- taskOpenToolsProbeReply{result: application.ActionResult{Data: d}}
	nextTaskOpenToolsUpdate(t, updates)()
}

func TestCommentMarkdownLateImportAndReceiptKeepNewerInput(t *testing.T) {
	s, backend, updates, state := commentComposerFixture(t)
	state.body, state.draft.Content = "原文", "原文"
	tester := commentMarkdownTester(s)
	owner := state.markdown
	owner.componentEditor.ImportResources(markdownui.ImportRequest{Files: []string{"late.png"}})
	call := nextCommentComposerCall(t, backend)
	if err := owner.live.ReplaceRange(len("原文"), len("原文"), "新增输入", owner.componentEditor.Snapshot().Version); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	a := domain.CommentDraftAsset{ID: strings.Repeat("c", 32), Name: "late.png", MarkdownRef: domain.CommentImageMarkdownRef(strings.Repeat("c", 32))}
	d := state.draft
	d.Revision++
	d.Images = append(d.Images, a)
	call.reply <- taskOpenToolsProbeReply{result: application.ActionResult{Data: d}}
	nextTaskOpenToolsUpdate(t, updates)()
	if state.body != "原文新增输入" || strings.Contains(state.body, a.MarkdownRef) || len(state.draft.Images) != 1 {
		t.Fatal("late import replaced newer input or discarded retained metadata")
	}
	call = nextCommentComposerCall(t, backend)
	if call.action.Payload["operation"] != "save" || call.action.Payload["content"] != state.body {
		t.Fatal("late import lost the newer local save")
	}
	d = state.draft
	d.Content, d.Revision = state.body, d.Revision+1
	call.reply <- taskOpenToolsProbeReply{result: application.ActionResult{Data: d}}
	nextTaskOpenToolsUpdate(t, updates)()
	state.draft.Content = state.body
	s.startCommentComposerJob(state, "publish", nil)
	call = nextCommentComposerCall(t, backend)
	// A receipt cannot clear even externally supplied newer input.
	if err := owner.live.ReplaceRange(len(state.body), len(state.body), "保留", owner.componentEditor.Snapshot().Version); err != nil {
		t.Fatal(err)
	}
	d = state.draft
	d.Content, d.Revision = "", d.Revision+1
	d.Attempt = &domain.CommentPublishAttempt{State: "published", CommentID: "created"}
	call.reply <- taskOpenToolsProbeReply{result: application.ActionResult{Data: domain.CommentPublishResult{Status: "published", Draft: d}}}
	nextTaskOpenToolsUpdate(t, updates)()
	if state.body != "原文新增输入保留" || !state.dirty || !state.open || state.markdown != owner {
		t.Fatal("published acknowledgement reset newer input")
	}
}

func TestCommentMarkdownSaveReceiptRejectsStaleDraftAndScope(t *testing.T) {
	for _, change := range []string{"revision", "draftID", "commentID", "scope", "account"} {
		t.Run(change, func(t *testing.T) {
			s, backend, updates, state := commentComposerFixture(t)
			state.body, state.draft.Content = "原文", "原文"
			tester := commentMarkdownTester(s)
			owner := state.markdown
			if err := owner.live.ReplaceRange(len("原文"), len("原文"), "更新", owner.componentEditor.Snapshot().Version); err != nil {
				t.Fatal(err)
			}
			tester.Frame()
			call := nextCommentComposerCall(t, backend)
			d := state.draft
			d.Content, d.Revision = state.body, d.Revision+1
			switch change {
			case "revision":
				state.draft.Revision++
			case "draftID":
				d.ID = strings.Repeat("f", 32)
			case "commentID":
				d.Edit = &domain.CommentEditTarget{CommentID: "unrelated"}
			case "scope":
				d.Scope = "replaced-config"
			case "account":
				d.Identity = "replaced-account"
			}
			call.reply <- taskOpenToolsProbeReply{result: application.ActionResult{Data: d}}
			nextTaskOpenToolsUpdate(t, updates)()
			if state.body != "原文更新" || owner.componentEditor.Snapshot().Source != state.body || state.markdown != owner || !state.dirty || state.errorText == "" || backend.count.Load() != 2 {
				t.Fatal("stale receipt replaced the current editor or acknowledged its newer draft")
			}
		})
	}
}

func TestCommentLiveInlineImageUsesOriginalViewerAboveComposer(t *testing.T) {
	s, backend, _, state := commentComposerFixture(t)
	path := filepath.Join(t.TempDir(), "inline.png")
	writeMarkdownImageFixture(t, path, 48, 24)
	ref := domain.CommentImageMarkdownRef("live-image")
	state.body = "图片前 ![实时图片](" + ref + ") 图片后"
	state.draft.Content = state.body
	state.draft.Images = []domain.CommentDraftAsset{{ID: "live-image", Name: "inline.png", MarkdownRef: ref, LocalPath: path}}
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		ui.Column(c).Fill()
		s.renderRequirementCommentComposer(c, p)
		s.renderCommentDraftBitmapViewer(c, p)
	}, 960, 900)
	seedMarkdownReadyImages(t, s, state.markdown, ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 48, 24))))
	tester.Frame()
	if err := tester.Click("Markdown 图片：实时图片"); err != nil {
		t.Fatal(err)
	}
	if s.materialsUnit.state.markdownBitmapViewer != nil {
		t.Fatal("single click opened live image")
	}
	if _, ok := state.markdown.live.SelectedImage(); !ok {
		t.Fatal("single click did not select live image")
	}
	if err := tester.Click("Markdown 图片：实时图片"); err != nil {
		t.Fatal(err)
	}
	viewer := s.materialsUnit.state.markdownBitmapViewer
	if viewer == nil {
		t.Fatalf("live image did not open its viewer: source=%q comment=%v detached=%v current=%v calls=%d", state.markdown.doc.Source(), state.markdown.commentReading, state.markdown.detached, s.commentComposerCurrent(state), backend.count.Load())
	}
	if viewer == nil || viewer.composer != state || viewer.sourcePath != path || state.markdown.componentEditor.Mode() != markdownui.Live || backend.count.Load() != 1 {
		t.Fatalf("viewer ownership: composer=%v path=%q want=%q mode=%v calls=%d source=%q expected=%q", viewer.composer == state, viewer.sourcePath, path, state.markdown.componentEditor.Mode(), backend.count.Load(), viewer.source, state.markdown.doc.Source())
	}
	tester.Key(0, ui.KeyEscape)
	if s.materialsUnit.state.markdownBitmapViewer != nil || !state.open || state.body != state.draft.Content {
		t.Fatal("closing the original image closed or changed its live comment")
	}
}
