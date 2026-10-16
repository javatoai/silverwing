package integrations

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	goldtext "github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

const (
	maxRequirementCommentsCacheBytes = 16 << 20
	maxRequirementMetadataCacheBytes = 2 << 20
	requirementMetadataCacheVersion  = 1
)

var requirementCacheMu sync.Mutex

type RequirementResource struct {
	URL   string `json:"url"`
	Path  string `json:"path,omitempty"`
	Error string `json:"error,omitempty"`
}

type RequirementReadResult struct {
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

type RequirementCommentsReadResult struct {
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

type requirementDocumentIndex struct {
	Version      int                              `json:"version"`
	Key          string                           `json:"key"`
	Project      string                           `json:"project"`
	Type         string                           `json:"type"`
	ID           string                           `json:"id"`
	Title        string                           `json:"title"`
	URL          string                           `json:"url"`
	FetchedAt    int64                            `json:"fetchedAt"`
	Generation   string                           `json:"generation"`
	BodyOffset   int                              `json:"bodyOffset"`
	MarkdownHash string                           `json:"markdownHash"`
	Images       map[string]requirementImageEntry `json:"images"`
}

type requirementImageEntry struct {
	RelativePath string `json:"relativePath,omitempty"`
	Error        string `json:"error,omitempty"`
	Generation   string `json:"generation,omitempty"`
}

type requirementDocument struct {
	Index        requirementDocumentIndex
	Directory    string
	MarkdownPath string
	AssetsPath   string
	Body         string
	Resources    []RequirementResource
	Complete     bool
}

type requirementCommentsEnvelope struct {
	Version   int                        `json:"version"`
	Key       string                     `json:"key"`
	FetchedAt int64                      `json:"fetchedAt"`
	Data      domain.RequirementComments `json:"data"`
}

type requirementMetadataEnvelope struct {
	Version   int                           `json:"version"`
	Key       string                        `json:"key"`
	FetchedAt int64                         `json:"fetchedAt"`
	Data      RequirementMetadataReadResult `json:"data"`
}

func requirementCacheKey(kind, identity string, item domain.ParticipatedWorkItem) string {
	canonical := strings.TrimSpace(item.URL)
	if link := domain.ParseFeishuWorkItemLink(canonical); link != nil {
		canonical = "project.feishu.cn/" + link.Space + "/" + link.Kind + "/" + link.WorkItemID
	}
	context, _ := marshalRequirementJSON([]any{"item", item.ProjectKey, item.Type, item.ID, canonical})
	key, _ := marshalRequirementJSON([]any{kind, identity, string(context)})
	return string(key)
}

func marshalRequirementJSON(value any) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(output.Bytes(), []byte{'\n'}), nil
}

func requirementCacheDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func newRequirementGeneration() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

func requirementImageExtension(data []byte) (string, error) {
	starts := func(signature ...byte) bool {
		if len(data) < len(signature) {
			return false
		}
		for i, value := range signature {
			if data[i] != value {
				return false
			}
		}
		return true
	}
	prefix := strings.TrimSpace(string(data[:minInt(len(data), 1024)]))
	switch {
	case starts(137, 80, 78, 71, 13, 10, 26, 10):
		return "png", nil
	case starts(255, 216, 255):
		return "jpg", nil
	case strings.HasPrefix(prefix, "GIF87a") || strings.HasPrefix(prefix, "GIF89a"):
		return "gif", nil
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "webp", nil
	case starts('B', 'M'):
		return "bmp", nil
	case strings.HasPrefix(prefix, "<svg") || strings.HasPrefix(prefix, "<?xml") && strings.Contains(prefix, "<svg"):
		return "svg", nil
	case len(data) >= 12 && (string(data[4:12]) == "ftypavif" || string(data[4:12]) == "ftypavis"):
		return "avif", nil
	default:
		return "", errors.New("下载结果不是受支持的图片")
	}
}

func requirementCacheImageBytes(data []byte) error {
	if len(data) == 0 {
		return errors.New("图片内容为空")
	}
	if len(data) > 32<<20 {
		return errors.New("图片超过 32 MiB，无法缓存")
	}
	_, err := requirementImageExtension(data)
	return err
}

func requirementUnixMillis() int64 { return time.Now().UnixMilli() }

func strictRequirementJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("缓存包含多个 JSON 值")
		}
		return err
	}
	return nil
}

func requirementSafePath(root, target string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("需求缓存路径越界")
	}
	for current := target; ; current = filepath.Dir(current) {
		info, statErr := os.Lstat(current)
		if statErr == nil {
			if requirementCacheReparsePoint(current, info) {
				return errors.New("需求缓存路径不能包含符号链接或重解析点")
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		if filepath.Clean(current) == filepath.Clean(root) || filepath.Dir(current) == current {
			break
		}
	}
	return nil
}

func ensureRequirementDirectory(root, directory string) error {
	if err := requirementSafePath(root, directory); err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	return requirementSafePath(root, directory)
}

func atomicRequirementWrite(root, path string, data []byte) error {
	if err := requirementSafePath(root, path); err != nil {
		return err
	}
	if err := ensureRequirementDirectory(root, filepath.Dir(path)); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".requirement-*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	return nil
}

func (s *Service) requirementDocumentDirectory(key string) string {
	return filepath.Join(s.paths.Cache, "requirement-documents", requirementCacheDigest(key))
}

func (s *Service) readRequirementDocument(key string) (*requirementDocument, error) {
	requirementCacheMu.Lock()
	defer requirementCacheMu.Unlock()
	return s.readRequirementDocumentUnlocked(key)
}

func (s *Service) readRequirementDocumentUnlocked(key string) (*requirementDocument, error) {
	directory := s.requirementDocumentDirectory(key)
	if err := requirementSafePath(s.paths.Cache, directory); err != nil {
		return nil, err
	}
	indexPath := filepath.Join(directory, "index.json")
	info, err := os.Lstat(indexPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || requirementCacheReparsePoint(indexPath, info) {
		return nil, errors.New("需求文档索引不是普通文件")
	}
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, err
	}
	var index requirementDocumentIndex
	if err := strictRequirementJSON(data, &index); err != nil {
		return nil, fmt.Errorf("需求文档索引无法读取：%w", err)
	}
	if index.Version != 1 || index.Key != requirementCacheDigest(key) || !numericIDPattern.MatchString(index.ID) || index.Generation == "" || index.FetchedAt < 0 || index.Images == nil {
		return nil, errors.New("需求文档索引不匹配")
	}
	markdownPath := filepath.Join(directory, "需求-"+index.ID+".md")
	assetsPath := filepath.Join(directory, "需求-"+index.ID+".assets")
	if err := requirementSafePath(s.paths.Cache, markdownPath); err != nil {
		return nil, err
	}
	markdownInfo, err := os.Lstat(markdownPath)
	if err != nil || !markdownInfo.Mode().IsRegular() || requirementCacheReparsePoint(markdownPath, markdownInfo) {
		return nil, errors.New("本地需求 Markdown 不存在或不是普通文件")
	}
	markdown, err := os.ReadFile(markdownPath)
	if err != nil {
		return nil, err
	}
	if requirementCacheDigest(string(markdown)) != index.MarkdownHash {
		return nil, errors.New("本地需求 Markdown 哈希不匹配")
	}
	bodyOffset, ok := byteOffsetFromUTF16(string(markdown), index.BodyOffset)
	if !ok {
		return nil, errors.New("需求 Markdown 正文偏移无效")
	}
	reverse := make(map[string]string, len(index.Images))
	resources := make([]RequirementResource, 0, len(index.Images))
	for imageURL, entry := range index.Images {
		resource := RequirementResource{URL: imageURL, Error: entry.Error}
		if entry.RelativePath != "" {
			candidate := filepath.Clean(filepath.Join(directory, filepath.FromSlash(entry.RelativePath)))
			expectedAssetRoot := filepath.Clean(assetsPath) + string(filepath.Separator)
			if !strings.HasPrefix(candidate+string(filepath.Separator), expectedAssetRoot) {
				return nil, errors.New("需求图片路径越界")
			}
			if err := requirementSafePath(s.paths.Cache, candidate); err != nil {
				return nil, err
			}
			assetInfo, statErr := os.Lstat(candidate)
			if statErr == nil && assetInfo.Mode().IsRegular() && !requirementCacheReparsePoint(candidate, assetInfo) {
				resource.Path = candidate
				reverse[entry.RelativePath] = imageURL
			} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
				return nil, statErr
			}
		}
		resources = append(resources, resource)
	}
	sortRequirementResources(resources)
	body := replaceRequirementImageDestinations(string(markdown[bodyOffset:]), reverse)
	complete := true
	for _, resource := range resources {
		if resource.Path == "" {
			complete = false
		}
	}
	return &requirementDocument{Index: index, Directory: directory, MarkdownPath: markdownPath, AssetsPath: assetsPath, Body: body, Resources: resources, Complete: complete}, nil
}

func (s *Service) writeRequirementDocument(key string, item domain.ParticipatedWorkItem, body string, fetchedAt int64) (*requirementDocument, error) {
	requirementCacheMu.Lock()
	defer requirementCacheMu.Unlock()
	if !numericIDPattern.MatchString(item.ID) {
		return nil, errors.New("需求编号不能用于缓存文件路径")
	}
	directory := s.requirementDocumentDirectory(key)
	if err := ensureRequirementDirectory(s.paths.Cache, directory); err != nil {
		return nil, err
	}
	var previous *requirementDocument
	if loaded, err := s.readRequirementDocumentUnlocked(key); err == nil {
		previous = loaded
	}
	images := map[string]requirementImageEntry{}
	for _, imageURL := range requirementDocumentImageURLs(body) {
		entry := requirementImageEntry{}
		if previous != nil {
			for _, resource := range previous.Resources {
				if resource.URL == imageURL {
					if resource.Path != "" {
						if relative, err := filepath.Rel(directory, resource.Path); err == nil && !strings.HasPrefix(relative, "..") {
							entry.RelativePath = filepath.ToSlash(relative)
						}
					}
				}
			}
		}
		images[imageURL] = entry
	}
	header := "# " + requirementHeaderTitle(item.Title) + "\n\n来源：" + item.URL + "\n\n---\n\n"
	replacements := make(map[string]string)
	for imageURL, entry := range images {
		if entry.RelativePath != "" {
			replacements[imageURL] = entry.RelativePath
		}
	}
	markdown := header + replaceRequirementImageDestinations(body, replacements)
	generation, err := newRequirementGeneration()
	if err != nil {
		return nil, err
	}
	index := requirementDocumentIndex{Version: 1, Key: requirementCacheDigest(key), Project: item.ProjectKey, Type: item.Type, ID: item.ID,
		Title: item.Title, URL: item.URL, FetchedAt: fetchedAt, Generation: generation, BodyOffset: utf16Length(header),
		MarkdownHash: requirementCacheDigest(markdown), Images: images}
	markdownPath := filepath.Join(directory, "需求-"+item.ID+".md")
	if err := requirementSafePath(s.paths.Cache, markdownPath); err != nil {
		return nil, err
	}
	if info, statErr := os.Lstat(markdownPath); statErr == nil && (!info.Mode().IsRegular() || requirementCacheReparsePoint(markdownPath, info)) {
		return nil, errors.New("需求 Markdown 不是普通文件")
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, statErr
	}
	old, oldErr := os.ReadFile(markdownPath)
	oldExists := oldErr == nil
	if oldErr != nil && !errors.Is(oldErr, os.ErrNotExist) {
		return nil, oldErr
	}
	if err := atomicRequirementWrite(s.paths.Cache, markdownPath, []byte(markdown)); err != nil {
		return nil, err
	}
	indexData, err := json.Marshal(index)
	if err == nil {
		err = atomicRequirementWrite(s.paths.Cache, filepath.Join(directory, "index.json"), indexData)
	}
	if err != nil {
		if oldExists {
			_ = atomicRequirementWrite(s.paths.Cache, markdownPath, old)
		} else {
			_ = os.Remove(markdownPath)
		}
		return nil, err
	}
	return s.readRequirementDocumentUnlocked(key)
}

func (s *Service) saveRequirementImage(key, generation, imageURL string, image []byte) (*requirementDocument, error) {
	requirementCacheMu.Lock()
	defer requirementCacheMu.Unlock()
	return s.saveRequirementImageUnlocked(key, generation, imageURL, image)
}

func (s *Service) saveRequirementImageUnlocked(key, generation, imageURL string, image []byte) (*requirementDocument, error) {
	document, err := s.readRequirementDocumentUnlocked(key)
	if err != nil || document == nil {
		if err == nil {
			err = errors.New("需求缓存不存在")
		}
		return nil, err
	}
	if document.Index.Generation != generation {
		return nil, errors.New("需求正文已刷新，请重新读取图片")
	}
	entry, ok := document.Index.Images[imageURL]
	if !ok {
		return nil, errors.New("图片不属于当前需求正文")
	}
	extension, err := requirementImageExtension(image)
	if err != nil {
		return nil, err
	}
	if err := ensureRequirementDirectory(s.paths.Cache, document.AssetsPath); err != nil {
		return nil, err
	}
	filename := requirementCacheDigest(string(image)) + "-" + requirementCacheDigest(imageURL)[:12] + "." + extension
	destination := filepath.Join(document.AssetsPath, filename)
	if err := atomicRequirementWrite(s.paths.Cache, destination, image); err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(document.Directory, destination)
	if err != nil {
		return nil, err
	}
	entry = requirementImageEntry{RelativePath: filepath.ToSlash(relative), Generation: generation}
	document.Index.Images[imageURL] = entry
	markdown, err := os.ReadFile(document.MarkdownPath)
	if err != nil {
		return nil, err
	}
	bodyOffset, ok := byteOffsetFromUTF16(string(markdown), document.Index.BodyOffset)
	if !ok {
		return nil, errors.New("需求 Markdown 正文偏移无效")
	}
	mapPaths := make(map[string]string)
	for imageKey, imageEntry := range document.Index.Images {
		if imageEntry.RelativePath != "" {
			mapPaths[imageKey] = imageEntry.RelativePath
		}
	}
	newMarkdown := string(markdown[:bodyOffset]) + replaceRequirementImageDestinations(document.Body, mapPaths)
	document.Index.MarkdownHash = requirementCacheDigest(newMarkdown)
	indexData, err := json.Marshal(document.Index)
	if err != nil {
		return nil, err
	}
	if err := atomicRequirementWrite(s.paths.Cache, document.MarkdownPath, []byte(newMarkdown)); err != nil {
		return nil, err
	}
	if err := atomicRequirementWrite(s.paths.Cache, filepath.Join(document.Directory, "index.json"), indexData); err != nil {
		_ = atomicRequirementWrite(s.paths.Cache, document.MarkdownPath, markdown)
		return nil, err
	}
	return s.readRequirementDocumentUnlocked(key)
}

func (s *Service) failRequirementImage(ctx context.Context, key, generation, imageURL, message string) {
	requirementCacheMu.Lock()
	defer requirementCacheMu.Unlock()
	if ctx.Err() != nil {
		return
	}
	document, err := s.readRequirementDocumentUnlocked(key)
	if err != nil || document == nil || document.Index.Generation != generation {
		return
	}
	entry, ok := document.Index.Images[imageURL]
	if !ok {
		return
	}
	entry.Error = truncateRunes(message, 500)
	document.Index.Images[imageURL] = entry
	if data, marshalErr := json.Marshal(document.Index); marshalErr == nil {
		if ctx.Err() == nil {
			_ = atomicRequirementWrite(s.paths.Cache, filepath.Join(document.Directory, "index.json"), data)
		}
	}
}

func (s *Service) readRequirementCommentsCache(key string) (*requirementCommentsEnvelope, string, error) {
	requirementCacheMu.Lock()
	defer requirementCacheMu.Unlock()
	return s.readRequirementCommentsCacheUnlocked(key)
}

func (s *Service) readRequirementCommentsCacheUnlocked(key string) (*requirementCommentsEnvelope, string, error) {
	path := filepath.Join(s.paths.Cache, "requirement-comments", requirementCacheDigest(key)+".json")
	if err := requirementSafePath(s.paths.Cache, path); err != nil {
		return nil, path, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, path, nil
	}
	if err != nil {
		return nil, path, err
	}
	if !info.Mode().IsRegular() || requirementCacheReparsePoint(path, info) || info.Size() > maxRequirementCommentsCacheBytes {
		return nil, path, errors.New("评论缓存不是有效普通文件或超过大小限制")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, path, err
	}
	var saved requirementCommentsEnvelope
	if err := strictRequirementJSON(data, &saved); err != nil {
		return nil, path, fmt.Errorf("评论缓存无法读取：%w", err)
	}
	if saved.Version != 1 || saved.Key != requirementCacheDigest(key) || saved.FetchedAt < 0 || saved.Data.Comments == nil || saved.Data.Warnings == nil {
		return nil, path, errors.New("评论缓存格式不匹配")
	}
	return &saved, path, nil
}

func (s *Service) writeRequirementCommentsCache(key string, data domain.RequirementComments, fetchedAt int64) (string, error) {
	requirementCacheMu.Lock()
	defer requirementCacheMu.Unlock()
	path := filepath.Join(s.paths.Cache, "requirement-comments", requirementCacheDigest(key)+".json")
	if data.Comments == nil {
		data.Comments = []domain.RequirementComment{}
	}
	if data.Warnings == nil {
		data.Warnings = []string{}
	}
	bytes, err := json.Marshal(requirementCommentsEnvelope{Version: 1, Key: requirementCacheDigest(key), FetchedAt: fetchedAt, Data: data})
	if err != nil {
		return "", err
	}
	if len(bytes) > maxRequirementCommentsCacheBytes {
		return "", errors.New("评论缓存超过大小限制")
	}
	if err := atomicRequirementWrite(s.paths.Cache, path, bytes); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Service) readRequirementMetadataCache(key string) (*requirementMetadataEnvelope, string, error) {
	path := filepath.Join(s.paths.Cache, "requirement-metadata", requirementCacheDigest(key)+".json")
	if err := requirementSafePath(s.paths.Cache, path); err != nil {
		return nil, path, err
	}
	requirementCacheMu.Lock()
	defer requirementCacheMu.Unlock()
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, path, nil
	}
	if err != nil {
		return nil, path, err
	}
	if !info.Mode().IsRegular() || requirementCacheReparsePoint(path, info) || info.Size() > maxRequirementMetadataCacheBytes {
		return nil, path, errors.New("需求元数据缓存不是有效普通文件或超过大小限制")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, path, err
	}
	var saved requirementMetadataEnvelope
	if err := strictRequirementJSON(data, &saved); err != nil {
		return nil, path, fmt.Errorf("需求元数据缓存无法读取：%w", err)
	}
	if saved.Version != requirementMetadataCacheVersion || saved.Key != requirementCacheDigest(key) || saved.FetchedAt <= 0 ||
		saved.Data.ID == "" || saved.Data.ProjectKey == "" || saved.Data.Type == "" {
		return nil, path, errors.New("需求元数据缓存格式不匹配")
	}
	if saved.Data.Participants.QCOwners == nil {
		saved.Data.Participants.QCOwners = []domain.RequirementPerson{}
	}
	if saved.Data.Participants.ProductManagers == nil {
		saved.Data.Participants.ProductManagers = []domain.RequirementPerson{}
	}
	return &saved, path, nil
}

func (s *Service) writeRequirementMetadataCache(key string, data RequirementMetadataReadResult, fetchedAt int64) (string, error) {
	if fetchedAt <= 0 {
		return "", errors.New("需求元数据缓存缺少有效读取时间")
	}
	if data.Participants.QCOwners == nil {
		data.Participants.QCOwners = []domain.RequirementPerson{}
	}
	if data.Participants.ProductManagers == nil {
		data.Participants.ProductManagers = []domain.RequirementPerson{}
	}
	data.Stale = false
	data.RefreshError = ""
	envelope := requirementMetadataEnvelope{Version: requirementMetadataCacheVersion, Key: requirementCacheDigest(key), FetchedAt: fetchedAt, Data: data}
	encoded, err := marshalRequirementJSON(envelope)
	if err != nil {
		return "", err
	}
	if len(encoded) > maxRequirementMetadataCacheBytes {
		return "", errors.New("需求元数据缓存超过大小限制")
	}
	path := filepath.Join(s.paths.Cache, "requirement-metadata", requirementCacheDigest(key)+".json")
	if err := atomicRequirementWrite(s.paths.Cache, path, encoded); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Service) verifyRequirementIdentity(ctx context.Context, config domain.AppConfig, expected string) error {
	session, loggedOut, err := s.requirementIdentityFor(ctx, config, "", true, s.requirementCacheIdentity)
	if process.IsCleanupFailure(err) {
		return err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if loggedOut {
		return &RequirementIdentityChangedError{State: requirementIdentityLoggedOut}
	}
	if err != nil || session.raw == "" {
		return errors.New("无法确认当前 Meegle 账户，未读取或写入持久缓存")
	}
	if !requirementIdentityMatches(session.raw, expected) {
		return &RequirementIdentityChangedError{State: requirementIdentityAuthenticated, Identity: session.token}
	}
	return nil
}

func (s *Service) requirementCacheIdentity(ctx context.Context, config domain.AppConfig) (string, bool, error) {
	profile, err := s.runMeegle(ctx, config, []string{"config", "profile", "current", "--format", "json"}, 10*time.Second, "读取 Meegle 当前配置")
	if err != nil {
		return "", false, err
	}
	profileName := strings.TrimSpace(profile.Stdout)
	if profileName == "" || len(profileName) > 256 || strings.ContainsAny(profileName, "\r\n") {
		return "", false, errors.New("Meegle 当前配置不可识别")
	}
	readObject := func(args ...string) (map[string]any, error) {
		result, err := s.runMeegle(ctx, config, append(args, "--format", "json"), 15*time.Second, "识别 Meegle 缓存身份")
		if err != nil {
			return nil, err
		}
		decoded, err := decodeJSON(result.Stdout)
		if err != nil || object(decoded) == nil {
			return nil, errors.New("Meegle 身份响应不是 JSON 对象")
		}
		return object(decoded), nil
	}
	auth, err := readObject("auth", "status")
	if err != nil {
		return "", false, err
	}
	authenticated, ok := auth["authenticated"].(bool)
	if !ok {
		return "", false, errors.New("Meegle 登录状态不可识别")
	}
	if !authenticated {
		return "", true, nil
	}
	host := strings.ToLower(text(auth["host"]))
	if host == "" {
		return "", false, errors.New("Meegle 账户 host 不可识别")
	}
	user, err := readObject("user", "me")
	if err != nil {
		return "", false, err
	}
	userKey := text(user["user_key"])
	if userKey == "" {
		return "", false, errors.New("Meegle 用户身份不可识别")
	}
	identityFields := []string{"meegle", s.commandPath(config, domain.ProxyTargetMeegle), profileName, host, userKey}
	for _, name := range []string{"tenant_key", "tenant_id", "enterprise_id"} {
		field := text(auth[name])
		if field == "" {
			field = text(user[name])
		}
		identityFields = append(identityFields, field)
	}
	data, err := marshalRequirementJSON(identityFields)
	return string(data), false, err
}

func requirementDocumentImageURLs(body string) []string {
	locations := requirementImageLocations(body)
	seen := map[string]bool{}
	urls := make([]string, 0, len(locations))
	for _, location := range locations {
		if isRequirementHTTPURL(location.value) && !seen[location.value] {
			seen[location.value] = true
			urls = append(urls, location.value)
		}
	}
	return urls
}

type requirementImageLocation struct {
	start, end int
	value      string
	angled     bool
}

func requirementImageLocations(body string) []requirementImageLocation {
	data := []byte(body)
	md := goldmark.New(goldmark.WithExtensions(extension.GFM))
	doc := md.Parser().Parse(goldtext.NewReader(data))
	definitions := map[string]requirementImageLocation{}
	var walk func(ast.Node)
	walk = func(node ast.Node) {
		if definition, ok := node.(*ast.LinkReferenceDefinition); ok {
			if location, ok := requirementReferenceDefinitionDestination(data, definition.Pos()); ok {
				definitions[util.ToLinkReference(definition.Label)] = location
			}
		}
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			walk(child)
		}
	}
	walk(doc)
	locations := []requirementImageLocation{}
	walk = func(node ast.Node) {
		image, ok := node.(*ast.Image)
		if ok {
			if image.Reference != nil {
				if location, exists := definitions[util.ToLinkReference(image.Reference.Value)]; exists {
					locations = append(locations, location)
				}
			} else if location, exists := requirementInlineImageDestination(data, image.Pos()); exists {
				locations = append(locations, location)
			}
		}
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			walk(child)
		}
	}
	walk(doc)
	seen := map[int]bool{}
	result := make([]requirementImageLocation, 0, len(locations))
	for _, location := range locations {
		if !seen[location.start] {
			seen[location.start] = true
			result = append(result, location)
		}
	}
	return result
}

func requirementReferenceDefinitionDestination(source []byte, pos int) (requirementImageLocation, bool) {
	if pos < 0 || pos >= len(source) {
		return requirementImageLocation{}, false
	}
	end := bytes.IndexByte(source[pos:], '\n')
	if end < 0 {
		end = len(source) - pos
	}
	line := source[pos : pos+end]
	colon := bytes.Index(line, []byte("]:"))
	if colon < 0 {
		return requirementImageLocation{}, false
	}
	cursor := colon + 2
	for cursor < len(line) && (line[cursor] == ' ' || line[cursor] == '\t') {
		cursor++
	}
	location, _, ok := parseRequirementDestination(line, cursor, false)
	if !ok {
		return requirementImageLocation{}, false
	}
	location.start += pos
	location.end += pos
	return location, true
}

func requirementInlineImageDestination(source []byte, pos int) (requirementImageLocation, bool) {
	if pos < 0 || pos >= len(source) {
		return requirementImageLocation{}, false
	}
	if pos+1 >= len(source) || source[pos] != '!' || source[pos+1] != '[' {
		return requirementImageLocation{}, false
	}
	depth, escaped, cursor := 1, false, pos+2
	for cursor < len(source) {
		char := source[cursor]
		if escaped {
			escaped = false
			cursor++
			continue
		}
		if char == '\\' {
			escaped = true
			cursor++
			continue
		}
		if char == '[' {
			depth++
		}
		if char == ']' {
			depth--
			if depth == 0 {
				break
			}
		}
		cursor++
	}
	if cursor >= len(source) || cursor+1 >= len(source) || source[cursor+1] != '(' {
		return requirementImageLocation{}, false
	}
	location, _, ok := parseRequirementDestination(source, cursor+2, true)
	return location, ok
}

func parseRequirementDestination(source []byte, start int, closeParen bool) (requirementImageLocation, int, bool) {
	cursor := start
	if closeParen {
		for cursor < len(source) && (source[cursor] == ' ' || source[cursor] == '\t' || source[cursor] == '\n' || source[cursor] == '\r') {
			cursor++
		}
	}
	if cursor >= len(source) {
		return requirementImageLocation{}, cursor, false
	}
	angled := source[cursor] == '<'
	if angled {
		cursor++
	}
	valueStart, depth, escaped := cursor, 0, false
	for cursor < len(source) {
		char := source[cursor]
		if escaped {
			escaped = false
			cursor++
			continue
		}
		if char == '\\' {
			escaped = true
			cursor++
			continue
		}
		if angled && char == '>' {
			break
		}
		if !angled {
			if char == '(' {
				depth++
			}
			if char == ')' {
				if depth == 0 {
					break
				}
				depth--
			}
			if char == ' ' || char == '\t' || char == '\n' || char == '\r' {
				break
			}
		}
		cursor++
	}
	if cursor == valueStart || angled && (cursor >= len(source) || source[cursor] != '>') {
		return requirementImageLocation{}, cursor, false
	}
	location := requirementImageLocation{start: valueStart, end: cursor, value: string(source[valueStart:cursor]), angled: angled}
	return location, cursor, true
}

func replaceRequirementImageDestinations(body string, replacements map[string]string) string {
	if len(replacements) == 0 {
		return body
	}
	locations := requirementImageLocations(body)
	sort.Slice(locations, func(i, j int) bool { return locations[i].start < locations[j].start })
	for i := len(locations) - 1; i >= 0; i-- {
		location := locations[i]
		if replacement, ok := replacements[location.value]; ok {
			body = body[:location.start] + replacement + body[location.end:]
		}
	}
	return body
}

func isRequirementHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Host != "" && parsed.User == nil && (strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https"))
}

func sortRequirementResources(resources []RequirementResource) {
	// Preserve the order used by the document index, which is sorted for stable JSON output.
	// The caller populates from the index map; sort by URL to make responses deterministic.
	sort.SliceStable(resources, func(i, j int) bool { return resources[i].URL < resources[j].URL })
}

func utf16Length(value string) int { return len(utf16.Encode([]rune(value))) }

func requirementHeaderTitle(value string) string {
	var output strings.Builder
	previousLineBreak := false
	for _, character := range value {
		if character == '\r' || character == '\n' {
			if !previousLineBreak {
				output.WriteByte(' ')
			}
			previousLineBreak = true
			continue
		}
		previousLineBreak = false
		output.WriteRune(character)
	}
	return output.String()
}

func byteOffsetFromUTF16(value string, offset int) (int, bool) {
	if offset < 0 {
		return 0, false
	}
	units, bytesSeen := 0, 0
	for _, r := range value {
		if units == offset {
			return bytesSeen, true
		}
		width := 1
		if r > 0xffff {
			width = 2
		}
		if units+width > offset {
			return 0, false
		}
		units += width
		bytesSeen += len(string(r))
	}
	return bytesSeen, units == offset
}
