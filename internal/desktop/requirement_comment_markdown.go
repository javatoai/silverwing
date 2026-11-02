package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/editor"
	markdownui "silverwing-go/internal/ui/markdown"
)

const commentMarkdownBodyLimit = 2 << 20

type commentMarkdownImport struct {
	owner    *markdownui.EditorState
	request  markdownui.ImportRequest
	complete func(markdownui.ImportResult, error)
	draftID  string
	itemKey  string
}

// This path is a resolution base for fragment references only. Comment drafts
// never create, read, or autosave a Markdown file at this virtual location.
func commentMarkdownVirtualPath(state *requirementCommentComposer) string {
	return filepath.Join(state.home, ".comment-editor", state.draft.ID+".md")
}

func (s *app) commentMarkdownCurrent(state *requirementCommentComposer, owner *markdownEditorState, draftID string) bool {
	return state != nil && owner != nil && !owner.closed && state.markdown == owner && state.loaded &&
		state.draft.ID == draftID && s.commentComposerCurrent(state) &&
		(state.editCommentID == "" && !state.draft.Editing() || state.draft.Edit != nil && state.draft.Edit.CommentID == state.editCommentID)
}

func commentMarkdownTrustedImages(draft domain.RequirementCommentDraft) map[string]string {
	images := map[string]string{}
	for _, a := range draft.Images {
		if a.LocalPath == "" || a.Error != "" {
			continue
		}
		if a.MarkdownRef == domain.CommentImageMarkdownRef(a.ID) {
			images[a.MarkdownRef] = a.LocalPath
		}
		if a.OriginalURL != "" {
			images[a.OriginalURL] = a.LocalPath
		}
	}
	return images
}

func (s *app) ensureCommentMarkdownEditor(state *requirementCommentComposer) *markdownEditorState {
	if state == nil || !state.loaded {
		return nil
	}
	key := "comment-draft:" + state.draft.ID
	owner := state.markdown
	if owner == nil || owner.closed || owner.taskID != key || owner.componentEditor == nil {
		closeMarkdownPreviewState(owner)
		owner = newReadonlyMarkdownState(key, state.body, nil)
		owner.path, owner.commentReading, owner.readOnly = commentMarkdownVirtualPath(state), true, false
		owner.componentEditor = markdownui.NewEditorState(markdownui.Document{ID: key, Source: state.body, Path: owner.path})
		if state.preview {
			_ = owner.componentEditor.SetMode(markdownui.Preview)
		}
		owner.live = owner.componentEditor.NativeState()
		owner.live.Buffer().SetMaxSourceBytes(commentMarkdownBodyLimit)
		owner.doc.SetMaxSourceBytes(commentMarkdownBodyLimit)
		owner.liveSource, owner.liveVersion = state.body, owner.componentEditor.Snapshot().Version
		state.markdown = owner
		draftID := state.draft.ID
		owner.binding = &markdownEditorBinding{id: key}
		owner.binding.isCurrent = func() bool { return s.commentMarkdownCurrent(state, owner, draftID) }
		owner.binding.commit = func(source string) { s.commitCommentMarkdownInput(state, owner, source) }
		owner.binding.save = func() {
			if owner.binding.isCurrent() {
				s.startCommentComposerJob(state, "save", nil)
			}
		}
	}
	owner.trustedImages = commentMarkdownTrustedImages(state.draft)
	return owner
}

func (s *app) commitCommentMarkdownInput(state *requirementCommentComposer, owner *markdownEditorState, source string) {
	if !s.commentMarkdownCurrent(state, owner, state.draft.ID) {
		return
	}
	owner.liveSource, owner.liveVersion = source, owner.componentEditor.Snapshot().Version
	if owner.doc.Source() != source {
		owner.doc.SyncExternal(source)
	}
	if state.body == source {
		return
	}
	state.body, state.dirty, state.errorText = source, source != state.draft.Content, ""
	if !state.capturingInput && !state.running && !state.draft.Pending() && state.pendingImport == nil {
		s.startCommentComposerJob(state, "save", nil)
	}
}

// CaptureInput ends IME and observes the one native buffer. Save receipts never
// reach Reload: only accepted load/reload/clear/discard operations do.
func (s *app) captureCommentMarkdownInput(state *requirementCommentComposer) bool {
	if state == nil || state.markdown == nil || state.markdown.componentEditor == nil {
		return true
	}
	owner := state.markdown
	if !s.commentMarkdownCurrent(state, owner, state.draft.ID) {
		state.errorText = "评论编辑目标已变化，当前输入已保留"
		return false
	}
	state.capturingInput = true
	defer func() { state.capturingInput = false }()
	snapshot, err := owner.componentEditor.CaptureInput()
	if err != nil {
		state.errorText = err.Error()
		return false
	}
	s.commitCommentMarkdownInput(state, owner, snapshot.Source)
	return true
}

func (s *app) acceptCommentMarkdownRead(state *requirementCommentComposer, source string) {
	owner := state.markdown
	if owner == nil || owner.componentEditor == nil || owner.taskID != "comment-draft:"+state.draft.ID {
		closeMarkdownPreviewState(owner)
		state.markdown = nil
		return
	}
	if err := owner.componentEditor.Reload(source, owner.componentEditor.Snapshot().Version); err != nil {
		state.errorText, state.dirty = err.Error(), true
		return
	}
	owner.liveSource, owner.liveVersion = source, owner.componentEditor.Snapshot().Version
	owner.doc.SyncExternal(source)
}

func (s *app) renderCommentMarkdownEditor(c *ui.Context, p palette, state *requirementCommentComposer, locked bool) {
	owner := s.ensureCommentMarkdownEditor(state)
	if owner == nil {
		ui.Column(c).FillWidth().Grow(1).MinHeight(90).Background(p.soft).Radius(8)
		return
	}
	owner.readOnly = locked
	snapshot := owner.componentEditor.Snapshot()
	if owner.liveFramework == nil || owner.liveFramework.original != snapshot.Source {
		native := &frameworkMarkdownState{original: snapshot.Source, projection: editor.ReadingProjection{Source: owner.live.Buffer().Snapshot().String()}}
		native.document = parser.Parse(native.projection.Source)
		native.imageTargets = frameworkMarkdownImageTargets(native.document)
		native.sourceOffset = func(offset int) int {
			return editor.RuneToByteOffset(snapshot.Source, textEditorSourceRuneOffset(snapshot.Source, offset))
		}
		owner.liveFramework = native
	}
	native := owner.liveFramework
	s.configureFrameworkMarkdown(c, p, owner, native)
	options := markdownui.EditorOptions{
		ReaderOptions: markdownui.ReaderOptions{Native: &native.options, OnError: func(err error) {
			if err != nil && s.commentMarkdownCurrent(state, owner, state.draft.ID) {
				state.errorText = err.Error()
			}
		}},
		Label: "评论 Markdown 正文", ReadOnly: locked, ReadClipboard: s.markdownClipboardContent,
		OnChange: func(snapshot markdownui.Snapshot) { s.commitCommentMarkdownInput(state, owner, snapshot.Source) },
		OnSave: func(markdownui.Snapshot) {
			if !locked && s.captureCommentMarkdownInput(state) {
				s.startCommentComposerJob(state, "save", nil)
			}
		},
		OnCompositionChanged: func(composing bool) {
			if !composing && s.commentMarkdownCurrent(state, owner, state.draft.ID) {
				s.commitCommentMarkdownInput(state, owner, owner.componentEditor.Snapshot().Source)
			}
		},
		OnModeChanged: func(mode markdownui.Mode) {
			state.preview = mode == markdownui.Preview
			if mode == markdownui.Source {
				s.closeMarkdownBitmapViewerForOwner(owner)
			}
		},
		OnImportResources: func(_ context.Context, request markdownui.ImportRequest, complete func(markdownui.ImportResult, error)) {
			s.queueCommentMarkdownImport(state, owner, request, complete)
		},
	}
	markdownui.MarkdownEditor(c.Key("requirement-comment-markdown-"+state.draft.ID), owner.componentEditor, options).FillWidth().Grow(1).MinHeight(90).Padding(12).Background(p.soft).Border(1, p.line).Radius(8)
}

func (s *app) queueCommentMarkdownImport(state *requirementCommentComposer, owner *markdownEditorState, request markdownui.ImportRequest, complete func(markdownui.ImportResult, error)) {
	if !s.commentMarkdownCurrent(state, owner, state.draft.ID) || !state.open || state.draft.Pending() || state.importing || state.pendingImport != nil || state.running && state.operation != "save" {
		complete(markdownui.ImportResult{}, errors.New("请先完成当前评论操作"))
		return
	}
	state.pendingImport = &commentMarkdownImport{owner: owner.componentEditor, request: request, complete: complete, draftID: state.draft.ID, itemKey: state.draft.Item.Key()}
	if state.running {
		return
	}
	if state.dirty {
		s.startCommentComposerJob(state, "save", nil)
		return
	}
	s.startPendingCommentMarkdownImport(state)
}

func (s *app) commentMarkdownImportCurrent(state *requirementCommentComposer, pending *commentMarkdownImport) bool {
	return pending != nil && state.markdown != nil && s.commentMarkdownCurrent(state, state.markdown, pending.draftID) &&
		state.open && state.navigation == s.commentComposerNavigation() && state.draft.Item.Key() == pending.itemKey &&
		state.markdown.componentEditor == pending.owner && s.requirementUnit.state.commentComposers != nil && s.requirementUnit.state.commentComposers.active == state
}

func (s *app) startPendingCommentMarkdownImport(state *requirementCommentComposer) {
	pending := state.pendingImport
	if pending == nil || state.running || s.backend == nil {
		return
	}
	if !s.commentMarkdownImportCurrent(state, pending) || pending.owner.Snapshot().Version != pending.request.Version {
		state.pendingImport = nil
		pending.complete(markdownui.ImportResult{}, markdownui.ErrStale)
		return
	}
	state.pendingImport, state.importing, state.running = nil, true, true
	state.operation, state.errorText = "import-images", ""
	state.jobSerial++
	serial := state.jobSerial
	backend, win := s.backend, s.win
	draft := state.draft
	initialRevision := draft.Revision
	base := s.commentComposerPayload(state, "add-image")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	state.cancel, state.done = cancel, make(chan struct{})
	done := state.done
	go func() {
		defer close(done)
		defer cancel()
		result := markdownui.ImportResult{}
		files := append([]string(nil), pending.request.Files...)
		png := append([]byte(nil), pending.request.ImagePNG...)
		var err error
		if pending.request.Pick {
			files, err = mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: "添加图片", Multiple: true, Filters: []mygo.FileFilter{{Name: "图片", Extensions: []string{"png", "jpg", "jpeg", "gif", "webp"}}}})
		} else if len(files) == 0 && len(png) == 0 {
			var clip documents.ClipboardPayload
			clip, err = documents.NewClipboardService().Read(ctx)
			png = clip.ImagePNG
			if err == nil && len(png) == 0 {
				err = errors.New("剪贴板中没有截图，请复制图片后重试")
			}
		}
		count := len(files)
		if count == 0 && len(png) > 0 {
			count = 1
		}
		if count+len(draft.ActiveImages()) > 20 {
			err = errors.New("每条评论最多添加 20 张图片")
		}
		for i := 0; i < count && err == nil; i++ {
			p := map[string]any{}
			for k, value := range base {
				p[k] = value
			}
			p["revision"], p["inlineImage"], p["pendingImages"] = draft.Revision, true, i+1
			if len(files) > 0 {
				p["sourcePath"] = files[i]
			} else {
				p["operation"], p["imagePNG"] = "paste", png
			}
			previous := draft
			var response application.ActionResult
			response, err = backend.Dispatch(ctx, application.Action{Kind: "requirements.comment-draft", Payload: p})
			if err != nil {
				break
			}
			var raw []byte
			raw, err = json.Marshal(response.Data)
			var next domain.RequirementCommentDraft
			if err == nil {
				err = json.Unmarshal(raw, &next)
			}
			if err == nil && (next.ID != previous.ID || next.Identity != previous.Identity || next.Scope != previous.Scope || next.Item.Key() != previous.Item.Key() || next.Editing() != previous.Editing() || next.Editing() && next.Edit.CommentID != previous.Edit.CommentID || next.Revision <= previous.Revision || len(next.Images) != len(previous.Images)+1) {
				err = errors.New("图片草稿返回的目标或版本不匹配，当前输入已保留")
			}
			if err != nil {
				break
			}
			draft = next
			a := draft.Images[len(draft.Images)-1]
			if a.MarkdownRef != domain.CommentImageMarkdownRef(a.ID) {
				err = errors.New("图片草稿缺少安全的正文引用")
				break
			}
			result.Resources = append(result.Resources, markdownui.ImportedResource{Target: a.MarkdownRef, Text: a.Name, Image: true})
		}
		s.commentComposerUpdate(func() {
			if state.jobSerial != serial {
				pending.complete(markdownui.ImportResult{}, markdownui.ErrStale)
				return
			}
			state.running, state.importing, state.cancel = false, false, nil
			// Metadata/cache survives a late or partially failed import. It is
			// inactive until a registered reference is inserted (or restored).
			receiptCurrent := state.draft.ID == draft.ID && state.draft.Revision == initialRevision && draft.Revision >= initialRevision && commentComposerDraftMatches(state, draft)
			if receiptCurrent {
				state.draft = draft
				if state.markdown != nil {
					state.markdown.trustedImages = commentMarkdownTrustedImages(draft)
				}
			}
			if !receiptCurrent || !s.commentMarkdownImportCurrent(state, pending) || pending.owner.Snapshot().Version != pending.request.Version {
				err = fmt.Errorf("%w：图片已保留在本地草稿，当前输入未被替换", markdownui.ErrStale)
			}
			pending.complete(result, err)
			if err != nil {
				state.errorText = err.Error()
			}
			state.dirty = state.body != state.draft.Content
			if state.dirty && s.commentComposerCurrent(state) {
				s.startCommentComposerJob(state, "save", nil)
			}
		})
	}()
}

func (s *app) requestCommentImagePicker(state *requirementCommentComposer) {
	if !s.captureCommentMarkdownInput(state) {
		return
	}
	owner := s.ensureCommentMarkdownEditor(state)
	if owner != nil {
		owner.componentEditor.ImportResources(markdownui.ImportRequest{Pick: true})
	}
}

func (s *app) requestCommentScreenshot(state *requirementCommentComposer) {
	if !s.captureCommentMarkdownInput(state) {
		return
	}
	owner := s.ensureCommentMarkdownEditor(state)
	if owner != nil {
		// The empty request captures the insertion point before the async
		// clipboard read. Only the explicitly requested screenshot is staged.
		owner.componentEditor.ImportResources(markdownui.ImportRequest{})
	}
}

func commentImportHasResources(state *requirementCommentComposer) bool {
	return state != nil && (state.importing || state.pendingImport != nil)
}
