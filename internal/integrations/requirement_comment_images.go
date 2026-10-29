package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/editor"
)

// Comment images are a runtime view backed by a sidecar; comments retain their
// original Markdown and the existing comment cache envelope remains unchanged.
type RequirementCommentResource struct {
	CommentID  string `json:"commentID"`
	URL        string `json:"url"`
	Path       string `json:"path,omitempty"`
	Error      string `json:"error,omitempty"`
	Generation string `json:"generation"`
}

type requirementCommentImageIndex struct {
	Version int                                         `json:"version"`
	Key     string                                      `json:"key"`
	Images  map[string]map[string]requirementImageEntry `json:"images"`
}

func (s *Service) requirementCommentImageDirectory(key string) string {
	return filepath.Join(s.paths.Cache, "requirement-comment-images", requirementCacheDigest(key))
}

func (s *Service) readRequirementCommentImageIndex(key string) (requirementCommentImageIndex, error) {
	index := requirementCommentImageIndex{Version: 1, Key: requirementCacheDigest(key), Images: make(map[string]map[string]requirementImageEntry)}
	path := filepath.Join(s.requirementCommentImageDirectory(key), "index.json")
	if err := requirementSafePath(s.paths.Cache, path); err != nil {
		return index, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return index, nil
	}
	if err != nil {
		return index, err
	}
	if !info.Mode().IsRegular() || requirementCacheReparsePoint(path, info) || info.Size() > maxRequirementCommentsCacheBytes {
		return index, errors.New("评论图片索引不是有效普通文件或超过大小限制")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return index, err
	}
	var saved requirementCommentImageIndex
	if err := strictRequirementJSON(data, &saved); err != nil {
		return index, errors.New("评论图片索引无法读取")
	}
	if saved.Version != 1 || saved.Key != index.Key || saved.Images == nil {
		return index, errors.New("评论图片索引身份不匹配")
	}
	return saved, nil
}

func commentImageResources(comments []domain.RequirementComment) []RequirementCommentResource {
	counts := make(map[string]int)
	for _, comment := range comments {
		counts[comment.ID]++
	}
	var resources []RequirementCommentResource
	for _, comment := range comments {
		if strings.TrimSpace(comment.ID) == "" || counts[comment.ID] != 1 {
			continue
		}
		generation := requirementCacheDigest(comment.Content)
		for _, imageURL := range editor.FeishuCommentImageURLs(comment.Content) {
			resources = append(resources, RequirementCommentResource{CommentID: comment.ID, URL: imageURL, Generation: generation})
		}
	}
	return resources
}

func (s *Service) resolveCommentImageResource(key string, resource RequirementCommentResource, index requirementCommentImageIndex) (RequirementCommentResource, error) {
	entry := index.Images[resource.CommentID][resource.URL]
	if entry.Generation != resource.Generation {
		return resource, nil
	}
	resource.Error = entry.Error
	if entry.RelativePath == "" {
		return resource, nil
	}
	directory := s.requirementCommentImageDirectory(key)
	assets := filepath.Join(directory, "assets")
	path := filepath.Clean(filepath.Join(directory, filepath.FromSlash(entry.RelativePath)))
	if err := requirementSafePath(assets, path); err != nil {
		return resource, err
	}
	if err := requirementSafePath(s.paths.Cache, path); err != nil {
		return resource, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return resource, nil
	}
	if err != nil {
		return resource, err
	}
	if !info.Mode().IsRegular() || requirementCacheReparsePoint(path, info) || info.Size() == 0 || info.Size() > 32<<20 {
		return resource, errors.New("评论图片不是有效普通文件或超过大小限制")
	}
	resource.Path = path
	return resource, nil
}

func (s *Service) requirementCommentsResult(key string, result RequirementCommentsReadResult) RequirementCommentsReadResult {
	if key == "" {
		return result
	}
	requirementCacheMu.Lock()
	defer requirementCacheMu.Unlock()
	index, err := s.readRequirementCommentImageIndex(key)
	for _, resource := range commentImageResources(result.Comments) {
		if err == nil {
			var resolveErr error
			resource, resolveErr = s.resolveCommentImageResource(key, resource, index)
			if resolveErr != nil {
				resource.Error = resolveErr.Error()
			}
		} else {
			resource.Error = err.Error()
		}
		result.CommentResources = append(result.CommentResources, resource)
	}
	return result
}

func (s *Service) validateCommentImageResource(key, commentID, generation, imageURL string) (RequirementCommentResource, error) {
	resource, _, err := s.validateCommentImageSnapshot(key, commentID, generation, imageURL)
	return resource, err
}

func (s *Service) validateCommentImageSnapshot(key, commentID, generation, imageURL string) (RequirementCommentResource, string, error) {
	saved, _, err := s.readRequirementCommentsCacheUnlocked(key)
	if err != nil {
		return RequirementCommentResource{}, "", err
	}
	if saved == nil {
		return RequirementCommentResource{}, "", errors.New("当前需求评论尚未持久缓存")
	}
	resources := commentImageResources(saved.Data.Comments)
	for _, resource := range resources {
		if resource.CommentID == commentID && resource.Generation == generation && resource.URL == imageURL {
			return resource, commentImageCatalogVersion(saved.FetchedAt, resources), nil
		}
	}
	return RequirementCommentResource{}, "", errors.New("评论已刷新或图片不属于当前评论")
}

func (s *Service) cachedCommentImageResource(key, commentID, generation, imageURL string) (RequirementCommentResource, error) {
	resource, _, err := s.cachedCommentImageSnapshot(key, commentID, generation, imageURL)
	return resource, err
}

func (s *Service) cachedCommentImageSnapshot(key, commentID, generation, imageURL string) (RequirementCommentResource, string, error) {
	requirementCacheMu.Lock()
	defer requirementCacheMu.Unlock()
	resource, version, err := s.validateCommentImageSnapshot(key, commentID, generation, imageURL)
	if err != nil {
		return resource, "", err
	}
	index, err := s.readRequirementCommentImageIndex(key)
	if err != nil {
		return resource, "", err
	}
	resource, err = s.resolveCommentImageResource(key, resource, index)
	return resource, version, err
}

func (s *Service) saveCommentImageResource(ctx context.Context, key, commentID, generation, imageURL string, image []byte, message string) (RequirementCommentResource, error) {
	requirementCacheMu.Lock()
	defer requirementCacheMu.Unlock()
	resource, err := s.validateCommentImageResource(key, commentID, generation, imageURL)
	if err != nil {
		return resource, err
	}
	if err := ctx.Err(); err != nil {
		return resource, err
	}
	index, err := s.readRequirementCommentImageIndex(key)
	if err != nil {
		return resource, err
	}
	entry := requirementImageEntry{Generation: generation, Error: message}
	if message == "" {
		if err := requirementCacheImageBytes(image); err != nil {
			return resource, err
		}
		extension, _ := requirementImageExtension(image)
		// The sidecar retains independent comment ownership while identical
		// immutable bytes occupy a single asset in this account/item directory.
		// Older sidecar paths remain readable without migration.
		relative := "assets/" + requirementCacheDigest(string(image)) + "." + extension
		path := filepath.Join(s.requirementCommentImageDirectory(key), filepath.FromSlash(relative))
		if err := atomicRequirementWrite(s.paths.Cache, path, image); err != nil {
			return resource, err
		}
		entry.RelativePath = relative
	}
	if index.Images[commentID] == nil {
		index.Images[commentID] = make(map[string]requirementImageEntry)
	}
	index.Images[commentID][imageURL] = entry
	data, err := json.Marshal(index)
	if err != nil {
		return resource, err
	}
	if len(data) > maxRequirementCommentsCacheBytes {
		return resource, errors.New("评论图片索引超过大小限制")
	}
	if err := ctx.Err(); err != nil {
		return resource, err
	}
	if err := atomicRequirementWrite(s.paths.Cache, filepath.Join(s.requirementCommentImageDirectory(key), "index.json"), data); err != nil {
		return resource, err
	}
	return s.resolveCommentImageResource(key, resource, index)
}
