package desktop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	markdownui "silverwing-go/internal/ui/markdown"
)

type requirementCommentComposer struct {
	open, loaded, running, dirty, preview         bool
	url, home, config, account, navigation        string
	editCommentID                                 string
	profile                                       requirementIdentityContext
	draft                                         domain.RequirementCommentDraft
	body, errorText, status, requestID, operation string
	progress                                      domain.CommentPublishProgress
	markdown                                      *markdownEditorState
	cancel                                        context.CancelFunc
	done                                          chan struct{}
	imageFocusKey                                 string
	restoreImageFocus                             bool
	capturingInput, importing                     bool
	pendingImport                                 *commentMarkdownImport
	publishAfterSave                              *commentComposerPublishIntent
	jobSerial                                     uint64
}

type commentComposerPublishIntent struct{ draftID, body string }

type requirementCommentComposers struct {
	active *requirementCommentComposer
	all    []*requirementCommentComposer
	update func(func()) // Window-thread scheduler seam for deterministic tests.
}

func newCommentRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func (s *app) commentComposerConfig() string {
	return fmt.Sprint(s.snapshot.Home, s.configString("meegleExecutablePath"), s.readConfigArray("meegleProjects"), s.configString("commandProxyUrl"), s.configString("commandProxyNoProxy"), s.configString("commandProxyUsername"), s.configString("commandProxyPassword"), s.readConfigArray("commandProxyTargets"))
}

func (s *app) commentComposerNavigation() string {
	key := ""
	if s.requirementUnit.state.requirementContentJobs != nil {
		key = s.requirementUnit.state.requirementContentJobs.visible
	}
	return s.page + "\x00" + key
}

func (s *app) commentComposerCurrent(state *requirementCommentComposer) bool {
	if state == nil || state.home != s.snapshot.Home || state.config != s.commentComposerConfig() || state.profile != s.currentRequirementIdentityContext() {
		return false
	}
	if identity := s.requirementUnit.state.requirementIdentityJobs; state.account != "" && identity != nil && identity.confirmed && (identity.identity != state.account || identity.state != application.RequirementIdentityAuthenticated) {
		return false
	}
	return true
}

func (s *app) commentComposerUpdate(fn func()) {
	if s.requirementUnit.state.commentComposers != nil && s.requirementUnit.state.commentComposers.update != nil {
		s.requirementUnit.state.commentComposers.update(fn)
		return
	}
	if s.win != nil && !s.win.IsDestroyed() {
		s.win.Update(fn)
	}
}

func (s *app) openRequirementCommentComposer(reader *requirementReaderState) {
	s.openRequirementCommentTarget(reader, "")
}

func (s *app) openRequirementCommentEditor(reader *requirementReaderState, commentID string) {
	if strings.TrimSpace(commentID) == "" {
		return
	}
	s.openRequirementCommentTarget(reader, commentID)
}

func (s *app) openRequirementCommentTarget(reader *requirementReaderState, commentID string) {
	if reader == nil || domain.ParseFeishuWorkItemLink(reader.url) == nil || s.backend == nil {
		return
	}
	if s.requirementUnit.state.commentComposers == nil {
		s.requirementUnit.state.commentComposers = &requirementCommentComposers{}
	}
	for _, state := range s.requirementUnit.state.commentComposers.all {
		if state.editCommentID == commentID && sameRequirementReaderLink(state.url, reader.url) && s.commentComposerCurrent(state) {
			if old := s.requirementUnit.state.commentComposers.active; old != nil {
				old.open = false
			}
			state.open = true
			state.navigation = s.commentComposerNavigation()
			s.requirementUnit.state.commentComposers.active = state
			if (!state.loaded || commentID != "" && !state.dirty) && !state.running {
				s.startCommentComposerJob(state, "load", nil)
			}
			return
		}
	}
	state := &requirementCommentComposer{open: true, editCommentID: commentID, url: reader.url, home: s.snapshot.Home, config: s.commentComposerConfig(), profile: s.currentRequirementIdentityContext(), navigation: s.commentComposerNavigation()}
	if req, ok := s.requirementForURL(reader.url); ok {
		state.draft.Item = req.WorkItem
	} else if reader.body != nil {
		state.draft.Item = domain.ParticipatedWorkItem{URL: reader.url, Title: reader.body.Title}
	}
	if id := s.requirementUnit.state.requirementIdentityJobs; id != nil && id.confirmed {
		state.account = id.identity
	}
	if old := s.requirementUnit.state.commentComposers.active; old != nil {
		old.open = false
	}
	s.requirementUnit.state.commentComposers.all = append(s.requirementUnit.state.commentComposers.all, state)
	s.requirementUnit.state.commentComposers.active = state
	s.startCommentComposerJob(state, "load", nil)
}

func (s *app) commentComposerPayload(state *requirementCommentComposer, op string) map[string]any {
	p := map[string]any{"url": state.url, "operation": op, "draftID": state.draft.ID, "revision": state.draft.Revision, "scope": state.draft.Scope}
	if state.editCommentID != "" {
		p["editCommentID"] = state.editCommentID
	}
	if state.account != "" {
		p["expectedIdentity"] = state.account
	}
	if state.loaded || state.draft.Item.URL != "" {
		raw, _ := json.Marshal(state.draft.Item)
		var item map[string]any
		_ = json.Unmarshal(raw, &item)
		p["item"] = item
	}
	return p
}

func (s *app) startCommentComposerJob(state *requirementCommentComposer, op string, extra map[string]any) {
	if state == nil || state.running || s.backend == nil || !s.commentComposerCurrent(state) {
		return
	}
	if op != "load" && !state.loaded {
		return
	}
	if state.loaded && op != "check" && !s.captureCommentMarkdownInput(state) {
		return
	}
	if op == "publish" && state.dirty {
		if state.draft.Pending() || commentImportHasResources(state) {
			return
		}
		state.publishAfterSave = &commentComposerPublishIntent{draftID: state.draft.ID, body: state.body}
		s.startCommentComposerJob(state, "save", nil)
		return
	}
	if op == "publish" && !commentComposerCanPublish(state) {
		return
	}
	if op == "load" && state.loaded && state.dirty {
		s.startCommentComposerJob(state, "save", nil)
		return
	}
	if state.draft.Pending() && op != "load" && op != "check" && op != "allow-retry" {
		return
	}
	state.running = true
	state.errorText = ""
	state.progress = domain.CommentPublishProgress{}
	state.operation = op
	state.jobSerial++
	serial, sentDraftID, sentRevision := state.jobSerial, state.draft.ID, state.draft.Revision
	p := s.commentComposerPayload(state, op)
	for k, v := range extra {
		p[k] = v
	}
	sentBody := state.body
	if op == "save" || op == "add-attachment" || op == "add-image" || op == "paste" || op == "remove" {
		p["content"] = sentBody
	}
	kind := "requirements.comment-draft"
	if op == "publish" {
		kind = "requirements.comments/create"
		if state.editCommentID != "" {
			kind = "requirements.comments/update"
		}
		state.requestID = newCommentRequestID()
		p["requestID"] = state.requestID
	}
	if op == "check" {
		kind = "requirements.comments/check"
	}
	action := application.Action{Kind: kind, Payload: p}
	backend := s.backend
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	state.cancel = cancel
	state.done = make(chan struct{})
	done := state.done
	go func() {
		defer close(done)
		defer cancel()
		var result application.ActionResult
		var err error
		if publishing, ok := backend.(application.RequirementCommentPublishBackend); ok && op == "publish" {
			result, err = publishing.DispatchRequirementComment(ctx, action, func(progress domain.CommentPublishProgress) {
				s.commentComposerUpdate(func() {
					if s.commentComposerCurrent(state) && state.running && state.jobSerial == serial {
						state.progress = progress
					}
				})
			})
		} else {
			result, err = backend.Dispatch(ctx, action)
		}
		s.commentComposerUpdate(func() {
			if state.jobSerial != serial {
				return
			}
			state.running = false
			state.cancel = nil
			// Old results belong to the original draft only. Do not retarget a
			// reader, toast, or dialog after an account/home/config switch.
			current := s.commentComposerCurrent(state)
			if !current {
				state.open = false
				state.publishAfterSave = nil
				if pending := state.pendingImport; pending != nil {
					state.pendingImport = nil
					pending.complete(markdownui.ImportResult{}, markdownui.ErrStale)
				}
				return
			}
			if err != nil {
				state.errorText = err.Error()
				state.publishAfterSave = nil
				if pending := state.pendingImport; pending != nil {
					state.pendingImport = nil
					pending.complete(markdownui.ImportResult{}, err)
				}
				return
			}
			raw, e := json.Marshal(result.Data)
			if e != nil {
				state.errorText = e.Error()
				return
			}
			var draft domain.RequirementCommentDraft
			publishedOK := false
			if op == "publish" || op == "check" {
				var published domain.CommentPublishResult
				if e = json.Unmarshal(raw, &published); e != nil {
					state.errorText = e.Error()
					return
				}
				draft = published.Draft
				if !commentComposerDraftMatches(state, draft) || draft.ID != sentDraftID || draft.Revision < sentRevision {
					state.errorText = "评论编辑目标已变化，请重新打开核对"
					return
				}
				state.status = published.Message
				if published.Status == "published" || published.Status == "updated" {
					publishedOK = true
				} else {
					state.errorText = published.Message
				}
			} else if e = json.Unmarshal(raw, &draft); e != nil {
				state.errorText = e.Error()
				return
			}
			if !commentComposerDraftMatches(state, draft) {
				state.errorText = "评论草稿返回的账号或需求不匹配，请重新读取草稿核对"
				return
			}
			if state.draft.ID != sentDraftID || state.draft.Revision != sentRevision || op != "load" && op != "reload" && op != "clear" && op != "discard" && draft.ID != sentDraftID || draft.Revision < sentRevision || op != "load" && draft.Scope != state.draft.Scope {
				state.errorText = "评论草稿版本已变化，当前输入已保留，请重新核对"
				state.publishAfterSave = nil
				return
			}
			// Observe any newer committed native input before accepting a receipt.
			if current && state.markdown != nil && state.markdown.componentEditor != nil && !state.markdown.live.Composing() {
				state.capturingInput = true
				s.commitCommentMarkdownInput(state, state.markdown, state.markdown.componentEditor.Snapshot().Source)
				state.capturingInput = false
			}
			newerInput := state.body != sentBody || state.markdown != nil && state.markdown.live != nil && state.markdown.live.Composing()
			state.draft = draft
			if draft.Pending() && draft.Attempt.Error != "" {
				state.errorText = draft.Attempt.Error
			}
			state.loaded = true
			if op == "discard" && !newerInput {
				state.open, state.loaded = false, false
			}
			state.account = draft.Identity
			if (op == "load" || op == "reload" || op == "clear" || op == "discard" || op == "check" || op == "publish" || op == "allow-retry") && !newerInput {
				state.body = draft.Content
				if op == "load" || op == "reload" || op == "clear" || op == "discard" || publishedOK {
					s.acceptCommentMarkdownRead(state, draft.Content)
				}
			}
			state.dirty = state.body != draft.Content
			if publishedOK {
				if !newerInput {
					state.open = false
				}
				if current {
					s.lastMessage = "评论已发布"
					if state.editCommentID != "" {
						s.lastMessage = "评论已修改"
					}
					s.refreshPublishedRequirementComments(state.url)
				}
			}
			if op == "allow-retry" {
				s.startCommentComposerJob(state, "publish", nil)
				return
			}
			if state.dirty {
				s.startCommentComposerJob(state, "save", nil)
				return
			}
			if intent := state.publishAfterSave; intent != nil {
				state.publishAfterSave = nil
				if state.draft.ID == intent.draftID && state.body == intent.body && !newerInput {
					s.startCommentComposerJob(state, "publish", nil)
				} else {
					state.errorText = "评论输入已变化，请再次点击发布或保存修改"
				}
				return
			}
			if state.pendingImport != nil {
				s.startPendingCommentMarkdownImport(state)
			}
		})
	}()
}

func commentComposerDraftMatches(state *requirementCommentComposer, draft domain.RequirementCommentDraft) bool {
	id := ""
	if draft.Edit != nil {
		id = draft.Edit.CommentID
	}
	return id == state.editCommentID && draft.ID != "" && draft.Identity != "" && sameRequirementReaderLink(draft.Item.URL, state.url) &&
		(!state.loaded || draft.Item.Key() == state.draft.Item.Key()) && (state.account == "" || state.account == draft.Identity)
}

func (s *app) observeRequirementCommentComposers() {
	if s.requirementUnit.state.commentComposers == nil {
		return
	}
	for _, state := range s.requirementUnit.state.commentComposers.all {
		if !s.commentComposerCurrent(state) {
			state.open = false
			if state.cancel != nil {
				state.cancel()
			}
			continue
		}
		if state.navigation != s.commentComposerNavigation() {
			state.open = false
		}
		if state.dirty && state.errorText == "" && !state.running {
			s.startCommentComposerJob(state, "save", nil)
		}
		if !state.running && !state.dirty && state.errorText == "" && state.pendingImport != nil {
			s.startPendingCommentMarkdownImport(state)
		}
	}
	if viewer := s.materialsUnit.state.markdownBitmapViewer; viewer != nil && viewer.composer != nil && !s.commentComposerImageCurrent(viewer) {
		s.dismissMarkdownBitmapViewer()
	}
	if viewer := s.materialsUnit.state.markdownLinkFile; viewer != nil && viewer.composer != nil && !s.markdownLinkFileCurrent(viewer) {
		s.closeMarkdownLinkFilePreview(viewer)
	}
}

func isCommentDraftMarkdownOwner(owner *markdownEditorState) bool {
	return owner != nil && strings.HasPrefix(owner.taskID, "comment-draft:")
}

func (s *app) commentComposerImageCurrent(viewer *markdownBitmapViewerState) bool {
	if viewer == nil || viewer.composer == nil || s.requirementUnit.state.commentComposers == nil {
		return false
	}
	state := viewer.composer
	sourceAllowed := state.markdown == nil || state.markdown.componentEditor == nil || state.markdown.componentEditor.Mode() != markdownui.Source
	if !sourceAllowed && viewer.sourcePath != "" {
		for _, asset := range commentLegacyPendingImages(state.draft) {
			if asset.LocalPath != "" && sameInstructionPath(asset.LocalPath, viewer.sourcePath) {
				sourceAllowed = true
				break
			}
		}
	}
	return s.requirementUnit.state.commentComposers.active == state && state.open && state.markdown != nil && state.markdown == viewer.owner &&
		state.markdown.taskID == "comment-draft:"+state.draft.ID && sourceAllowed &&
		s.commentComposerCurrent(state) && state.navigation == s.commentComposerNavigation()
}

func (s *app) refreshPublishedRequirementComments(link string) {
	for _, reader := range s.requirementUnit.state.requirementReaders {
		if !sameRequirementReaderLink(reader.url, link) {
			continue
		}
		// Only the comment slice changes. Body/source/find/scroll stay intact.
		reader.commentsDirty = true
		s.retireRequirementReaderRequest(reader, "requirements.comments")
		reader.commentsLoading = false
		s.requestRequirementContent(reader, true, true)
	}
}

func (s *app) chooseCommentFiles(state *requirementCommentComposer, images bool) {
	if images {
		s.requestCommentImagePicker(state)
		return
	}
	if state.running || state.dirty || !s.commentComposerCurrent(state) {
		return
	}
	if !s.captureCommentMarkdownInput(state) || state.dirty {
		if state.dirty {
			s.startCommentComposerJob(state, "save", nil)
		}
		return
	}
	state.running = true
	state.operation = "choose-attachment"
	state.jobSerial++
	serial := state.jobSerial
	draftID, revision := state.draft.ID, state.draft.Revision
	window := s.win
	go func() {
		opts := mygo.OpenDialogOptions{Parent: window, Title: "添加附件"}
		paths, err := mygo.Dialog.Open(opts)
		s.commentComposerUpdate(func() {
			if state.jobSerial != serial {
				return
			}
			state.running = false
			if !s.commentComposerCurrent(state) || state.draft.ID != draftID || state.draft.Revision != revision {
				return
			}
			if err != nil {
				state.errorText = err.Error()
				return
			}
			if len(paths) == 0 {
				return
			}
			s.startCommentComposerJob(state, "add-attachment", map[string]any{"sourcePath": paths[0]})
		})
	}()
}

func (s *app) pasteCommentScreenshot(state *requirementCommentComposer) {
	if state == nil || state.draft.Pending() || state.running && state.operation != "save" || !s.commentComposerCurrent(state) {
		return
	}
	s.requestCommentScreenshot(state)
}

func (s *app) confirmCommentDraftChange(state *requirementCommentComposer, retry bool) {
	title, message, button := "清空草稿", "将清空本地评论文字、图片和附件，原始文件保留。", "清空草稿"
	op := "clear"
	if state.editCommentID != "" {
		if retry {
			return
		}
		title, message, button, op = "放弃修改", "仅清除这条评论的本地编辑稿，远端评论保留。", "放弃修改", "discard"
	}
	attemptID := ""
	if retry {
		title, message, button, op = "重新发布评论", "请先打开原需求确认本条评论尚未发布。若已经发布，再次发布会产生重复评论。", "确认未发布并重试", "allow-retry"
		if state.draft.Attempt != nil {
			attemptID = state.draft.Attempt.ID
		}
	}
	draftID, revision := state.draft.ID, state.draft.Revision
	if s.showConfirmationDialog(title, message, state.draft.Item.Title, []confirmationDialogButton{{label: button, primary: true}, {label: "取消"}}, 1, 1, func(index int) {
		if index == 0 && !state.running && s.commentComposerCurrent(state) && state.draft.ID == draftID && state.draft.Revision == revision {
			s.startCommentComposerJob(state, op, map[string]any{"confirmed": true, "attemptID": attemptID})
		}
	}) {
		s.confirmationDialog.Current = func() bool {
			return state.open && s.commentComposerCurrent(state) && state.draft.ID == draftID && state.draft.Revision == revision
		}
	}
}

func (s *app) commentComposerCloseError() error {
	if s.requirementUnit.state.commentComposers == nil {
		return nil
	}
	for _, state := range s.requirementUnit.state.commentComposers.all {
		if state.running || commentImportHasResources(state) {
			return fmt.Errorf("评论草稿或发布操作正在执行，请完成后再关闭；可先收起弹窗")
		}
		if s.commentComposerCurrent(state) && !s.captureCommentMarkdownInput(state) {
			return fmt.Errorf("评论输入尚未确认，请处理编辑错误后再关闭；当前输入已保留")
		}
		if state.dirty {
			if s.commentComposerCurrent(state) {
				s.startCommentComposerJob(state, "save", nil)
			}
			return fmt.Errorf("评论草稿尚未保存，请处理保存错误后再关闭；当前输入已保留")
		}
	}
	return nil
}

func commentComposerCanPublish(state *requirementCommentComposer) bool {
	if state == nil || !state.loaded || state.running || state.dirty || state.draft.Pending() || commentImportHasResources(state) {
		return false
	}
	d := state.draft
	d.Content = state.body
	return d.HasContent() && (state.editCommentID == "" || d.HasChanges() && d.Edit != nil && !d.Edit.Conflict)
}

func (s *app) confirmReloadCommentEdit(state *requirementCommentComposer) {
	if state.editCommentID == "" || state.running || state.draft.Pending() {
		return
	}
	draftID, revision := state.draft.ID, state.draft.Revision
	if s.showConfirmationDialog("重新载入原评论", "将放弃这条评论的本地修改，读取远端最新内容。", state.draft.Item.Title, []confirmationDialogButton{{label: "重新载入", primary: true}, {label: "取消"}}, 1, 1, func(index int) {
		if index == 0 && !state.running && !state.dirty && s.commentComposerCurrent(state) && state.draft.ID == draftID && state.draft.Revision == revision {
			s.startCommentComposerJob(state, "reload", map[string]any{"confirmed": true})
		}
	}) {
		s.confirmationDialog.Current = func() bool {
			return state.open && s.commentComposerCurrent(state) && state.draft.ID == draftID && state.draft.Revision == revision
		}
	}
}

func (s *requirementController) closeRequirementCommentComposers(ctx context.Context) error {
	if s.state.commentComposers == nil {
		return nil
	}
	for _, state := range s.state.commentComposers.all {
		if state.cancel != nil {
			state.cancel()
		}
	}
	for _, state := range s.state.commentComposers.all {
		if state.done != nil {
			select {
			case <-state.done:
			case <-ctx.Done():
				return fmt.Errorf("等待需求评论任务清理：%w", ctx.Err())
			}
		}
		closeMarkdownPreviewState(state.markdown)
	}
	return nil
}
