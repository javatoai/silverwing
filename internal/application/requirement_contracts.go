package application

import (
	"silverwing-go/internal/domain"
	"silverwing-go/internal/integrations"
)

// RequirementContent and its resources are application views. The desktop
// consumes these values without depending on the integration/cache service.
type RequirementResource struct {
	URL   string `json:"url"`
	Path  string `json:"path,omitempty"`
	Error string `json:"error,omitempty"`
}

type RequirementContent struct {
	ID              string                `json:"id"`
	ProjectKey      string                `json:"projectKey"`
	Type            string                `json:"type"`
	Title           string                `json:"title"`
	Body            string                `json:"body"`
	URL             string                `json:"url"`
	FetchedAt       int64                 `json:"fetchedAt"`
	Source          string                `json:"source"`
	Stale           bool                  `json:"stale"`
	RefreshError    string                `json:"refreshError,omitempty"`
	CacheError      string                `json:"cacheError,omitempty"`
	MarkdownPath    string                `json:"markdownPath,omitempty"`
	Generation      string                `json:"generation,omitempty"`
	AssetsDirectory string                `json:"assetsDirectory,omitempty"`
	Complete        bool                  `json:"complete"`
	Resources       []RequirementResource `json:"resources"`
}

type RequirementComments struct {
	CommentResources []RequirementCommentResource `json:"commentResources,omitempty"`
	Comments         []domain.RequirementComment  `json:"comments"`
	Warnings         []string                     `json:"warnings"`
	FetchedAt        int64                        `json:"fetchedAt"`
	Source           string                       `json:"source"`
	Stale            bool                         `json:"stale"`
	RefreshError     string                       `json:"refreshError,omitempty"`
	CacheError       string                       `json:"cacheError,omitempty"`
	CachePath        string                       `json:"cachePath,omitempty"`
}

type RequirementCommentResource struct {
	CommentID  string `json:"commentID"`
	URL        string `json:"url"`
	Path       string `json:"path,omitempty"`
	Error      string `json:"error,omitempty"`
	Generation string `json:"generation"`
}

type RequirementMetadata struct {
	ID           string                         `json:"id"`
	ProjectKey   string                         `json:"projectKey"`
	Type         string                         `json:"type"`
	Title        *string                        `json:"title"`
	Status       *string                        `json:"status"`
	Participants domain.RequirementParticipants `json:"participants"`
	Stale        bool                           `json:"stale"`
	RefreshError string                         `json:"refreshError,omitempty"`
}

func requirementViewData(data any) any {
	switch source := data.(type) {
	case integrations.RequirementReadResult:
		var resources []RequirementResource
		if source.Resources != nil {
			resources = make([]RequirementResource, len(source.Resources))
			for index, resource := range source.Resources {
				resources[index] = RequirementResource{URL: resource.URL, Path: resource.Path, Error: resource.Error}
			}
		}
		return RequirementContent{ID: source.ID, ProjectKey: source.ProjectKey, Type: source.Type, Title: source.Title,
			Body: source.Body, URL: source.URL, FetchedAt: source.FetchedAt, Source: source.Source, Stale: source.Stale,
			RefreshError: source.RefreshError, CacheError: source.CacheError, MarkdownPath: source.MarkdownPath,
			Generation: source.Generation, AssetsDirectory: source.AssetsDirectory, Complete: source.Complete, Resources: resources}
	case integrations.RequirementCommentsReadResult:
		resources := make([]RequirementCommentResource, len(source.CommentResources))
		for index, resource := range source.CommentResources {
			resources[index] = RequirementCommentResource{CommentID: resource.CommentID, URL: resource.URL, Path: resource.Path, Error: resource.Error, Generation: resource.Generation}
		}
		return RequirementComments{Comments: source.Comments, Warnings: source.Warnings, FetchedAt: source.FetchedAt,
			Source: source.Source, Stale: source.Stale, RefreshError: source.RefreshError, CacheError: source.CacheError, CachePath: source.CachePath, CommentResources: resources}
	case integrations.RequirementMetadataReadResult:
		return RequirementMetadata{ID: source.ID, ProjectKey: source.ProjectKey, Type: source.Type, Title: source.Title,
			Status: source.Status, Participants: source.Participants, Stale: source.Stale, RefreshError: source.RefreshError}
	case integrations.RequirementResource:
		return RequirementResource{URL: source.URL, Path: source.Path, Error: source.Error}
	case integrations.RequirementCommentResource:
		return RequirementCommentResource{CommentID: source.CommentID, URL: source.URL, Path: source.Path, Error: source.Error, Generation: source.Generation}
	default:
		return data
	}
}
