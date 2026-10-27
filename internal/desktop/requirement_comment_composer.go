package desktop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"golang.org/x/image/draw"
	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
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
	paths                                         []string
	cancel                                        context.CancelFunc
	done                                          chan struct{}
	thumbnails                                    map[string]*ui.Bitmap
	clipboardReading                              bool
	pendingScreenshot                             []byte
	imageFocusKey                                 string
	restoreImageFocus                             bool
}

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
	if s.requirementContentJobs != nil {
		key = s.requirementContentJobs.visible
	}
	return s.page + "\x00" + key
}

func (s *app) commentComposerCurrent(state *requirementCommentComposer) bool {
	if state == nil || state.home != s.snapshot.Home || state.config != s.commentComposerConfig() || state.profile != s.currentRequirementIdentityContext() {
		return false
	}
	if identity := s.requirementIdentityJobs; state.account != "" && identity != nil && identity.confirmed && (identity.identity != state.account || identity.state != application.RequirementIdentityAuthenticated) {
		return false
	}
	return true
}

func (s *app) commentComposerUpdate(fn func()) {
	if s.commentComposers != nil && s.commentComposers.update != nil {
		s.commentComposers.update(fn)
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
	if s.commentComposers == nil {
		s.commentComposers = &requirementCommentComposers{}
	}
	for _, state := range s.commentComposers.all {
		if state.editCommentID == commentID && sameRequirementReaderLink(state.url, reader.url) && s.commentComposerCurrent(state) {
			if old := s.commentComposers.active; old != nil {
				old.open = false
			}
			state.open = true
			state.navigation = s.commentComposerNavigation()
			s.commentComposers.active = state
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
	if id := s.requirementIdentityJobs; id != nil && id.confirmed {
		state.account = id.identity
	}
	if old := s.commentComposers.active; old != nil {
		old.open = false
	}
	s.commentComposers.all = append(s.commentComposers.all, state)
	s.commentComposers.active = state
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
	if op == "publish" && state.editCommentID != "" && !commentComposerCanPublish(state) {
		return
	}
	state.running = true
	state.errorText = ""
	state.progress = domain.CommentPublishProgress{}
	state.operation = op
	p := s.commentComposerPayload(state, op)
	for k, v := range extra {
		p[k] = v
	}
	sentBody := state.body
	if op == "save" {
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
					if s.commentComposerCurrent(state) && state.running {
						state.progress = progress
					}
				})
			})
		} else {
			result, err = backend.Dispatch(ctx, action)
		}
		s.commentComposerUpdate(func() {
			state.running = false
			state.cancel = nil
			// Old results belong to the original draft only. Do not retarget a
			// reader, toast, or dialog after an account/home/config switch.
			current := s.commentComposerCurrent(state)
			if !current {
				state.open = false
			}
			if err != nil {
				state.errorText = err.Error()
				state.paths = nil
				return
			}
			raw, e := json.Marshal(result.Data)
			if e != nil {
				state.errorText = e.Error()
				return
			}
			var draft domain.RequirementCommentDraft
			if op == "publish" || op == "check" {
				var published domain.CommentPublishResult
				if e = json.Unmarshal(raw, &published); e != nil {
					state.errorText = e.Error()
					return
				}
				draft = published.Draft
				if !commentComposerDraftMatches(state, draft) {
					state.errorText = "评论编辑目标已变化，请重新打开核对"
					return
				}
				state.status = published.Message
				if published.Status == "published" || published.Status == "updated" {
					state.body = ""
					state.dirty = false
					state.open = false
					if current {
						s.lastMessage = "评论已发布"
						if state.editCommentID != "" {
							s.lastMessage = "评论已修改"
						}
						s.refreshPublishedRequirementComments(state.url)
					}
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
			state.draft = draft
			if draft.Pending() && draft.Attempt.Error != "" {
				state.errorText = draft.Attempt.Error
			}
			state.loaded = true
			if op == "discard" {
				state.open, state.loaded = false, false
			}
			state.account = draft.Identity
			if op == "save" {
				state.dirty = state.body != sentBody
			} else {
				state.body = draft.Content
				state.dirty = false
			}
			if !current {
				state.paths = nil
				return
			}
			s.queueCommentThumbnails(state)
			if op == "allow-retry" {
				s.startCommentComposerJob(state, "publish", nil)
				return
			}
			if len(state.pendingScreenshot) > 0 && !state.dirty {
				data := state.pendingScreenshot
				state.pendingScreenshot = nil
				s.startCommentComposerJob(state, "paste", map[string]any{"imagePNG": data})
				return
			}
			if len(state.paths) > 0 {
				path := state.paths[0]
				state.paths = state.paths[1:]
				s.startCommentComposerJob(state, "add-image", map[string]any{"sourcePath": path})
				return
			}
			if state.dirty {
				s.startCommentComposerJob(state, "save", nil)
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
	if s.commentComposers == nil {
		return
	}
	for _, state := range s.commentComposers.all {
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
		if !state.running && !state.dirty && state.errorText == "" && len(state.pendingScreenshot) > 0 {
			data := state.pendingScreenshot
			state.pendingScreenshot = nil
			s.startCommentComposerJob(state, "paste", map[string]any{"imagePNG": data})
		}
	}
	if viewer := s.markdownBitmapViewer; viewer != nil && viewer.composer != nil && !s.commentComposerImageCurrent(viewer) {
		s.dismissMarkdownBitmapViewer()
	}
}

func isCommentDraftMarkdownOwner(owner *markdownEditorState) bool {
	return owner != nil && strings.HasPrefix(owner.taskID, "comment-draft:")
}

func (s *app) commentComposerImageCurrent(viewer *markdownBitmapViewerState) bool {
	if viewer == nil || viewer.composer == nil || s.commentComposers == nil {
		return false
	}
	state := viewer.composer
	return s.commentComposers.active == state && state.open && state.preview && state.markdown == viewer.owner &&
		s.commentComposerCurrent(state) && state.navigation == s.commentComposerNavigation()
}

func (s *app) queueCommentThumbnails(state *requirementCommentComposer) {
	if s.win == nil {
		return
	}
	if state.thumbnails == nil {
		state.thumbnails = map[string]*ui.Bitmap{}
	}
	for _, a := range state.draft.Images {
		if _, exists := state.thumbnails[a.ID]; exists || a.LocalPath == "" {
			continue
		}
		state.thumbnails[a.ID] = nil
		go func(asset domain.CommentDraftAsset) {
			file, err := os.Open(asset.LocalPath)
			if err != nil {
				return
			}
			defer file.Close()
			img, _, err := image.Decode(file)
			if err != nil {
				return
			}
			bounds := img.Bounds()
			w, h := bounds.Dx(), bounds.Dy()
			if w <= 0 || h <= 0 {
				return
			}
			scale := min(float64(160)/float64(w), float64(100)/float64(h), float64(1))
			thumb := image.NewRGBA(image.Rect(0, 0, max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))))
			draw.CatmullRom.Scale(thumb, thumb.Bounds(), img, bounds, draw.Over, nil)
			bitmap := ui.NewBitmap(thumb)
			s.commentComposerUpdate(func() {
				if !s.commentComposerCurrent(state) {
					return
				}
				for _, current := range state.draft.Images {
					if current.ID == asset.ID && current.Hash == asset.Hash {
						state.thumbnails[asset.ID] = bitmap
						break
					}
				}
			})
		}(a)
	}
}

func (s *app) refreshPublishedRequirementComments(link string) {
	for _, reader := range s.requirementReaders {
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
	if state.running || state.dirty || !s.commentComposerCurrent(state) {
		return
	}
	state.running = true
	window := s.win
	go func() {
		opts := mygo.OpenDialogOptions{Parent: window, Title: "添加附件", Multiple: images}
		if images {
			opts.Title = "添加图片"
			opts.Filters = []mygo.FileFilter{{Name: "图片", Extensions: []string{"png", "jpg", "jpeg", "gif", "webp"}}}
		}
		paths, err := mygo.Dialog.Open(opts)
		s.commentComposerUpdate(func() {
			state.running = false
			if !s.commentComposerCurrent(state) {
				return
			}
			if err != nil {
				state.errorText = err.Error()
				return
			}
			if len(paths) == 0 {
				return
			}
			op := "add-attachment"
			if images {
				op = "add-image"
				state.paths = paths[1:]
			}
			s.startCommentComposerJob(state, op, map[string]any{"sourcePath": paths[0]})
		})
	}()
}

func (s *app) pasteCommentScreenshot(state *requirementCommentComposer) {
	if state.clipboardReading || state.draft.Pending() || state.running && state.operation != "save" || !s.commentComposerCurrent(state) {
		return
	}
	state.clipboardReading = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		clip, err := documents.NewClipboardService().Read(ctx)
		s.commentComposerUpdate(func() {
			state.clipboardReading = false
			if !s.commentComposerCurrent(state) {
				return
			}
			if err != nil {
				state.errorText = err.Error()
				return
			}
			if len(clip.ImagePNG) == 0 {
				state.errorText = "剪贴板中没有截图，请复制图片后重试"
				return
			}
			state.pendingScreenshot = clip.ImagePNG
			if !state.running && !state.dirty {
				data := state.pendingScreenshot
				state.pendingScreenshot = nil
				s.startCommentComposerJob(state, "paste", map[string]any{"imagePNG": data})
			} else if !state.running {
				s.startCommentComposerJob(state, "save", nil)
			}
		})
	}()
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
	if s.commentComposers == nil {
		return nil
	}
	for _, state := range s.commentComposers.all {
		if state.running || state.clipboardReading || len(state.pendingScreenshot) > 0 {
			return fmt.Errorf("评论草稿或发布操作正在执行，请完成后再关闭；可先收起弹窗")
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
	if state == nil || !state.loaded || state.running || state.clipboardReading || len(state.pendingScreenshot) != 0 || state.dirty || state.draft.Pending() {
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

func (s *app) closeRequirementCommentComposers(ctx context.Context) error {
	if s.commentComposers == nil {
		return nil
	}
	for _, state := range s.commentComposers.all {
		if state.cancel != nil {
			state.cancel()
		}
	}
	for _, state := range s.commentComposers.all {
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
