package desktop

import (
	"encoding/json"
	"fmt"

	"silverwing-go/internal/application"
)

func commentResourceKey(resource application.RequirementCommentResource) string {
	return resource.CommentID + "\x00" + resource.Generation + "\x00" + resource.URL
}

func commentActionResourceKey(action application.Action) string {
	return commentResourceKey(application.RequirementCommentResource{CommentID: payloadString(action.Payload, "commentID"),
		Generation: payloadString(action.Payload, "commentGeneration"), URL: payloadString(action.Payload, "fileUrl")})
}

func requirementCommentResourceCurrent(reader *requirementReaderState, action application.Action) bool {
	if reader == nil || reader.comments == nil || reader.commentsLoading {
		return false
	}
	key := commentActionResourceKey(action)
	for _, resource := range reader.comments.CommentResources {
		if commentResourceKey(resource) == key {
			return true
		}
	}
	return false
}

func requirementCommentTrustedImages(resources []application.RequirementCommentResource, commentID string) map[string]string {
	images := make(map[string]string)
	for _, resource := range resources {
		if resource.CommentID == commentID && resource.Path != "" {
			images[resource.URL] = resource.Path
		}
	}
	return images
}

func (s *app) requestRequirementCommentResource(reader *requirementReaderState, resource application.RequirementCommentResource, retry bool) {
	if s.backend == nil || s.win == nil || s.win.IsDestroyed() || reader == nil || reader.comments == nil ||
		reader.commentsLoading || resource.CommentID == "" || resource.Generation == "" || reader.commentResourceLoading != "" {
		return
	}
	if reader.commentResourceErrors == nil {
		reader.commentResourceErrors = make(map[string]string)
	}
	key := commentResourceKey(resource)
	reader.commentResourceLoading = key
	delete(reader.commentResourceErrors, key)
	delete(reader.commentResourceRetries, key)
	payload := s.requirementReaderPayload(reader, "requirements.attachment:comment", false)
	payload["scope"], payload["commentID"], payload["commentGeneration"] = "comment", resource.CommentID, resource.Generation
	payload["fileUrl"], payload["retry"] = resource.URL, retry
	s.act("requirements.attachment", reader.taskID, "", "", reader.url, "", payload)
}

func (s *app) queueRequirementCommentImages(reader *requirementReaderState) {
	if reader == nil || reader.comments == nil || reader.commentsLoading || reader.commentResourceLoading != "" {
		return
	}
	for _, resource := range reader.comments.CommentResources {
		if resource.Path == "" && resource.Error == "" && reader.commentResourceErrors[commentResourceKey(resource)] == "" {
			s.requestRequirementCommentResource(reader, resource, reader.commentResourceRetries[commentResourceKey(resource)])
			return
		}
	}
}

func (s *app) retryRequirementCommentImages(reader *requirementReaderState) {
	if reader == nil || reader.comments == nil || reader.commentResourceLoading != "" || reader.commentsLoading {
		return
	}
	// Clear the current errors once; the ordinary queue then retries each missing
	// image exactly once and stops again for failures returned by that attempt.
	reader.commentResourceErrors = make(map[string]string)
	reader.commentResourceRetries = make(map[string]bool)
	for index := range reader.comments.CommentResources {
		reader.comments.CommentResources[index].Error = ""
		if reader.comments.CommentResources[index].Path == "" {
			reader.commentResourceRetries[commentResourceKey(reader.comments.CommentResources[index])] = true
		}
	}
	for _, resource := range reader.comments.CommentResources {
		if resource.Path == "" {
			s.requestRequirementCommentResource(reader, resource, true)
			return
		}
	}
}

func (s *app) retireRequirementCommentImages(reader *requirementReaderState) {
	if reader == nil {
		return
	}
	if state := s.requirementUnit.state.requirementContentJobs; state != nil {
		for _, request := range state.requests {
			if request.reader == reader && payloadString(request.action.Payload, "scope") == "comment" {
				s.requirementUnit.cancelRequirementContentJob(request)
			}
		}
	}
	delete(reader.requestIDs, "requirements.attachment:comment")
	reader.commentResourceLoading = ""
	reader.commentResourceErrors = make(map[string]string)
	reader.commentResourceRetries = nil
}

func (s *app) applyRequirementCommentResource(reader *requirementReaderState, action application.Action, encoded []byte) {
	var resource application.RequirementCommentResource
	if err := json.Unmarshal(encoded, &resource); err != nil {
		s.applyRequirementContentFailure(action, fmt.Errorf("评论图片结果格式无效：%w", err))
		return
	}
	if !requirementCommentResourceCurrent(reader, action) || commentResourceKey(resource) != commentActionResourceKey(action) {
		s.applyRequirementContentFailure(action, fmt.Errorf("评论图片结果身份不匹配"))
		return
	}
	reader.commentResourceLoading = ""
	for index := range reader.comments.CommentResources {
		if commentResourceKey(reader.comments.CommentResources[index]) == commentResourceKey(resource) {
			reader.comments.CommentResources[index] = resource
		}
	}
	for index, comment := range reader.comments.Comments {
		if comment.ID != resource.CommentID {
			continue
		}
		state := reader.commentMarkdown[requirementCommentStateKey(comment, index)]
		if state != nil {
			updateReadonlyMarkdownState(state, requirementCommentMarkdownBody(comment), requirementCommentTrustedImages(reader.comments.CommentResources, comment.ID))
		}
	}
}
