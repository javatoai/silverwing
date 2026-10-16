package documents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

const MaxMarkdownDraftRecords = 100
const MaxMarkdownDraftCacheBytes = 8 * 1024 * 1024

type MarkdownDraftIdentity struct {
	TaskID        string `json:"taskId"`
	TaskPath      string `json:"taskPath"`
	MaterialsRoot string `json:"materialsRoot"`
	RelativePath  string `json:"relativePath"`
}

type MarkdownDraft struct {
	Identity     MarkdownDraftIdentity `json:"identity"`
	Content      string                `json:"content"`
	BaselineHash string                `json:"baselineHash"`
	HasUTF8BOM   bool                  `json:"hasUtf8Bom,omitempty"`
	Newline      string                `json:"newline,omitempty"`
}

type markdownDraftCache struct {
	Schema  int                      `json:"schema"`
	Records map[string]MarkdownDraft `json:"records"`
}

func (cache *markdownDraftCache) UnmarshalJSON(data []byte) error {
	wire := struct {
		Schema  int             `json:"schema"`
		Records json.RawMessage `json:"records"`
	}{Schema: cache.Schema}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	records := make(map[string]MarkdownDraft)
	if len(wire.Records) != 0 {
		if err := json.Unmarshal(wire.Records, &records); err != nil {
			// Earlier empty journals used []. Only an empty array is equivalent
			// to the map; never discard unrecognized, potentially unsaved records.
			var empty []json.RawMessage
			if arrayErr := json.Unmarshal(wire.Records, &empty); arrayErr != nil || len(empty) != 0 {
				return err
			}
		}
	}
	cache.Schema, cache.Records = wire.Schema, records
	return nil
}

func NewMarkdownDraftIdentity(taskID, taskPath, root, path string) (MarkdownDraftIdentity, error) {
	if taskID == "" || taskPath == "" || root == "" || path == "" {
		return MarkdownDraftIdentity{}, errors.New("Markdown 草稿缺少文档身份")
	}
	taskPath, err := filepath.Abs(taskPath)
	if err != nil {
		return MarkdownDraftIdentity{}, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return MarkdownDraftIdentity{}, err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return MarkdownDraftIdentity{}, err
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || hasGitSegment(path) {
		return MarkdownDraftIdentity{}, errors.New("Markdown 草稿路径超出资料目录")
	}
	return MarkdownDraftIdentity{TaskID: taskID, TaskPath: filepath.Clean(taskPath), MaterialsRoot: filepath.Clean(root), RelativePath: filepath.ToSlash(relative)}, nil
}

func (identity MarkdownDraftIdentity) key() string {
	path := strings.Join([]string{identity.TaskPath, identity.MaterialsRoot, identity.RelativePath}, "\x00")
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return Hash([]byte(identity.TaskID + "\x00" + path))
}

func canonicalMarkdownDraft(draft MarkdownDraft) (MarkdownDraft, error) {
	return canonicalMaterialDraft(draft, false)
}

func canonicalMaterialDraft(draft MarkdownDraft, textDraft bool) (MarkdownDraft, error) {
	identity := draft.Identity
	if !filepath.IsAbs(identity.TaskPath) || !filepath.IsAbs(identity.MaterialsRoot) || filepath.IsAbs(identity.RelativePath) {
		return draft, errors.New("Markdown 草稿身份无效")
	}
	canonical, err := NewMarkdownDraftIdentity(identity.TaskID, identity.TaskPath, identity.MaterialsRoot, identity.RelativePath)
	if err != nil {
		return draft, err
	}
	if len(materialTextBytes(draft.Content, draft.HasUTF8BOM)) > MaxMaterialsText || !utf8.ValidString(draft.Content) {
		return draft, errors.New("资料草稿必须为不超过 3 MiB（含 BOM）的 UTF-8 原文")
	}
	if textDraft && strings.IndexByte(draft.Content, 0) >= 0 {
		return draft, errors.New("该资料类型不能保存可编辑文本草稿")
	}
	if len(draft.BaselineHash) != 64 || strings.Trim(draft.BaselineHash, "0123456789abcdefABCDEF") != "" {
		return draft, errors.New("Markdown 草稿没有可验证的原始版本")
	}
	draft.Identity, draft.BaselineHash = canonical, strings.ToLower(draft.BaselineHash)
	return draft, nil
}

// Drafts contain the untouched editing source and its original disk hash. Their
// cache is independent of reading position and belongs only to the Go home.
func (s *Service) ReadMarkdownDraft(ctx context.Context, identity MarkdownDraftIdentity) (*MarkdownDraft, error) {
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	_, cache, err := s.readMarkdownDraftCache(ctx)
	if err != nil {
		return nil, err
	}
	draft, ok := cache.Records[identity.key()]
	if !ok {
		return nil, nil
	}
	return &draft, nil
}

func (s *Service) WriteMarkdownDraft(ctx context.Context, draft MarkdownDraft) error {
	draft, err := canonicalMarkdownDraft(draft)
	if err != nil {
		return err
	}
	return s.updateMarkdownDraftCache(ctx, func(cache *markdownDraftCache) bool {
		key := draft.Identity.key()
		if old, exists := cache.Records[key]; exists && old == draft {
			return false
		}
		cache.Records[key] = draft
		return true
	})
}

func (s *Service) DiscardMarkdownDraft(ctx context.Context, identity MarkdownDraftIdentity) error {
	return s.updateMarkdownDraftCache(ctx, func(cache *markdownDraftCache) bool {
		key := identity.key()
		if _, exists := cache.Records[key]; !exists {
			return false
		}
		delete(cache.Records, key)
		return true
	})
}

// A save may finish after newer text was typed. Keep that newer draft and move
// its baseline to the successful save instead of deleting the newer source.
func (s *Service) CompleteMarkdownDraftSave(ctx context.Context, identity MarkdownDraftIdentity, savedContent, oldHash, newHash string) error {
	return s.completeMaterialDraftSave(ctx, false, identity, savedContent, oldHash, newHash)
}

func (s *Service) completeMaterialDraftSave(ctx context.Context, textDraft bool, identity MarkdownDraftIdentity, savedContent, oldHash, newHash string) error {
	return s.updateMaterialDraftCache(ctx, textDraft, func(cache *markdownDraftCache) bool {
		key := identity.key()
		draft, exists := cache.Records[key]
		if !exists || draft.BaselineHash != strings.ToLower(oldHash) {
			return false
		}
		if draft.Content == savedContent {
			delete(cache.Records, key)
		} else {
			draft.BaselineHash = newHash
			cache.Records[key] = draft
		}
		return true
	})
}

// Rename moves every cached descendant, including drafts not open this run.
// One cache replacement commits the complete mapping; failed writes retain the
// previous cache and can never partially remove its original records.
func (s *Service) MoveMarkdownDrafts(ctx context.Context, identity MarkdownDraftIdentity, previous, replacement string) error {
	return s.moveMaterialDrafts(ctx, false, identity, previous, replacement)
}

func (s *Service) moveMaterialDrafts(ctx context.Context, textDraft bool, identity MarkdownDraftIdentity, previous, replacement string) error {
	oldPath := filepath.Join(identity.MaterialsRoot, filepath.FromSlash(previous))
	newPath := filepath.Join(identity.MaterialsRoot, filepath.FromSlash(replacement))
	if !pathWithin(identity.MaterialsRoot, oldPath) || !pathWithin(identity.MaterialsRoot, newPath) {
		return errors.New("Markdown 草稿迁移超出资料目录")
	}
	return s.updateMaterialDraftCache(ctx, textDraft, func(cache *markdownDraftCache) bool {
		moves := make(map[string]MarkdownDraft)
		for key, draft := range cache.Records {
			if draft.Identity.TaskID != identity.TaskID || !samePathName(draft.Identity.TaskPath, identity.TaskPath) || !samePathName(draft.Identity.MaterialsRoot, identity.MaterialsRoot) {
				continue
			}
			path := filepath.Join(draft.Identity.MaterialsRoot, filepath.FromSlash(draft.Identity.RelativePath))
			if !pathWithin(oldPath, path) {
				continue
			}
			relative, err := filepath.Rel(oldPath, path)
			if err != nil {
				continue
			}
			target := newPath
			if relative != "." {
				target = filepath.Join(newPath, relative)
			}
			next, err := NewMarkdownDraftIdentity(identity.TaskID, identity.TaskPath, identity.MaterialsRoot, target)
			if err != nil {
				continue
			}
			draft.Identity = next
			moves[key] = draft
		}
		for key := range moves {
			delete(cache.Records, key)
		}
		for _, draft := range moves {
			cache.Records[draft.Identity.key()] = draft
		}
		return len(moves) != 0
	})
}

func (s *Service) updateMarkdownDraftCache(ctx context.Context, update func(*markdownDraftCache) bool) error {
	return s.updateMaterialDraftCache(ctx, false, update)
}

func (s *Service) updateMaterialDraftCache(ctx context.Context, textDraft bool, update func(*markdownDraftCache) bool) error {
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	path, cache, err := s.readMaterialDraftCache(ctx, textDraft)
	if err != nil {
		return err
	}
	if !update(&cache) {
		return nil
	}
	if len(cache.Records) > MaxMarkdownDraftRecords {
		return errors.New("Markdown 草稿缓存已达到 100 条上限")
	}
	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	if len(data) > MaxMarkdownDraftCacheBytes {
		return errors.New("Markdown 草稿缓存已达到 8 MB 上限")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRealAncestors(filepath.Dir(path), false); err != nil {
		return err
	}
	return atomicWrite(path, data, 0600)
}

func (s *Service) readMarkdownDraftCache(ctx context.Context) (string, markdownDraftCache, error) {
	return s.readMaterialDraftCache(ctx, false)
}

func (s *Service) readMaterialDraftCache(ctx context.Context, textDraft bool) (string, markdownDraftCache, error) {
	cache := markdownDraftCache{Schema: 1, Records: make(map[string]MarkdownDraft)}
	if err := ctx.Err(); err != nil {
		return "", cache, err
	}
	if strings.TrimSpace(s.CacheRoot) == "" {
		return "", cache, errors.New("Markdown 草稿缓存目录未配置")
	}
	root, err := filepath.Abs(s.CacheRoot)
	if err != nil {
		return "", cache, err
	}
	if err := validateRealAncestors(root, true); err != nil {
		return "", cache, err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", cache, err
	}
	if err := validateRealAncestors(root, false); err != nil {
		return "", cache, err
	}
	path := filepath.Join(root, "materials-markdown-drafts.json")
	if textDraft {
		path = filepath.Join(root, "materials-text-drafts.json")
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return path, cache, nil
	}
	if err != nil {
		return path, cache, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxMarkdownDraftCacheBytes {
		return path, cache, errors.New("Markdown 草稿缓存不是普通文件或已超过 8 MB")
	}
	file, err := openMaterialFileNoFollow(path, info)
	if err != nil {
		return path, cache, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxMarkdownDraftCacheBytes+1))
	if err != nil {
		return path, cache, err
	}
	if len(data) > MaxMarkdownDraftCacheBytes || !utf8.Valid(data) {
		return path, cache, errors.New("Markdown 草稿缓存无效或已超过 8 MB")
	}
	if err := json.Unmarshal(data, &cache); err != nil {
		return path, cache, fmt.Errorf("读取 Markdown 草稿缓存失败：%w", err)
	}
	if cache.Schema != 1 || len(cache.Records) > MaxMarkdownDraftRecords {
		return path, cache, errors.New("Markdown 草稿缓存格式或条数无效")
	}
	if cache.Records == nil {
		cache.Records = make(map[string]MarkdownDraft)
	}
	for key, record := range cache.Records {
		draft, err := canonicalMaterialDraft(record, textDraft)
		if err != nil || key != draft.Identity.key() {
			return path, cache, errors.New("Markdown 草稿缓存的内容或身份无效")
		}
		cache.Records[key] = draft
	}
	return path, cache, nil
}
