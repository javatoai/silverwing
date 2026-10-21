package documents

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

// A task root move changes draft ownership even when its materials live in an
// external directory. Relative document paths and original source stay intact.
type MarkdownDraftTaskRelocation struct {
	TaskID                string
	PreviousTaskPath      string
	NextTaskPath          string
	PreviousMaterialsRoot string
	NextMaterialsRoot     string
}

type markdownDraftRelocation struct {
	previous MarkdownDraftIdentity
	next     MarkdownDraftIdentity
}

// RelocateMarkdownDrafts commits the entire batch in one cache replacement.
// Replaying the same batch is safe: only records with the previous identity
// match. Invalid mappings and collisions leave every original record intact.
func (s *Service) RelocateMarkdownDrafts(ctx context.Context, changes []MarkdownDraftTaskRelocation) error {
	if err := s.relocateMaterialDrafts(ctx, false, changes); err != nil {
		return err
	}
	return s.relocateMaterialDrafts(ctx, true, changes)
}

func (s *Service) relocateMaterialDrafts(ctx context.Context, textDraft bool, changes []MarkdownDraftTaskRelocation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	mappings := make([]markdownDraftRelocation, 0, len(changes))
	seen := make(map[string]MarkdownDraftIdentity, len(changes))
	for _, change := range changes {
		if !filepath.IsAbs(change.PreviousTaskPath) || !filepath.IsAbs(change.NextTaskPath) ||
			!filepath.IsAbs(change.PreviousMaterialsRoot) || !filepath.IsAbs(change.NextMaterialsRoot) {
			return errors.New("Markdown 草稿目录迁移必须使用绝对身份路径")
		}
		previous, err := NewMarkdownDraftIdentity(change.TaskID, change.PreviousTaskPath, change.PreviousMaterialsRoot, ".silverwing-draft-identity.md")
		if err != nil {
			return fmt.Errorf("原 Markdown 草稿身份无效：%w", err)
		}
		next, err := NewMarkdownDraftIdentity(change.TaskID, change.NextTaskPath, change.NextMaterialsRoot, ".silverwing-draft-identity.md")
		if err != nil {
			return fmt.Errorf("新 Markdown 草稿身份无效：%w", err)
		}
		if existing, ok := seen[previous.key()]; ok {
			if existing.key() != next.key() {
				return errors.New("同一 Markdown 草稿目录有多个迁移目标")
			}
			continue
		}
		seen[previous.key()] = next
		mappings = append(mappings, markdownDraftRelocation{previous, next})
	}
	if len(mappings) == 0 {
		return nil
	}
	var validationErr error
	err := s.updateMaterialDraftCache(ctx, textDraft, func(cache *markdownDraftCache) bool {
		moves := make(map[string]MarkdownDraft)
		targets := make(map[string]MarkdownDraft)
		for key, draft := range cache.Records {
			for _, mapping := range mappings {
				if draft.Identity.TaskID != mapping.previous.TaskID ||
					!samePathName(draft.Identity.TaskPath, mapping.previous.TaskPath) ||
					!samePathName(draft.Identity.MaterialsRoot, mapping.previous.MaterialsRoot) {
					continue
				}
				next, identityErr := NewMarkdownDraftIdentity(draft.Identity.TaskID, mapping.next.TaskPath, mapping.next.MaterialsRoot, draft.Identity.RelativePath)
				if identityErr != nil {
					validationErr = identityErr
					return false
				}
				if next.key() == key {
					break
				}
				draft.Identity = next
				if existing, exists := targets[next.key()]; exists && existing != draft {
					validationErr = errors.New("多个 Markdown 草稿迁移到同一身份，原缓存已保留")
					return false
				}
				moves[key], targets[next.key()] = draft, draft
				break
			}
		}
		for key, draft := range targets {
			if existing, exists := cache.Records[key]; exists {
				if _, moving := moves[key]; !moving && existing != draft {
					validationErr = errors.New("Markdown 草稿迁移目标已有不同原文，原缓存已保留")
					return false
				}
			}
		}
		for key := range moves {
			delete(cache.Records, key)
		}
		for key, draft := range targets {
			cache.Records[key] = draft
		}
		return len(moves) != 0
	})
	if err != nil {
		return err
	}
	return validationErr
}
