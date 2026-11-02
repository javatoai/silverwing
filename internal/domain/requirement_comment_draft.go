package domain

import (
	"strings"

	"silverwing-go/internal/markdowntext"
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
	// MarkdownRef is a registered, draft-local image destination. Its cache
	// remains available after deleting an occurrence so Undo can restore it.
	MarkdownRef string `json:"markdownRef,omitempty"`
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
	for _, n := range markdowntext.CommentImages(d.Content) {
		urls[n.Destination] = true
	}
	var images []CommentDraftAsset
	seen := map[string]bool{}
	for _, a := range d.Images {
		if seen[a.ID] {
			continue
		}
		if a.MarkdownRef != "" && (a.MarkdownRef != CommentImageMarkdownRef(a.ID) || !urls[a.MarkdownRef]) {
			continue
		}
		if a.MarkdownRef != "" || a.OriginalURL == "" || urls[a.OriginalURL] {
			images = append(images, a)
			seen[a.ID] = true
		}
	}
	return images
}

func CommentImageMarkdownRef(assetID string) string { return "#sw-comment-image/" + assetID }

// NormalizeImageReferences upgrades editable legacy append-only images in one
// draft transaction. Pending attempts retain their exact publication snapshot.
func (d *RequirementCommentDraft) NormalizeImageReferences() bool {
	if d == nil || d.Pending() {
		return false
	}
	changed := false
	for i := range d.Images {
		a := &d.Images[i]
		if a.MarkdownRef != "" || a.OriginalURL != "" {
			continue
		}
		a.MarkdownRef = CommentImageMarkdownRef(a.ID)
		markup := commentImageMarkup(a.Name, a.MarkdownRef, "")
		if d.Content != "" {
			d.Content += "\n\n"
		}
		d.Content += markup
		changed = true
	}
	return changed
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

// Only registered staged images authorize uploads. Rewriting targets real image
// nodes, preserving their position, alt text, title and surrounding Markdown.
func (d RequirementCommentDraft) Markdown(image func(CommentDraftAsset) (string, string)) string {
	parts := []string{}
	body := d.Content
	images := d.ActiveImages()
	replacements := map[string]*string{}
	annotations := map[string]string{}
	for _, a := range images {
		ref := a.MarkdownRef
		if ref == "" {
			ref = a.OriginalURL
		}
		if ref != "" {
			u, token := image(a)
			replacements[ref] = &u
			annotations[ref] = token
		}
	}
	body = markdowntext.RewriteCommentImages(body, replacements, annotations)
	if body := strings.TrimSpace(body); body != "" {
		parts = append(parts, body)
	}
	for _, asset := range images {
		if asset.OriginalURL != "" || asset.MarkdownRef != "" {
			continue
		}
		url, token := image(asset)
		parts = append(parts, commentImageMarkup(asset.Name, url, token))
	}
	if len(parts) == 0 && d.Attachment != nil {
		parts = append(parts, "附件："+d.Attachment.Name)
	}
	return strings.Join(parts, "\n\n")
}

func commentImageMarkup(name, destination, token string) string {
	name = strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]", "\r", "", "\n", " ").Replace(name)
	markup := "![" + name + "](" + destination + ")"
	if token != "" {
		markup += "<!--" + token + "-->"
	}
	return markup
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
