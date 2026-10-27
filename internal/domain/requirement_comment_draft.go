package domain

import (
	"strings"

	"silverwing-go/internal/editor"
)

type CommentUpload struct {
	Token string `json:"token"`
	URL   string `json:"url"`
	Scope string `json:"scope"`
}

type CommentDraftAsset struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	MIME        string         `json:"mime"`
	Size        int64          `json:"size"`
	Hash        string         `json:"hash"`
	LocalPath   string         `json:"localPath,omitempty"`
	Error       string         `json:"error,omitempty"`
	Upload      *CommentUpload `json:"upload,omitempty"`
	OriginalURL string         `json:"originalUrl,omitempty"`
}

type CommentEditTarget struct {
	CommentID string              `json:"commentId"`
	Original  *RequirementComment `json:"original,omitempty"`
	Conflict  bool                `json:"conflict,omitempty"`
}

type CommentPublishAttempt struct {
	ID        string   `json:"id"`
	State     string   `json:"state"`
	Body      string   `json:"body,omitempty"`
	BeforeIDs []string `json:"beforeIds,omitempty"`
	StartedAt string   `json:"startedAt"`
	AuthorKey string   `json:"authorKey,omitempty"`
	CommentID string   `json:"commentId,omitempty"`
	Error     string   `json:"error,omitempty"`
}

type RequirementCommentDraft struct {
	ID         string                 `json:"id"`
	Identity   string                 `json:"identity"`
	Account    string                 `json:"account"`
	Scope      string                 `json:"scope"`
	Item       ParticipatedWorkItem   `json:"item"`
	Revision   uint64                 `json:"revision"`
	Content    string                 `json:"content"`
	Images     []CommentDraftAsset    `json:"images"`
	Attachment *CommentDraftAsset     `json:"attachment,omitempty"`
	Attempt    *CommentPublishAttempt `json:"attempt,omitempty"`
	Edit       *CommentEditTarget     `json:"edit,omitempty"`
}

func (d RequirementCommentDraft) Editing() bool { return d.Edit != nil && d.Edit.CommentID != "" }

func (d RequirementCommentDraft) ActiveImages() []CommentDraftAsset {
	urls := map[string]bool{}
	if d.Editing() {
		for _, n := range editor.CommentImages(d.Content) {
			urls[n.Destination] = true
		}
	}
	var images []CommentDraftAsset
	for _, a := range d.Images {
		if a.OriginalURL == "" || urls[a.OriginalURL] {
			images = append(images, a)
		}
	}
	return images
}

func (d RequirementCommentDraft) HasChanges() bool {
	if !d.Editing() {
		return d.HasContent()
	}
	if d.Edit.Original == nil {
		return false
	}
	if strings.TrimSpace(d.Content) != strings.TrimSpace(d.Edit.Original.Content) {
		return true
	}
	for _, a := range d.ActiveImages() {
		if a.OriginalURL == "" {
			return true
		}
	}
	original := d.Edit.Original.AttachmentURL
	if d.Attachment == nil {
		return original != nil && *original != ""
	}
	return original == nil || d.Attachment.OriginalURL != *original
}

func (d RequirementCommentDraft) Pending() bool {
	return d.Attempt != nil && (d.Attempt.State == "publishing" || d.Attempt.State == "unknown")
}

func (d RequirementCommentDraft) HasContent() bool {
	return strings.TrimSpace(d.Content) != "" || len(d.ActiveImages()) > 0 || d.Attachment != nil
}

// Only explicitly staged images are appended. Arbitrary local Markdown paths
// are never interpreted as upload requests.
func (d RequirementCommentDraft) Markdown(image func(CommentDraftAsset) (string, string)) string {
	parts := []string{}
	body := d.Content
	images := d.ActiveImages()
	if d.Editing() {
		replacements := map[string]*string{}
		annotations := map[string]string{}
		for _, a := range images {
			if a.OriginalURL != "" {
				u, token := image(a)
				replacements[a.OriginalURL] = &u
				annotations[a.OriginalURL] = token
			}
		}
		body = editor.RewriteCommentImages(body, replacements, annotations)
	}
	if body := strings.TrimSpace(body); body != "" {
		parts = append(parts, body)
	}
	for _, asset := range images {
		if asset.OriginalURL != "" {
			continue
		}
		name := strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]", "\r", "", "\n", " ").Replace(asset.Name)
		url, token := image(asset)
		markup := "![" + name + "](" + url + ")"
		if token != "" {
			markup += "<!--" + token + "-->"
		}
		parts = append(parts, markup)
	}
	if len(parts) == 0 && d.Attachment != nil {
		parts = append(parts, "附件："+d.Attachment.Name)
	}
	return strings.Join(parts, "\n\n")
}

type CommentPublishProgress struct {
	Stage     string `json:"stage"`
	Completed int    `json:"completed"`
	Total     int    `json:"total"`
}

type CommentPublishResult struct {
	Status    string                  `json:"status"`
	Message   string                  `json:"message"`
	Draft     RequirementCommentDraft `json:"draft"`
	CommentID string                  `json:"commentId,omitempty"`
}
