package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"

	"github.com/egoist/mygo/ui"
)

type requirementReaderState struct {
	taskID, url            string
	epoch, requestID       uint64
	requestIDs             map[string]uint64
	body                   *application.RequirementContent
	comments               *application.RequirementComments
	bodyMarkdown           *markdownEditorState
	commentsMarkdown       *markdownEditorState
	commentMarkdown        map[string]*markdownEditorState
	sharedReader           *readonlyMarkdownReaderState
	contextMenu            func(*ui.Context, *ui.Menu)
	bodyLoading            bool
	commentsLoading        bool
	commentsDirty          bool
	bodyError              string
	commentsError          string
	showingComments        bool
	resourceLoading        string
	resourceErrors         map[string]string
	commentResourceLoading string
	commentResourceErrors  map[string]string
	commentResourceRetries map[string]bool
	refreshAssetIndex      bool
	bodyScroll             ui.ScrollState
	commentsScroll         ui.ScrollState
}

func (s *app) requirementReaderScroll(reader *requirementReaderState, comments bool) *ui.ScrollState {
	if reader.taskID != "" {
		id := "requirement-body"
		if comments {
			id = "requirement-comments"
		}
		return s.readingScrollState(id)
	}
	if comments {
		return &reader.commentsScroll
	}
	return &reader.bodyScroll
}

func requirementReaderKey(taskID, link string) string {
	return taskID + "\x00" + strings.TrimSpace(link)
}

// Share only the same parsed space/type/ID. The existing parser normalizes
// case, trailing punctuation and query/fragment variants of work-item links.
func sameRequirementReaderLink(left, right string) bool {
	a, b := domain.ParseFeishuWorkItemLink(left), domain.ParseFeishuWorkItemLink(right)
	if a != nil && b != nil {
		return a.Space == b.Space && a.Kind == b.Kind && a.WorkItemID == b.WorkItemID
	}
	return strings.TrimSpace(left) == strings.TrimSpace(right)
}

func requirementContentAction(kind string) bool {
	return kind == "requirements.read" || kind == "requirements.comments" || kind == "requirements.attachment" || kind == "requirements.copy-document" || kind == "requirements.copy-comments"
}

func (s *app) requirementReader(taskID, link string) *requirementReaderState {
	if s.requirementReaders == nil {
		s.requirementReaders = make(map[string]*requirementReaderState)
	}
	key := requirementReaderKey(taskID, link)
	if reader := s.requirementReaders[key]; reader != nil {
		return reader
	}
	reader := &requirementReaderState{taskID: taskID, url: strings.TrimSpace(link), epoch: s.requirementReaderGeneration, resourceErrors: make(map[string]string)}
	s.requirementReaders[key] = reader
	return reader
}

func (s *app) closeRequirementReaders() {
	s.cancelRequirementContentReads()
	if state := s.requirementContentJobs; state != nil {
		state.entered = nil
	}
	for _, reader := range s.requirementReaders {
		closeMarkdownPreviewState(reader.bodyMarkdown)
		closeMarkdownPreviewState(reader.commentsMarkdown)
		closeRequirementCommentMarkdownStates(reader)
		if reader.sharedReader != nil {
			reader.sharedReader.find.close()
			reader.sharedReader.findOpen = false
		}
	}
	s.requirementReaders = nil
}

func (s *app) requirementReaderForAction(action application.Action) *requirementReaderState {
	reader := s.requirementReaders[payloadString(action.Payload, "readerKey")]
	kind := requirementContentRequestKind(action)
	if reader == nil || reader.epoch != s.requirementReaderGeneration || reader.requestIDs[kind] == 0 || fmt.Sprint(reader.requestIDs[kind]) != payloadString(action.Payload, "readerRequestID") {
		return nil
	}
	if action.Kind == "requirements.attachment" {
		if payloadString(action.Payload, "scope") == "comment" {
			if !requirementCommentResourceCurrent(reader, action) {
				return nil
			}
		} else if reader.body == nil || reader.body.Generation != payloadString(action.Payload, "generation") {
			return nil
		}
	}
	return reader
}

func (s *app) requirementReaderPayload(reader *requirementReaderState, kind string, force bool) map[string]any {
	if force && (kind == "requirements.read" || kind == "requirements.comments") {
		// A newer explicit refresh owns this requirement across both entry
		// points. Retire older subscribers before their replies can publish.
		for _, other := range s.requirementReaders {
			if other != reader && sameRequirementReaderLink(other.url, reader.url) {
				s.retireRequirementReaderRequest(other, kind)
			}
		}
	}
	reader.requestID++
	if reader.requestIDs == nil {
		reader.requestIDs = make(map[string]uint64)
	}
	reader.requestIDs[kind] = reader.requestID
	payload := s.bindRequirementAccount(map[string]any{"url": reader.url, "force": force,
		"readerKey": requirementReaderKey(reader.taskID, reader.url), "readerRequestID": fmt.Sprint(reader.requestID)})
	if item, ok := s.requirementForURL(reader.url); ok {
		payload["item"] = item.WorkItem
		payload["id"] = item.ID
	}
	return payload
}

func (s *app) bindRequirementAccount(payload map[string]any) map[string]any {
	// An absent token asks the backend to confirm the account on first use.
	// A present empty token is invalid and must never represent startup state.
	if state := s.requirementIdentityJobs; state != nil && state.confirmed && state.state == application.RequirementIdentityAuthenticated && state.identity != "" {
		payload["expectedIdentity"] = state.identity
	}
	return payload
}

func (s *app) requestRequirementContent(reader *requirementReaderState, comments, force bool) {
	if reader == nil {
		return
	}
	if !force && (comments && reader.comments != nil && !reader.commentsDirty || !comments && reader.body != nil && !reader.refreshAssetIndex) {
		return
	}
	if comments && reader.commentsDirty {
		force = true
	}
	s.startRequirementContentRead(reader, comments, force)
}

func (s *app) startRequirementContentRead(reader *requirementReaderState, comments, force bool) {
	if s.backend == nil || s.win == nil || s.win.IsDestroyed() || reader == nil || reader.url == "" {
		return
	}
	if comments {
		if reader.commentsLoading {
			return
		}
		reader.commentsLoading, reader.commentsError = true, ""
		if force {
			s.retireRequirementCommentImages(reader)
		}
	} else {
		if reader.bodyLoading {
			return
		}
		reader.bodyLoading, reader.bodyError = true, ""
		reader.refreshAssetIndex = false
		if force {
			reader.resourceErrors = make(map[string]string)
			if state := s.requirementContentJobs; state != nil {
				for _, request := range state.requests {
					if request.reader == reader && request.action.Kind == "requirements.attachment" {
						s.cancelRequirementContentJob(request)
					}
				}
			}
		}
	}
	kind := "requirements.read"
	if comments {
		kind = "requirements.comments"
	}
	s.act(kind, reader.taskID, "", "", reader.url, "", s.requirementReaderPayload(reader, kind, force))
}

func (s *app) requestRequirementResource(reader *requirementReaderState, resource application.RequirementResource, retry bool) {
	if s.backend == nil || s.win == nil || s.win.IsDestroyed() || reader.body == nil || reader.body.Generation == "" || reader.resourceLoading != "" {
		return
	}
	reader.resourceLoading = resource.URL
	delete(reader.resourceErrors, resource.URL)
	payload := s.requirementReaderPayload(reader, "requirements.attachment", false)
	payload["fileUrl"], payload["generation"], payload["retry"] = resource.URL, reader.body.Generation, retry
	s.act("requirements.attachment", reader.taskID, "", "", reader.url, "", payload)
}

func (s *app) applyRequirementContentResult(action application.Action, result application.ActionResult) {
	reader := s.requirementReaderForAction(action)
	if reader == nil {
		return
	}
	encoded, err := json.Marshal(result.Data)
	if err != nil {
		s.applyRequirementContentFailure(action, fmt.Errorf("需求结果格式无效：%w", err))
		return
	}
	switch action.Kind {
	case "requirements.read":
		var body application.RequirementContent
		if err = json.Unmarshal(encoded, &body); err != nil {
			s.applyRequirementContentFailure(action, err)
			return
		}
		s.updateRequirementReaderBody(reader, body)
		if action.Payload["force"] == true && !body.Stale && body.RefreshError == "" && body.CacheError == "" {
			for _, other := range s.requirementReaders {
				if other != reader && sameRequirementReaderLink(other.url, reader.url) {
					s.retireRequirementReaderRequest(other, action.Kind)
					s.updateRequirementReaderBody(other, body)
				}
			}
		}
	case "requirements.comments":
		var comments application.RequirementComments
		if err = json.Unmarshal(encoded, &comments); err != nil {
			s.applyRequirementContentFailure(action, err)
			return
		}
		s.updateRequirementReaderComments(reader, comments)
		if action.Payload["force"] == true && !comments.Stale && comments.RefreshError == "" && comments.CacheError == "" {
			for _, other := range s.requirementReaders {
				if other != reader && sameRequirementReaderLink(other.url, reader.url) {
					s.retireRequirementReaderRequest(other, action.Kind)
					s.updateRequirementReaderComments(other, comments)
				}
			}
		}
	case "requirements.attachment":
		if payloadString(action.Payload, "scope") == "comment" {
			s.applyRequirementCommentResource(reader, action, encoded)
			return
		}
		var resource application.RequirementResource
		if err = json.Unmarshal(encoded, &resource); err != nil {
			s.applyRequirementContentFailure(action, err)
			return
		}
		if resource.URL == "" {
			resource.URL = payloadString(action.Payload, "fileUrl")
		}
		reader.resourceLoading = ""
		if reader.body != nil && reader.body.Generation == payloadString(action.Payload, "generation") {
			for index := range reader.body.Resources {
				if reader.body.Resources[index].URL == resource.URL {
					reader.body.Resources[index] = resource
				}
			}
			images := make(map[string]string)
			for _, cached := range reader.body.Resources {
				if cached.Path != "" {
					images[cached.URL] = cached.Path
				}
			}
			reader.bodyMarkdown.path = reader.body.MarkdownPath
			updateReadonlyMarkdownState(reader.bodyMarkdown, reader.body.Body, images)
			reader.refreshAssetIndex = true
		}
	}
}

func (s *app) updateRequirementReaderBody(reader *requirementReaderState, body application.RequirementContent) {
	if reader.body != nil && reader.body.Generation != body.Generation {
		s.retireRequirementReaderRequest(reader, "requirements.attachment")
		reader.resourceErrors = make(map[string]string)
		reader.refreshAssetIndex = false
	}
	// Resource callbacks update this slice; each entry point owns its copy.
	body.Resources = append([]application.RequirementResource(nil), body.Resources...)
	reader.body, reader.bodyLoading, reader.bodyError = &body, false, body.RefreshError
	images := make(map[string]string)
	for _, resource := range body.Resources {
		if resource.Path != "" {
			images[resource.URL] = resource.Path
		}
	}
	if reader.bodyMarkdown == nil {
		reader.bodyMarkdown = newReadonlyMarkdownState("requirement:"+requirementReaderKey(reader.taskID, reader.url), body.Body, images)
		reader.bodyMarkdown.path = body.MarkdownPath
	} else {
		reader.bodyMarkdown.path = body.MarkdownPath
		updateReadonlyMarkdownState(reader.bodyMarkdown, body.Body, images)
	}
	reader.bodyMarkdown.richTextReading = true
}

func (s *app) updateRequirementReaderComments(reader *requirementReaderState, comments application.RequirementComments) {
	s.retireRequirementCommentImages(reader)
	comments.CommentResources = append([]application.RequirementCommentResource(nil), comments.CommentResources...)
	reader.comments, reader.commentsLoading, reader.commentsError = &comments, false, comments.RefreshError
	reader.commentsDirty = comments.RefreshError != ""
	content := requirementCommentsMarkdown(comments.Comments)
	if reader.commentsMarkdown == nil {
		reader.commentsMarkdown = newReadonlyMarkdownState("comments:"+requirementReaderKey(reader.taskID, reader.url), content, nil)
		reader.commentsMarkdown.path = comments.CachePath
	} else {
		reader.commentsMarkdown.path = comments.CachePath
		if reader.commentsMarkdown.doc == nil || reader.commentsMarkdown.doc.Source() != content || reader.commentsMarkdown.closed {
			updateReadonlyMarkdownState(reader.commentsMarkdown, content, nil)
		}
	}
	if reader.commentMarkdown == nil {
		reader.commentMarkdown = make(map[string]*markdownEditorState)
	}
	retained := make(map[string]bool, len(comments.Comments))
	for index, comment := range comments.Comments {
		key := requirementCommentStateKey(comment, index)
		retained[key] = true
		body := requirementCommentMarkdownBody(comment)
		images := requirementCommentTrustedImages(comments.CommentResources, comment.ID)
		state := reader.commentMarkdown[key]
		if state == nil {
			state = newReadonlyMarkdownState("requirement-comment:"+requirementReaderKey(reader.taskID, reader.url)+":"+key, body, images)
			reader.commentMarkdown[key] = state
		} else if state.doc == nil || state.doc.Source() != body || state.closed || !sameMarkdownTrustedImages(state.trustedImages, images) {
			updateReadonlyMarkdownState(state, body, images)
		}
		state.commentReading = true
		state.richTextReading = true
		state.path = comments.CachePath
	}
	for key, state := range reader.commentMarkdown {
		if !retained[key] {
			closeMarkdownPreviewState(state)
			delete(reader.commentMarkdown, key)
		}
	}
}

func closeRequirementCommentMarkdownStates(reader *requirementReaderState) {
	if reader == nil {
		return
	}
	for key, state := range reader.commentMarkdown {
		closeMarkdownPreviewState(state)
		delete(reader.commentMarkdown, key)
	}
	reader.commentMarkdown = nil
}

func requirementCommentStateKey(comment domain.RequirementComment, index int) string {
	if id := strings.TrimSpace(comment.ID); id != "" {
		return "id:" + id
	}
	return fmt.Sprintf("index:%d", index)
}

func requirementCommentFindSection(key string) string { return "comment:" + key }

func requirementCommentMarkdownBody(comment domain.RequirementComment) string {
	content := comment.Content
	if strings.TrimSpace(content) == "" {
		content = "（无文字内容）"
	}
	if comment.AttachmentURL == nil || strings.TrimSpace(*comment.AttachmentURL) == "" {
		return content
	}
	target, err := url.Parse(strings.TrimSpace(*comment.AttachmentURL))
	if err == nil && (target.Scheme == "http" || target.Scheme == "https") && target.Host != "" {
		return content + "\n\n[查看评论附件](<" + strings.NewReplacer("<", "%3C", ">", "%3E").Replace(target.String()) + ">)"
	}
	return content + "\n\n附件：" + escapeRequirementCommentLabel(*comment.AttachmentURL)
}

func (s *app) applyRequirementContentFailure(action application.Action, err error) bool {
	if action.Kind != "requirements.read" && action.Kind != "requirements.comments" && action.Kind != "requirements.attachment" {
		return false
	}
	if reader := s.requirementReaderForAction(action); reader != nil {
		var changed *application.RequirementIdentityChangedError
		if errors.As(err, &changed) {
			s.applyRequirementListIdentityFailure(changed.Snapshot, err)
			return true
		}
		switch action.Kind {
		case "requirements.read":
			reader.bodyLoading, reader.bodyError = false, err.Error()
		case "requirements.comments":
			reader.commentsLoading, reader.commentsError = false, err.Error()
		case "requirements.attachment":
			if payloadString(action.Payload, "scope") == "comment" {
				if reader.commentResourceErrors == nil {
					reader.commentResourceErrors = make(map[string]string)
				}
				reader.commentResourceErrors[commentActionResourceKey(action)] = err.Error()
				reader.commentResourceLoading = ""
			} else {
				reader.resourceErrors[payloadString(action.Payload, "fileUrl")] = err.Error()
				reader.resourceLoading = ""
			}
		}
	}
	return true
}

func requirementCommentsMarkdown(comments []domain.RequirementComment) string {
	byID := make(map[string]domain.RequirementComment, len(comments))
	for _, comment := range comments {
		byID[comment.ID] = comment
	}
	var out strings.Builder
	for index, comment := range comments {
		if index > 0 {
			out.WriteString("\n\n---\n\n")
		}
		fmt.Fprintf(&out, "**%s** · %s\n\n", escapeRequirementCommentLabel(firstNonEmpty(comment.AuthorName, comment.AuthorKey, "未知用户")), escapeRequirementCommentLabel(firstNonEmpty(comment.CreatedAt, "时间未知")))
		if comment.ParentID != nil {
			fmt.Fprintf(&out, "回复：%s\n\n", escapeRequirementCommentLabel(firstNonEmpty(byID[*comment.ParentID].AuthorName, *comment.ParentID)))
		}
		out.WriteString(requirementCommentMarkdownBody(comment))
	}
	return out.String()
}

func escapeRequirementCommentLabel(value string) string {
	var out strings.Builder
	for _, char := range value {
		if strings.ContainsRune("\\`*_{}[]<>#|!()", char) {
			out.WriteByte('\\')
		}
		if char == '\n' || char == '\r' {
			char = ' '
		}
		out.WriteRune(char)
	}
	return out.String()
}

func (s *app) renderRequirementContent(c *ui.Context, p palette, taskID, link string) {
	if strings.TrimSpace(link) == "" {
		label(c, "该任务尚未填写需求链接", 12, p.muted).Padding(16)
		return
	}
	reader := s.requirementReader(taskID, link)
	state := s.ensureRequirementContentJobs()
	state.visible = requirementReaderKey(taskID, reader.url)
	if state.entered != reader {
		state.entered = reader
	}
	if reader.body == nil && !reader.bodyLoading && reader.bodyError == "" && !s.requirementRefreshing(link) {
		s.requestRequirementContent(reader, false, false)
	}
	if (reader.comments == nil || reader.commentsDirty) && !reader.commentsLoading && reader.commentsError == "" && !s.requirementRefreshing(link) {
		s.requestRequirementContent(reader, true, false)
	}
	ui.Column(c.Key("requirement-content-" + requirementReaderKey(taskID, link))).FillWidth().Grow(1).MinHeight(0).Gap(8).Children(func() {
		s.renderRequirementReaderNotice(c, p, reader)
		s.renderRequirementReadonlyMarkdownReader(c, p, reader)
	})
}

func requirementReaderLoading(reader *requirementReaderState) bool {
	return reader == nil || reader.bodyLoading || reader.commentsLoading
}

func (s *app) renderRequirementReaderNotice(c *ui.Context, p palette, reader *requirementReaderState) {
	s.queueRequirementCommentImages(reader)
	body := reader.body
	if body != nil {
		for _, resource := range body.Resources {
			if resource.Path == "" {
				if reader.resourceErrors[resource.URL] == "" && resource.Error == "" && reader.resourceLoading == "" && !reader.bodyLoading {
					s.requestRequirementResource(reader, resource, false)
				}
			}
		}
		if reader.refreshAssetIndex && reader.resourceLoading == "" {
			s.requestRequirementContent(reader, false, false)
		}
	}
}

func (s *app) renderRequirementBody(c *ui.Context, p palette, reader *requirementReaderState) {
	s.renderRequirementReaderNotice(c, p, reader)
	if reader.body == nil {
		return
	}
	if strings.TrimSpace(reader.body.Body) == "" {
		label(c, "该工作项没有可读取的正文", 12, p.muted)
	}
}
