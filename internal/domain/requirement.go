package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

type RequirementPerson struct {
	Name  string  `json:"name"`
	Email *string `json:"email"`
}

type RequirementParticipants struct {
	QCOwners        []RequirementPerson `json:"qcOwners"`
	ProductManagers []RequirementPerson `json:"productManagers"`
}

func (p RequirementParticipants) IsEmpty() bool {
	return len(p.QCOwners) == 0 && len(p.ProductManagers) == 0
}

type RequirementMetadata struct {
	Title        *string                 `json:"title"`
	Status       *string                 `json:"status"`
	Participants RequirementParticipants `json:"participants"`
}

type RequirementComment struct {
	ID            string  `json:"id"`
	Content       string  `json:"content"`
	AuthorKey     string  `json:"authorKey"`
	AuthorName    string  `json:"authorName"`
	CreatedAt     string  `json:"createdAt"`
	UpdatedAt     *string `json:"updatedAt"`
	ParentID      *string `json:"parentId"`
	AttachmentURL *string `json:"attachmentUrl"`
}

type RequirementComments struct {
	Comments []RequirementComment `json:"comments"`
	Warnings []string             `json:"warnings"`
}

type ParticipatedWorkItem struct {
	ProjectKey           string              `json:"projectKey"`
	ProjectName          string              `json:"projectName"`
	Type                 string              `json:"type"`
	TypeLabel            string              `json:"typeLabel"`
	ID                   string              `json:"id"`
	Title                string              `json:"title"`
	URL                  string              `json:"url"`
	Developers           []RequirementPerson `json:"developers"`
	QCOwners             []RequirementPerson `json:"qcOwners"`
	ProductManagers      []RequirementPerson `json:"productManagers"`
	MySprintEstimateDays *string             `json:"mySprintEstimateDays"`
	MetadataWarnings     []string            `json:"metadataWarnings"`
	Status               *string             `json:"status"`
}

func (w ParticipatedWorkItem) Key() string { return w.ProjectKey + ":" + w.Type + ":" + w.ID }

type RequirementCommentWriteStatus string

const (
	RequirementCommentCreated   RequirementCommentWriteStatus = "CREATED"
	RequirementCommentUpdated   RequirementCommentWriteStatus = "UPDATED"
	RequirementCommentUnchanged RequirementCommentWriteStatus = "UNCHANGED"
	RequirementCommentSkipped   RequirementCommentWriteStatus = "SKIPPED"
	RequirementCommentFailed    RequirementCommentWriteStatus = "FAILED"
	RequirementCommentCancelled RequirementCommentWriteStatus = "CANCELLED"
)

type RequirementCommentWriteResult struct {
	Status                  RequirementCommentWriteStatus
	Message                 string
	CommentID               *string
	RequirementDirectoryURL *string
}

func RequirementCommentMarker(item ParticipatedWorkItem) string {
	digest := sha256.Sum256([]byte(item.ProjectKey + "\n" + item.ID))
	return "[silverwing-materials:" + hex.EncodeToString(digest[:10]) + "]"
}

type RequirementDraftState struct {
	RequirementLink  string  `json:"requirementLink"`
	TaskName         string  `json:"taskName"`
	RequirementTitle *string `json:"requirementTitle"`
	NameEdited       bool    `json:"nameEdited"`

	MetadataLoading bool    `json:"metadataLoading"`
	MetadataHint    *string `json:"metadataHint"`
}

func (s RequirementDraftState) ChangeRequirement(value, _ string, title *string) RequirementDraftState {
	var clean *string
	if title != nil && strings.TrimSpace(*title) != "" {
		clean = title
	}
	return RequirementDraftState{RequirementLink: value, TaskName: s.TaskName, RequirementTitle: clean, NameEdited: s.NameEdited, MetadataLoading: ParseFeishuWorkItemLink(value) != nil}
}

func (s RequirementDraftState) EditName(value string) RequirementDraftState {
	s.TaskName, s.NameEdited = value, true
	return s
}

func (s RequirementDraftState) ApplyAINaming(requestedLink string, suggestion RequirementAiNamingSuggestion, _ string) (RequirementDraftState, error) {
	if requestedLink != s.RequirementLink {
		return s, nil
	}
	validated, err := ValidateRequirementAiNamingSuggestion(suggestion)
	if err != nil {
		return s, err
	}
	if !s.NameEdited {
		s.TaskName = validated.FolderName
	}
	return s, nil
}

func (s RequirementDraftState) ApplyMetadata(requestedLink string, metadata *RequirementMetadata) RequirementDraftState {
	if requestedLink != s.RequirementLink {
		return s
	}
	var title *string
	if metadata != nil && metadata.Title != nil && strings.TrimSpace(*metadata.Title) != "" {
		title = metadata.Title
	}
	s.RequirementTitle, s.MetadataLoading = title, false
	if metadata == nil {
		message := "未获取到需求标题，可手工填写"
		s.MetadataHint = &message
	}
	return s
}

func RequireTaskName(value string) (string, error) {
	name, err := RequireValidDirectoryName(value)
	if err != nil {
		return "", fmt.Errorf("%w", err)
	}
	return name, nil
}
