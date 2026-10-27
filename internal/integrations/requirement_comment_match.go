package integrations

import (
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/editor"
)

const commentPublishTimeout = 45 * time.Second

// Matching never rewrites a draft or a remote comment. Only the exact uploaded
// image nodes may differ; everything around them remains source-sensitive.
func matchPublishedComment(d *domain.RequirementCommentDraft, comments []domain.RequirementComment) (domain.RequirementComment, string) {
	if d == nil || d.Attempt == nil {
		return domain.RequirementComment{}, "缺少本次发布记录，请保留草稿并核对原需求。"
	}
	expected, sequence, err := comparableCommentBody(d.Attempt.Body, d.Images)
	if err != nil {
		return domain.RequirementComment{}, "本次图片上传凭证不完整，暂不能确认发布结果。"
	}
	wanted := make([]string, 0, len(d.Images))
	for _, image := range d.Images {
		if image.Upload == nil || image.Upload.Token == "" {
			return domain.RequirementComment{}, "本次图片上传凭证不完整，暂不能确认发布结果。"
		}
		wanted = append(wanted, image.Upload.Token)
	}
	if !slices.Equal(sequence, wanted) {
		return domain.RequirementComment{}, "发布快照中的图片与上传凭证不一致，请核对原需求。"
	}
	previous := make(map[string]bool, len(d.Attempt.BeforeIDs))
	for _, id := range d.Attempt.BeforeIDs {
		previous[id] = true
	}
	var matches []domain.RequirementComment
	sawNewCandidate := false
	reason := "尚未找到本次发布的新增评论，请稍后重新核对。"
	if d.Attempt.CommentID != "" {
		reason = "尚未读到本次返回编号对应的评论，请稍后重新核对。"
	}
	for _, c := range comments {
		if c.ID == "" || previous[c.ID] || c.ParentID != nil || d.Attempt.AuthorKey == "" || c.AuthorKey != d.Attempt.AuthorKey {
			continue
		}
		sawNewCandidate = true
		if d.Attempt.CommentID != "" {
			if c.ID != d.Attempt.CommentID {
				continue
			}
		} else if !commentCreatedInPublishWindow(d.Attempt.StartedAt, c.CreatedAt) {
			reason = "新增评论的创建时间无法确认属于本次发布，请核对原需求。"
			continue
		}
		body, actualSequence, bodyErr := comparableCommentBody(c.Content, d.Images)
		if bodyErr != nil || !slices.Equal(sequence, actualSequence) || body != expected {
			reason = "新增评论的正文或图片凭证与本次发布不一致，请核对原需求。"
			continue
		}
		if d.Attachment != nil {
			if d.Attachment.Upload == nil || c.AttachmentURL == nil || !commentUploadURLMatches(*c.AttachmentURL, *d.Attachment.Upload) {
				reason = "新增评论的附件凭证与本次上传不一致，请核对原需求。"
				continue
			}
		} else if c.AttachmentURL != nil {
			reason = "新增评论包含本次未上传的附件，请核对原需求。"
			continue
		}
		matches = append(matches, c)
	}
	if len(matches) == 1 {
		return matches[0], ""
	}
	if len(matches) > 1 {
		reason = "找到多条符合本次内容的新增评论，无法唯一确认，请核对原需求。"
	}
	if len(matches) == 0 && !sawNewCandidate && d.Attempt.CommentID == "" {
		for _, c := range comments {
			if !previous[c.ID] || c.ParentID != nil || d.Attempt.AuthorKey == "" || c.AuthorKey != d.Attempt.AuthorKey {
				continue
			}
			body, oldSequence, bodyErr := comparableCommentBody(c.Content, d.Images)
			if bodyErr != nil || body != expected || !slices.Equal(sequence, oldSequence) {
				continue
			}
			if d.Attachment != nil {
				if d.Attachment.Upload == nil || c.AttachmentURL == nil || !commentUploadURLMatches(*c.AttachmentURL, *d.Attachment.Upload) {
					continue
				}
			} else if c.AttachmentURL != nil {
				continue
			}
			reason = "相同内容的评论在本次发布前已存在；尚未找到本次新增评论，请先核对原需求。"
			break
		}
	}
	return domain.RequirementComment{}, reason
}

func commentCreatedInPublishWindow(started, created string) bool {
	start, err := time.Parse(time.RFC3339Nano, started)
	if err != nil {
		return false
	}
	stamp, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		// Feishu's unzoned comment timestamps are China Standard Time, not
		// the machine's local timezone. FixedZone also works on Windows
		// installations without an IANA timezone database.
		stamp, err = time.ParseInLocation("2006-01-02 15:04:05", created, time.FixedZone("Asia/Shanghai", 8*60*60))
	}
	if err != nil {
		return false
	}
	stamp = stamp.Truncate(time.Second)
	return !stamp.Before(start.Truncate(time.Second)) && !stamp.After(start.Add(commentPublishTimeout).Truncate(time.Second))
}

func commentUploadURLMatches(raw string, receipt domain.CommentUpload) bool {
	if receipt.Token == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	if receipt.URL != "" && raw == receipt.URL {
		return true
	}
	const prefix = "/goapi/v5/platform/file/stream/download/"
	if u.Host != "project.feishu.cn" || !strings.HasPrefix(u.EscapedPath(), prefix) {
		return false
	}
	tail := strings.TrimPrefix(u.EscapedPath(), prefix)
	if tail == "" || strings.Contains(tail, "/") {
		return false
	}
	token, err := url.PathUnescape(tail)
	return err == nil && token == receipt.Token
}

type commentImageReplacement struct {
	start, end int
	token      string
	title      string
}

func comparableCommentBody(source string, images []domain.CommentDraftAsset) (string, []string, error) {
	var replacements []commentImageReplacement
	var visit func(editor.Node) error
	visit = func(node editor.Node) error {
		if node.Kind == "image" {
			for _, asset := range images {
				if asset.Upload == nil || !commentUploadURLMatches(node.Destination, *asset.Upload) {
					continue
				}
				if node.Range.Start < 0 || node.Range.End <= node.Range.Start || node.Range.End > len(source) {
					return fmt.Errorf("invalid uploaded image range or title")
				}
				end := adjacentCommentUploadAnnotationEnd(source, node.Range.End, asset.Upload.Token)
				replacements = append(replacements, commentImageReplacement{node.Range.Start, end, asset.Upload.Token, node.Title})
				return nil
			}
		}
		for _, child := range node.Children {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	for _, part := range editor.Parse(source).Parts {
		for _, node := range part.Nodes {
			if err := visit(node); err != nil {
				return "", nil, err
			}
		}
	}
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start < replacements[j].start })
	var body strings.Builder
	var sequence []string
	position := 0
	for _, image := range replacements {
		if image.start < position {
			return "", nil, fmt.Errorf("overlapping image ranges")
		}
		body.WriteString(source[position:image.start])
		body.WriteString("\x00sw-comment-image:" + image.token + "\x00")
		if image.title != "" {
			body.WriteString("title:" + image.title + "\x00")
		}
		sequence = append(sequence, image.token)
		position = image.end
	}
	body.WriteString(source[position:])
	return strings.TrimSpace(strings.ReplaceAll(body.String(), "\r\n", "\n")), sequence, nil
}

func adjacentCommentUploadAnnotationEnd(source string, end int, token string) int {
	start, newlines := end, 0
	for start < len(source) && strings.ContainsRune(" \t\r\n", rune(source[start])) {
		if source[start] == '\n' {
			newlines++
		}
		if newlines > 1 {
			return end
		}
		start++
	}
	if !strings.HasPrefix(source[start:], "<!--") {
		return end
	}
	stop := strings.Index(source[start+4:], "-->")
	if stop < 0 || strings.TrimSpace(source[start+4:start+4+stop]) != token {
		return end
	}
	return start + 4 + stop + 3
}
