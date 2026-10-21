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

type MaterialsServiceBranch struct {
	ServiceName string
	Branch      string
}

type MaterialsCommentContext struct {
	Item     *ParticipatedWorkItem
	Services []MaterialsServiceBranch
}

type MaterialsCommentStatus string

const (
	MaterialsCommentCreated   MaterialsCommentStatus = "CREATED"
	MaterialsCommentUpdated   MaterialsCommentStatus = "UPDATED"
	MaterialsCommentUnchanged MaterialsCommentStatus = "UNCHANGED"
	MaterialsCommentSkipped   MaterialsCommentStatus = "SKIPPED"
	MaterialsCommentFailed    MaterialsCommentStatus = "FAILED"
	MaterialsCommentCancelled MaterialsCommentStatus = "CANCELLED"
)

type MaterialsCommentResult struct {
	Status                  MaterialsCommentStatus
	Message                 string
	CommentID               *string
	RequirementDirectoryURL *string
}

type RequirementMaterialsCommentGateway interface {
	Upsert(item ParticipatedWorkItem, content, marker string) (MaterialsCommentResult, error)
}

func MaterialsCommentMarker(item ParticipatedWorkItem) string {
	digest := sha256.Sum256([]byte(item.ProjectKey + "\n" + item.ID))
	return "[silverwing-materials:" + hex.EncodeToString(digest[:10]) + "]"
}

type RequirementDraftState struct {
	RequirementLink  string  `json:"requirementLink"`
	TaskName         string  `json:"taskName"`
	RequirementTitle *string `json:"requirementTitle"`
	Branch           string  `json:"branch"`
	NameEdited       bool    `json:"nameEdited"`
	BranchEdited     bool    `json:"branchEdited"`
	AIBranchSuffix   *string `json:"aiBranchSuffix"`
	MetadataLoading  bool    `json:"metadataLoading"`
	MetadataHint     *string `json:"metadataHint"`
}

func (s RequirementDraftState) ChangeRequirement(value, branchPrefix string, title *string) RequirementDraftState {
	resolved := ResolveBranchPrefix(branchPrefix, value)
	branch := s.Branch
	if !s.BranchEdited {
		if resolved != nil {
			branch = *resolved
		} else {
			branch = branchPrefix
		}
	}
	var cleanTitle *string
	if title != nil && strings.TrimSpace(*title) != "" {
		cleanTitle = title
	}
	var hint *string
	if ContainsUnresolvedBranchPlaceholder(branchPrefix) && resolved == nil {
		message := "未从需求编号或链接中解析到编号"
		hint = &message
	}
	return RequirementDraftState{RequirementLink: value, TaskName: s.TaskName, RequirementTitle: cleanTitle, Branch: branch,
		NameEdited: s.NameEdited, BranchEdited: s.BranchEdited, MetadataLoading: ParseFeishuWorkItemLink(value) != nil, MetadataHint: hint}
}

func (s RequirementDraftState) ChangeGroup(branchPrefix string) RequirementDraftState {
	branch, hint := s.Branch, s.MetadataHint
	if !s.BranchEdited {
		resolved := ResolveBranchPrefix(branchPrefix, s.RequirementLink)
		if resolved != nil {
			branch = *resolved
		} else {
			branch = branchPrefix
		}
		if s.AIBranchSuffix != nil {
			base := branchPrefix
			if resolved != nil {
				base = *resolved
			}
			branch = ComposeAIRequirementBranch(base, *s.AIBranchSuffix)
		}
		if ContainsUnresolvedBranchPlaceholder(branchPrefix) && resolved == nil {
			message := "未从需求编号或链接中解析到编号"
			hint = &message
		} else {
			hint = nil
		}
	}
	return RequirementDraftState{RequirementLink: s.RequirementLink, TaskName: s.TaskName, RequirementTitle: s.RequirementTitle,
		Branch: branch, NameEdited: s.NameEdited, BranchEdited: s.BranchEdited, AIBranchSuffix: s.AIBranchSuffix,
		MetadataLoading: s.MetadataLoading, MetadataHint: hint}
}

func (s RequirementDraftState) EditName(value string) RequirementDraftState {
	s.TaskName, s.NameEdited = value, true
	return s
}

func (s RequirementDraftState) EditBranch(value string) RequirementDraftState {
	s.Branch, s.BranchEdited, s.AIBranchSuffix = value, true, nil
	return s
}

func (s RequirementDraftState) ApplyAINaming(requestedLink string, suggestion RequirementAiNamingSuggestion, branchPrefix string) (RequirementDraftState, error) {
	if requestedLink != s.RequirementLink {
		return s, nil
	}
	validated, err := ValidateRequirementAiNamingSuggestion(suggestion)
	if err != nil {
		return s, err
	}
	resolved := ResolveBranchPrefix(branchPrefix, s.RequirementLink)
	base := branchPrefix
	if resolved != nil {
		base = *resolved
	}
	if !s.NameEdited {
		s.TaskName = validated.FolderName
	}
	if !s.BranchEdited {
		s.Branch = ComposeAIRequirementBranch(base, validated.BranchSuffix)
		s.AIBranchSuffix = &validated.BranchSuffix
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
