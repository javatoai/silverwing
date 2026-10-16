package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/integrations"
)

// Resolve copy paths by reading the identity-checked store. Paths supplied by a
// UI request are never accepted as clipboard targets.
func (a *App) copyRequirementCache(ctx context.Context, action Action, payload map[string]any) (ActionResult, error) {
	kind := "requirements.read"
	if action.Kind == "requirements.copy-comments" {
		kind = "requirements.comments"
	}
	payload["force"] = false
	value, err := a.Integrations.Execute(ctx, kind, payload)
	if err != nil {
		return ActionResult{}, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ActionResult{}, err
	}
	paths := []string{}
	if action.Kind == "requirements.copy-comments" {
		var comments integrations.RequirementCommentsReadResult
		if err := json.Unmarshal(encoded, &comments); err != nil {
			return ActionResult{}, err
		}
		if comments.CachePath == "" || comments.CacheError != "" {
			return ActionResult{}, errors.New("评论文件尚未安全缓存，请刷新评论后重试")
		}
		path, err := validateRequirementCopyArtifact(a.Paths.Cache, comments.CachePath, false)
		if err != nil {
			return ActionResult{}, err
		}
		paths = append(paths, path)
	} else {
		var body integrations.RequirementReadResult
		if err := json.Unmarshal(encoded, &body); err != nil {
			return ActionResult{}, err
		}
		if !body.Complete || body.MarkdownPath == "" || body.CacheError != "" {
			return ActionResult{}, errors.New("需求文件或图片尚未安全缓存，请重试缓存后复制")
		}
		if generation := textPayload(action, "generation"); generation != "" && body.Generation != generation {
			return ActionResult{}, errors.New("需求正文已刷新，请重新读取后复制")
		}
		path, err := validateRequirementCopyArtifact(a.Paths.Cache, body.MarkdownPath, false)
		if err != nil {
			return ActionResult{}, err
		}
		paths = append(paths, path)
		if len(body.Resources) > 0 {
			assets, err := validateRequirementCopyArtifact(filepath.Dir(path), body.AssetsDirectory, true)
			if err != nil {
				return ActionResult{}, err
			}
			paths = append(paths, assets)
		}
	}
	if err := documents.NewClipboardService().CopyFiles(ctx, paths); err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Message: "已复制到系统文件剪贴板", Data: paths}, nil
}

func validateRequirementCopyArtifact(root, path string, directory bool) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("缓存文件路径为空")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", errors.New("缓存文件路径越界")
	}
	current := root
	parts := strings.Split(relative, string(filepath.Separator))
	for index, part := range append([]string{""}, parts...) {
		if part != "" {
			current = filepath.Join(current, part)
		}
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("缓存文件包含符号链接")
		}
		last := index == len(parts)
		if !last && !info.IsDir() {
			return "", fmt.Errorf("缓存文件父目录无效：%s", current)
		}
		if last && (directory && !info.IsDir() || !directory && !info.Mode().IsRegular()) {
			return "", errors.New("缓存文件类型无效")
		}
	}
	return path, nil
}
