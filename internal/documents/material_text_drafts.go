package documents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// Text drafts share the journal format and bounded atomic cache writer while
// using their own file. Markdown records keep their existing schema and keys.
type MaterialTextDraft = MarkdownDraft
type MaterialTextDraftIdentity = MarkdownDraftIdentity

func (s *Service) ReadMaterialTextDraft(ctx context.Context, identity MaterialTextDraftIdentity) (*MaterialTextDraft, error) {
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	_, cache, err := s.readMaterialDraftCache(ctx, true)
	if err != nil {
		return nil, err
	}
	if draft, ok := cache.Records[identity.key()]; ok {
		return &draft, nil
	}
	return nil, nil
}

func (s *Service) WriteMaterialTextDraft(ctx context.Context, draft MaterialTextDraft) error {
	if !EditableMaterialText(draft.Identity.RelativePath) {
		return errors.New("该资料类型不能保存可编辑文本草稿")
	}
	draft, err := canonicalMaterialDraft(draft, true)
	if err != nil {
		return err
	}
	var sourceErr error
	err = s.updateMaterialDraftCache(ctx, true, func(cache *markdownDraftCache) bool {
		root, err := validatedMaterialsRoot(draft.Identity.MaterialsRoot)
		if err != nil {
			sourceErr = err
			return false
		}
		path, err := safePath(root, draft.Identity.RelativePath, false)
		if err != nil {
			sourceErr = err
			return false
		}
		info, err := os.Lstat(path)
		if err == nil {
			err = validateMaterialPath(root, path, info)
		}
		if err != nil {
			sourceErr = err
			return false
		}
		if !info.Mode().IsRegular() {
			sourceErr = errors.New("只能缓存普通资料文件的文本草稿")
			return false
		}
		key := draft.Identity.key()
		if old, exists := cache.Records[key]; exists && old == draft {
			return false
		}
		cache.Records[key] = draft
		return true
	})
	if err != nil {
		return err
	}
	return sourceErr
}

func (s *Service) DiscardMaterialTextDraft(ctx context.Context, identity MaterialTextDraftIdentity) error {
	return s.updateMaterialDraftCache(ctx, true, func(cache *markdownDraftCache) bool {
		key := identity.key()
		if _, exists := cache.Records[key]; !exists {
			return false
		}
		delete(cache.Records, key)
		return true
	})
}

func (s *Service) CompleteMaterialTextDraftSave(ctx context.Context, identity MaterialTextDraftIdentity, savedContent, oldHash, newHash string) error {
	return s.completeMaterialDraftSave(ctx, true, identity, savedContent, oldHash, newHash)
}

func (s *Service) MoveMaterialTextDrafts(ctx context.Context, identity MaterialTextDraftIdentity, previous, replacement string) error {
	return s.moveMaterialDrafts(ctx, true, identity, previous, replacement)
}

func (s *Service) RemoveMaterialTextDrafts(ctx context.Context, identity MaterialTextDraftIdentity, paths []string) error {
	return s.removeMaterialDrafts(ctx, true, identity, paths)
}

func (s *Service) RemoveMarkdownDrafts(ctx context.Context, identity MarkdownDraftIdentity, paths []string) error {
	return s.removeMaterialDrafts(ctx, false, identity, paths)
}

func (s *Service) removeMaterialDrafts(ctx context.Context, textDraft bool, identity MarkdownDraftIdentity, paths []string) error {
	return s.updateMaterialDraftCache(ctx, textDraft, func(cache *markdownDraftCache) bool {
		changed := false
		for key, draft := range cache.Records {
			if draft.Identity.TaskID != identity.TaskID || !samePathName(draft.Identity.TaskPath, identity.TaskPath) || !samePathName(draft.Identity.MaterialsRoot, identity.MaterialsRoot) {
				continue
			}
			path := filepath.Join(draft.Identity.MaterialsRoot, filepath.FromSlash(draft.Identity.RelativePath))
			for _, removed := range paths {
				if pathWithin(removed, path) {
					delete(cache.Records, key)
					changed = true
					break
				}
			}
		}
		return changed
	})
}
